package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/epuerta/codex-go/internal/config"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// ToolDefinition represents a tool that can be called by the AI
type ToolDefinition struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef represents a function definition
type FunctionDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// OpenAIAgent implements the Agent interface using OpenAI
type OpenAIAgent struct {
	client         *openai.Client
	config         *config.Config
	tools          []ToolDefinition
	currentContext context.Context
	cancelFunc     context.CancelFunc
	sessionID      string
	history        *ConversationHistory
	historyOpts    HistoryOptions
	mu             sync.Mutex
	currentHandler ResponseHandler
}

// NewOpenAIAgent creates a new OpenAI agent
func NewOpenAIAgent(cfg *config.Config) (*OpenAIAgent, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("OpenAI API key is required")
	}

	clientConfig := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		clientConfig.BaseURL = cfg.BaseURL
	}

	client := openai.NewClientWithConfig(clientConfig)

	// Generate a session ID
	sessionID := uuid.New().String()

	// Create history options
	historyOpts := DefaultHistoryOptions()
	historyOpts.SessionID = sessionID

	// Load instructions from config if available
	if cfg.Instructions != "" {
		historyOpts.SystemPrompt = cfg.Instructions
	}

	// Initialize conversation history
	history, err := NewConversationHistory(historyOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize conversation history: %w", err)
	}

	// Default tools
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "shell",
				Description: "Execute a shell command",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The shell command to execute",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "read_file",
				Description: "Read the contents of a file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "The path to the file",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "write_file",
				Description: "Write content to a file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "The path to the file",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "The content to write",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionDef{
				Name:        "list_directory",
				Description: "List the contents of a directory",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "The path to the directory",
						},
					},
					"required": []string{"path"},
				},
			},
		},
	}

	return &OpenAIAgent{
		client:      client,
		config:      cfg,
		tools:       tools,
		sessionID:   sessionID,
		history:     history,
		historyOpts: historyOpts,
	}, nil
}

// SendMessage sends a message to OpenAI and streams the response
func (a *OpenAIAgent) SendMessage(ctx context.Context, messages []Message, handler ResponseHandler) error {
	a.mu.Lock()
	// Cancel any ongoing request
	if a.cancelFunc != nil {
		a.cancelFunc()
	}

	// Store the handler for potential follow-up calls
	a.currentHandler = handler

	// Create a new context with cancellation
	a.currentContext, a.cancelFunc = context.WithCancel(ctx)
	a.mu.Unlock()

	// Add new messages to history (user input)
	a.history.AddMessages(messages)

	// Get context-aware messages from history
	historyMessages := a.history.GetMessagesForContext()

	// Convert messages to OpenAI format
	var openAIMessages []openai.ChatCompletionMessage
	for _, msg := range historyMessages {
		// Create the base message
		apiMsg := openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content, // Content is used for user, system, assistant (text), tool (result JSON)
		}

		// Handle Assistant requesting tool calls
		if msg.Role == openai.ChatMessageRoleAssistant && len(msg.ToolCalls) > 0 {
			apiMsg.ToolCalls = make([]openai.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				apiMsg.ToolCalls[i] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type), // Assuming type is compatible (e.g., "function")
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
			// Content might be empty or null when tool calls are present
			apiMsg.Content = "" // Or check if msg.Content should be preserved
		}

		// Handle Tool results
		if msg.Role == openai.ChatMessageRoleTool {
			apiMsg.ToolCallID = msg.ToolCallID
			// The 'Name' field is NOT part of ChatCompletionMessage for role 'tool' in go-openai
			// It's implicitly associated via the ToolCallID. The content contains the result.
			// Our history saving in history.go IS correct by adding Name to OUR struct for tracking.
		}

		openAIMessages = append(openAIMessages, apiMsg)
	}

	// Create the request
	req := openai.ChatCompletionRequest{
		Model:       a.config.Model,
		Messages:    openAIMessages,
		Temperature: 0.7,
		Tools:       convertToolDefinitions(a.tools),
		Stream:      true,
	}

	// Start thinking timer
	startTime := time.Now()

	logAgentDebug("[DEBUG] Agent.SendMessage: Creating stream request...")
	// Make the streaming request
	stream, err := a.client.CreateChatCompletionStream(a.currentContext, req)
	if err != nil {
		logAgentDebug("[ERROR] Agent.SendMessage: Error creating stream: %v", err)
		return fmt.Errorf("error creating chat completion stream: %w", err)
	}
	defer stream.Close()
	logAgentDebug("[DEBUG] Agent.SendMessage: Stream created successfully. Starting Recv() loop.")

	// Track function call state
	var currentFunctionCall *openai.FunctionCall
	var currentFunctionCallID string
	var currentContent string
	var currentRole string = "assistant"

	// Process the stream
	for {
		logAgentDebug("[DEBUG] Agent.SendMessage: Calling stream.Recv()...")
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// Send a debug system message
			logAgentDebug("[DEBUG] Agent.SendMessage: Received EOF from stream.")
			a.AddSystemMessage("Stream complete - EOF received")
			break // Exit loop on EOF
		}
		if err != nil {
			logAgentDebug("[ERROR] Agent.SendMessage: Error receiving from stream: %v", err)
			return fmt.Errorf("error receiving from stream: %w", err)
		}
		logAgentDebug("[DEBUG] Agent.SendMessage: stream.Recv() successful. Choices: %d", len(response.Choices))

		// Handle content in the response
		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			logAgentDebug("[DEBUG] Agent.SendMessage: Processing choice 0. Delta Content: %t, Delta ToolCalls: %t, FinishReason: %s", choice.Delta.Content != "", choice.Delta.ToolCalls != nil, choice.FinishReason)

			// Handle delta content (for text response)
			if choice.Delta.Content != "" {
				currentContent += choice.Delta.Content
				logAgentDebug("[DEBUG] Agent.SendMessage: Calling handler with type 'message'. Current content length: %d", len(currentContent))
				handler(ResponseItem{
					Type: "message",
					Message: &Message{
						Role:    currentRole,
						Content: currentContent,
					},
					ThinkingDuration: time.Since(startTime).Milliseconds(),
				})
			}

			// Handle accumulating tool calls data
			if choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0 {
				logAgentDebug("[DEBUG] Agent.SendMessage: Processing Delta.ToolCalls.")
				toolCall := choice.Delta.ToolCalls[0]

				if currentFunctionCall == nil {
					logAgentDebug("[DEBUG] Agent.SendMessage: Initializing new function call. Name: %s, ID: %s", toolCall.Function.Name, toolCall.ID)
					currentFunctionCall = &openai.FunctionCall{
						Name:      toolCall.Function.Name,
						Arguments: toolCall.Function.Arguments,
					}
					currentFunctionCallID = toolCall.ID
				} else {
					logAgentDebug("[DEBUG] Agent.SendMessage: Appending to existing function call arguments.")
					currentFunctionCall.Arguments += toolCall.Function.Arguments
				}
			}

			// Check for FinishReason SEPARATELY
			// Send the completed function call if FinishReason is tool_calls AND we have accumulated call data
			if choice.FinishReason == "tool_calls" && currentFunctionCall != nil {
				logAgentDebug("[DEBUG] Agent.SendMessage: FinishReason is 'tool_calls' and currentFunctionCall is not nil. Preparing function call item.")
				functionCall := &FunctionCall{
					Name:      currentFunctionCall.Name,
					Arguments: currentFunctionCall.Arguments,
					ID:        currentFunctionCallID,
				}

				logAgentDebug("[DEBUG] Agent.SendMessage: Calling handler with type 'function_call'. Name: %s, Args Len: %d, ID: %s", functionCall.Name, len(functionCall.Arguments), functionCall.ID)
				handler(ResponseItem{
					Type:             "function_call",
					FunctionCall:     functionCall,
					ThinkingDuration: time.Since(startTime).Milliseconds(),
				})

				currentFunctionCall = nil
				currentFunctionCallID = ""
			}
		}
	}

	logAgentDebug("[DEBUG] Agent.SendMessage: Exited Recv() loop.")
	// If we have a complete message, add it to history
	if currentContent != "" {
		logAgentDebug("[DEBUG] Agent.SendMessage: Adding final assistant message to history. Length: %d", len(currentContent))
		a.history.AddMessage(Message{
			Role:    currentRole,
			Content: currentContent,
		})

		// Save history to disk
		logAgentDebug("[DEBUG] Agent.SendMessage: Saving history to disk.")
		a.history.Save(a.historyOpts.HistoryPath)
	}

	logAgentDebug("[DEBUG] Agent.SendMessage: Function returning nil error.")
	return nil
}

// SendFileChange sends a file change to the AI for approval
func (a *OpenAIAgent) SendFileChange(ctx context.Context, filePath string, diff string) (*FileChangeConfirmation, error) {
	// In a real implementation, this would send the diff to the AI for approval
	// For now, we'll just return an automatic approval
	return &FileChangeConfirmation{
		Approved: true,
	}, nil
}

// GetCommandConfirmation gets user confirmation for a command
func (a *OpenAIAgent) GetCommandConfirmation(ctx context.Context, command string, args []string) (*CommandConfirmation, error) {
	// In a real implementation, this would get confirmation from the user or AI
	// For now, we'll just return an automatic approval
	return &CommandConfirmation{
		Approved: true,
	}, nil
}

// Cancel cancels the current streaming response
func (a *OpenAIAgent) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelFunc != nil {
		a.cancelFunc()
	}
}

// Close closes the agent and releases any resources
func (a *OpenAIAgent) Close() error {
	a.Cancel()

	// Save history before closing
	if a.history != nil {
		a.history.Save(a.historyOpts.HistoryPath)
	}

	return nil
}

// ClearHistory clears the conversation history
func (a *OpenAIAgent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.history != nil {
		a.history.Clear()
		a.history.Save(a.historyOpts.HistoryPath)
	}
}

// GetHistory returns the conversation history
func (a *OpenAIAgent) GetHistory() *ConversationHistory {
	return a.history
}

// SendFunctionResult adds the tool result to history and then triggers the next AI response stream.
func (a *OpenAIAgent) SendFunctionResult(ctx context.Context, callID, functionName, output string, success bool) error {
	a.mu.Lock()
	// Get the handler before potentially unlocking in defer
	handler := a.currentHandler
	a.mu.Unlock()

	logAgentDebug("[DEBUG] Agent.SendFunctionResult: Received result for CallID: %s, Name: %s, Success: %t", callID, functionName, success)

	// Ensure handler is cleared eventually, though SendMessage already has a defer
	// defer func() {
	// 	a.mu.Lock()
	// 	a.currentHandler = nil
	// 	a.mu.Unlock()
	// }()

	// 1. Add the tool result message to history
	// Retrieve necessary arguments from somewhere (need to pass them in or retrieve)
	// We need the original arguments for the assistant message.
	// A better approach would be to pass the original FunctionCall details through.
	// For now, let's try finding it in history (might be incorrect if history was pruned)
	var originalArgs string
	if a.history != nil {
		msgs := a.history.GetMessages()
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == openai.ChatMessageRoleAssistant && len(msgs[i].ToolCalls) > 0 {
				// Find the matching tool call ID? This assumes only one tool call per assistant msg.
				for _, tc := range msgs[i].ToolCalls {
					if tc.ID == callID {
						originalArgs = tc.Function.Arguments
						break
					}
				}
			}
			if originalArgs != "" {
				break
			} // Stop searching once found
		}
	}
	if originalArgs == "" {
		logAgentDebug("[WARN] Agent.SendFunctionResult: Could not find original arguments for ToolCall ID %s in history. Follow-up request might fail.", callID)
		// We might need to reconstruct arguments or handle this error differently
		originalArgs = "{}" // Default to empty JSON object?
	}

	// Create the Assistant message part
	assistantToolCallMsg := Message{
		Role: openai.ChatMessageRoleAssistant,
		ToolCalls: []ToolCall{
			{
				ID:   callID, // Use the passed callID
				Type: "function",
				Function: FunctionCall{
					Name:      functionName,
					Arguments: originalArgs,
				},
			},
		},
	}

	var content map[string]interface{}
	if success {
		content = map[string]interface{}{"output": output}
	} else {
		content = map[string]interface{}{"error": output}
	}
	// Create the Tool Result message part
	toolResultMessage := Message{
		Role:       openai.ChatMessageRoleTool,
		Content:    string(json.RawMessage(mustMarshal(content))), // Ensure content is valid JSON string
		ToolCallID: callID,
		Name:       functionName,
	}

	if a.history != nil {
		// Add BOTH messages to history IN ORDER
		a.history.AddMessage(assistantToolCallMsg)
		a.history.AddMessage(toolResultMessage)
		logAgentDebug("[DEBUG] Agent.SendFunctionResult: Tool result message added to history.")
	} else {
		logAgentDebug("[ERROR] Agent.SendFunctionResult: History is nil, cannot add tool result message.")
		return fmt.Errorf("agent history is nil") // Return error if history doesn't exist
	}

	// 2. Check if a handler is available (meaning SendMessage is waiting)
	if handler == nil {
		logAgentDebug("[WARN] Agent.SendFunctionResult: No current handler available to send follow-up request.")
		// This might happen if the original SendMessage context was cancelled
		return nil // Or return an error?
	}

	// 3. Prepare and send the follow-up request to OpenAI
	logAgentDebug("[DEBUG] Agent.SendFunctionResult: Preparing follow-up OpenAI request.")
	historyMessages := a.history.GetMessagesForContext()
	var openAIMessages []openai.ChatCompletionMessage
	// Use the same conversion logic as in SendMessage
	for _, msg := range historyMessages {
		apiMsg := openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
		if msg.Role == openai.ChatMessageRoleAssistant && len(msg.ToolCalls) > 0 {
			apiMsg.ToolCalls = make([]openai.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				apiMsg.ToolCalls[i] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolType(tc.Type),
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
			apiMsg.Content = ""
		}
		if msg.Role == openai.ChatMessageRoleTool {
			apiMsg.ToolCallID = msg.ToolCallID
		}
		openAIMessages = append(openAIMessages, apiMsg)
	}

	req := openai.ChatCompletionRequest{
		Model:       a.config.Model,
		Messages:    openAIMessages,
		Temperature: 0.7,
		Tools:       convertToolDefinitions(a.tools),
		Stream:      true,
	}

	logAgentDebug("[DEBUG] Agent.SendFunctionResult: Making follow-up CreateChatCompletionStream call.")
	stream, err := a.client.CreateChatCompletionStream(ctx, req) // Use the passed context
	if err != nil {
		logAgentDebug("[ERROR] Agent.SendFunctionResult: Error creating follow-up stream: %v", err)
		// Should we maybe inform the handler of this error?
		// For now, just return the error.
		return fmt.Errorf("error creating follow-up chat completion stream: %w", err)
	}
	defer stream.Close()

	// 4. Process the new stream, sending results back via the original handler
	logAgentDebug("[DEBUG] Agent.SendFunctionResult: Processing follow-up stream...")
	startTime := time.Now() // Reset start time for this response phase
	var currentContent string
	currentRole := openai.ChatMessageRoleAssistant // Expecting assistant response now
	var currentFunctionCall *openai.FunctionCall   // Added for potential nested calls
	var currentFunctionCallID string               // Added for potential nested calls

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			logAgentDebug("[DEBUG] Agent.SendFunctionResult: Received EOF from follow-up stream.")
			break
		}
		if err != nil {
			logAgentDebug("[ERROR] Agent.SendFunctionResult: Error receiving from follow-up stream: %v", err)
			// Inform handler?
			return fmt.Errorf("error receiving from follow-up stream: %w", err)
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			logAgentDebug("[DEBUG] Agent.SendFunctionResult: Processing choice 0. Delta Content: %t, Delta ToolCalls: %t, FinishReason: %s", choice.Delta.Content != "", choice.Delta.ToolCalls != nil, choice.FinishReason)

			// Handle delta content (for text response)
			if choice.Delta.Content != "" {
				currentContent += choice.Delta.Content
				logAgentDebug("[DEBUG] Agent.SendFunctionResult: Calling handler with type 'message'. Current content length: %d", len(currentContent))
				handler(ResponseItem{
					Type: "message",
					Message: &Message{
						Role:    currentRole,
						Content: currentContent,
					},
					ThinkingDuration: time.Since(startTime).Milliseconds(), // Duration for this part
				})
			}

			// Handle accumulating tool calls data (for potential recursive calls)
			if choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0 {
				logAgentDebug("[DEBUG] Agent.SendFunctionResult: Processing Delta.ToolCalls (nested).")
				toolCall := choice.Delta.ToolCalls[0]

				if currentFunctionCall == nil {
					logAgentDebug("[DEBUG] Agent.SendFunctionResult: Initializing new function call (nested). Name: %s, ID: %s", toolCall.Function.Name, toolCall.ID)
					currentFunctionCall = &openai.FunctionCall{
						Name:      toolCall.Function.Name,
						Arguments: toolCall.Function.Arguments,
					}
					currentFunctionCallID = toolCall.ID
				} else {
					logAgentDebug("[DEBUG] Agent.SendFunctionResult: Appending to existing function call arguments (nested).")
					currentFunctionCall.Arguments += toolCall.Function.Arguments
				}
			}

			// Check for FinishReason SEPARATELY (for potential recursive calls)
			if choice.FinishReason == "tool_calls" && currentFunctionCall != nil {
				logAgentDebug("[DEBUG] Agent.SendFunctionResult: FinishReason is 'tool_calls' (nested). Preparing function call item.")
				functionCall := &FunctionCall{
					Name:      currentFunctionCall.Name,
					Arguments: currentFunctionCall.Arguments,
					ID:        currentFunctionCallID,
				}

				logAgentDebug("[DEBUG] Agent.SendFunctionResult: Calling handler with type 'function_call' (nested). Name: %s, Args Len: %d, ID: %s", functionCall.Name, len(functionCall.Arguments), functionCall.ID)
				handler(ResponseItem{
					Type:             "function_call",
					FunctionCall:     functionCall,
					ThinkingDuration: time.Since(startTime).Milliseconds(),
				})

				// Reset for next potential call in this stream
				currentFunctionCall = nil
				currentFunctionCallID = ""
			}
		}
	}

	logAgentDebug("[DEBUG] Agent.SendFunctionResult: Follow-up stream processing finished.")
	// Add the final assistant message from this stream to history
	if currentContent != "" {
		if a.history != nil {
			a.history.AddMessage(Message{
				Role:    currentRole,
				Content: currentContent,
			})
			logAgentDebug("[DEBUG] Agent.SendFunctionResult: Added final assistant message to history.")
		}
	}

	// Indicate completion of the entire sequence? The original SendMessage completion/error
	// signal might be sufficient if we don't return an error here.
	// Note: The handler clearing is handled by the defer in SendMessage.
	return nil
}

// Helper function to convert ToolDefinition to openai.Tool
func convertToolDefinitions(tools []ToolDefinition) []openai.Tool {
	var result []openai.Tool
	for _, tool := range tools {
		// Convert FunctionDef to openai.FunctionDefinition
		bytes, _ := json.Marshal(tool.Function.Parameters)
		var params json.RawMessage = bytes

		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  params,
			},
		})
	}
	return result
}

// FileChange represents a change to a file
type FileChange struct {
	Filename    string
	Description string
	Content     string
}

// SendFileChanges sends file changes to the agent for context
func (a *OpenAIAgent) SendFileChanges(ctx context.Context, changes []FileChange) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, change := range changes {
		a.history.AddToolMessage("edit_file", map[string]interface{}{
			"target_file":  change.Filename,
			"instructions": change.Description,
			"code_edit":    change.Content,
		}, "")
	}

	// Save history to disk
	return a.history.Save(a.historyOpts.HistoryPath)
}

// AddSystemMessage adds a system message to the conversation history
func (a *OpenAIAgent) AddSystemMessage(content string) error {
	// Don't add debug messages to history
	if strings.Contains(content, "DEBUG:") {
		// Instead, print to stderr
		// fmt.Fprintf(os.Stderr, "%s\n", content)
		return nil
	}

	// If we have a history instance, add the message to it
	if a.history != nil {
		a.history.AddMessage(Message{
			Role:    "system",
			Content: content,
		})
	}
	return nil
}

// GetLastAssistantMessage returns the most recent assistant message
func (a *OpenAIAgent) GetLastAssistantMessage() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Add debug output
	fmt.Fprintf(os.Stderr, "DEBUG: Looking for last assistant message\n")

	if a.history == nil {
		fmt.Fprintf(os.Stderr, "DEBUG: History is nil\n")
		return "", false
	}

	messages := a.history.GetMessages()
	if len(messages) == 0 {
		fmt.Fprintf(os.Stderr, "DEBUG: No messages in history\n")
		return "", false
	}

	// Find the most recent assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			fmt.Fprintf(os.Stderr, "DEBUG: Found assistant message: %s\n", messages[i].Content)
			return messages[i].Content, true
		}
	}

	fmt.Fprintf(os.Stderr, "DEBUG: No assistant messages found\n")
	return "", false
}

// Placeholder definition for logDebug if it doesn't exist
// Ensure you have a proper logging mechanism (e.g., writing to a file)
// For now, just print to stderr for visibility during execution.
func logAgentDebug(format string, args ...interface{}) {
	// In a real app, use a proper logger (e.g., logrus, zap)
	// and write to a file configured via CLI flags or config file.
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// FinalizeInteraction clears the current handler, signifying the end of a request-response cycle.
func (a *OpenAIAgent) FinalizeInteraction() {
	a.mu.Lock()
	defer a.mu.Unlock()
	logAgentDebug("[DEBUG] Agent.FinalizeInteraction: Clearing currentHandler.")
	a.currentHandler = nil
	if a.cancelFunc != nil { // Also cancel context if still active
		a.cancelFunc()
		a.cancelFunc = nil
	}
}

// mustMarshal marshals v to JSON, panicking on error.
func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		// In a real application, handle this more gracefully
		panic(fmt.Sprintf("failed to marshal content for tool result: %v", err))
	}
	return data
}
