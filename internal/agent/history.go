package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// HistoryOptions defines options for conversation history management
type HistoryOptions struct {
	MaxTokenCount int    // Maximum number of tokens to keep in history
	SessionID     string // Unique ID for this conversation session
	HistoryPath   string // Path to store history files
	EnablePersist bool   // Whether to persist history to disk
	SystemPrompt  string // System prompt to prepend to history
}

// DefaultHistoryOptions returns the default options for history management
func DefaultHistoryOptions() HistoryOptions {
	return HistoryOptions{
		MaxTokenCount: 8000,      // Default token limit
		SessionID:     "default", // Default session ID
		HistoryPath:   "",        // Empty means no persistence
		EnablePersist: false,     // Disabled by default
		SystemPrompt: `You are a sophisticated AI coding assistant designed to help with software development tasks in the user's current project context.

Your primary goal is to fulfill the user's request, which may require multiple steps and the use of available tools.

Think step-by-step to break down complex requests.
Plan your actions and use the available tools sequentially as needed.

IMPORTANT: After outlining your plan, immediately proceed to execute the first step using the appropriate tool, unless you need clarification from the user. Do not wait for confirmation to start working.

Available tools include reading/writing/patching files, listing directories, and executing shell commands.
When using tools:
  - For file operations, be precise about paths.
  - For shell commands, ensure they are safe and relevant to the user's request.
If the user's request is ambiguous or requires more information, ask clarifying questions BEFORE proceeding.
Strive to complete the user's objective fully. If you believe the objective is met, inform the user.
If you encounter errors or cannot fulfill the request, explain the issue clearly.
Format code blocks and technical details appropriately using markdown. Be concise but thorough.`,
	}
}

// ConversationHistory manages the conversation history between the user and AI
type ConversationHistory struct {
	Messages       []Message `json:"messages"`
	MaxTokenCount  int       `json:"max_token_count"`
	CurrentTokens  int       `json:"current_tokens"`
	CurrentSession string    `json:"current_session"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	EnablePersist  bool      `json:"-"` // Not stored in JSON
	HistoryPath    string    `json:"-"` // Not stored in JSON
}

// NewConversationHistory creates a new conversation history with the given options
func NewConversationHistory(opts HistoryOptions) (*ConversationHistory, error) {
	history := &ConversationHistory{
		Messages:       []Message{},
		MaxTokenCount:  opts.MaxTokenCount,
		CurrentTokens:  0,
		CurrentSession: opts.SessionID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		EnablePersist:  opts.EnablePersist,
		HistoryPath:    opts.HistoryPath,
	}

	// If persistence is enabled, try to load existing history
	if opts.EnablePersist && opts.HistoryPath != "" {
		historyFile := filepath.Join(opts.HistoryPath, opts.SessionID+".json")
		if _, err := os.Stat(historyFile); err == nil {
			// File exists, try to load it
			data, err := os.ReadFile(historyFile)
			if err == nil {
				if err := json.Unmarshal(data, history); err == nil {
					// Update the history path and persistence flag
					history.HistoryPath = opts.HistoryPath
					history.EnablePersist = opts.EnablePersist
					return history, nil
				}
			}
		}

		// Ensure the directory exists for future saves
		if err := os.MkdirAll(opts.HistoryPath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create history directory: %w", err)
		}
	}

	// Add system prompt if provided
	if opts.SystemPrompt != "" {
		history.AddMessage(Message{
			Role:    "system",
			Content: opts.SystemPrompt,
		})
	}

	return history, nil
}

// AddMessage adds a single message to the history
func (h *ConversationHistory) AddMessage(message Message) {
	h.Messages = append(h.Messages, message)
	h.UpdatedAt = time.Now()

	// Update token count estimation
	h.CurrentTokens = h.EstimateTokenCount()

	// Prune history if needed
	h.pruneIfNeeded()

	// Save to disk if persistence is enabled
	if h.EnablePersist && h.HistoryPath != "" {
		h.Save(h.HistoryPath)
	}
}

// AddToolMessage adds a tool message to the history
func (h *ConversationHistory) AddToolMessage(toolName string, parameters map[string]interface{}, callID string) {
	parametersJSON, _ := json.Marshal(parameters)

	toolMessage := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{
				ID:   callID,
				Type: "function",
				Function: FunctionCall{
					Name:      toolName,
					Arguments: string(parametersJSON),
				},
			},
		},
	}
	h.AddMessage(toolMessage)
}

// AddToolResultMessage adds a tool result message to the history
func (h *ConversationHistory) AddToolResultMessage(callID, toolName string, content map[string]interface{}) {
	contentBytes, _ := json.Marshal(content)
	resultMessage := Message{
		Role:       "tool",
		Content:    string(contentBytes),
		ToolCallID: callID,
		Name:       toolName,
	}
	h.AddMessage(resultMessage)
}

// AddMessages adds multiple messages to the history
func (h *ConversationHistory) AddMessages(messages []Message) {
	for _, msg := range messages {
		h.AddMessage(msg)
	}
}

// GetMessagesForContext returns messages suitable for the AI context
func (h *ConversationHistory) GetMessagesForContext() []Message {
	return h.Messages
}

// GetMessages returns all messages in the history
func (h *ConversationHistory) GetMessages() []Message {
	return h.Messages
}

// GetLastMessage returns the most recent message and a boolean indicating if found
func (h *ConversationHistory) GetLastMessage() (Message, bool) {
	if len(h.Messages) == 0 {
		return Message{}, false
	}
	return h.Messages[len(h.Messages)-1], true
}

// Clear removes all messages from the history
func (h *ConversationHistory) Clear() {
	h.Messages = []Message{}
	h.CurrentTokens = 0
	h.UpdatedAt = time.Now()

	// Save empty history if persistence is enabled
	if h.EnablePersist && h.HistoryPath != "" {
		h.Save(h.HistoryPath)
	}
}

// Save persists the conversation history to disk
func (h *ConversationHistory) Save(path string) error {
	if path == "" {
		return nil // No-op if path is not specified
	}

	// Ensure the directory exists
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create history directory: %w", err)
	}

	// Marshal the history to JSON
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	// Write to file
	historyFile := filepath.Join(path, h.CurrentSession+".json")
	if err := os.WriteFile(historyFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write history file: %w", err)
	}

	return nil
}

// EstimateTokenCount estimates the number of tokens in the conversation history
// This is a simple heuristic based on the number of characters
func (h *ConversationHistory) EstimateTokenCount() int {
	tokenCount := 0

	for _, msg := range h.Messages {
		// Each message has a base overhead
		messageOverhead := 4

		// Roughly estimate 4 characters per token
		contentTokens := int(math.Ceil(float64(len(msg.Content)) / 4))

		// Add to total
		tokenCount += contentTokens + messageOverhead
	}

	return tokenCount
}

// pruneIfNeeded removes older messages if the token count exceeds the maximum
func (h *ConversationHistory) pruneIfNeeded() {
	// If we're under the limit, no pruning needed
	if h.CurrentTokens <= h.MaxTokenCount {
		return
	}

	// We need to prune
	// First, identify system messages to preserve
	var systemMessages []Message
	var otherMessages []Message

	for _, msg := range h.Messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
		} else {
			otherMessages = append(otherMessages, msg)
		}
	}

	// If we have too many messages, start removing older ones
	// We'll remove the oldest non-system messages first
	for len(otherMessages) > 2 && h.EstimateTokenCount() > h.MaxTokenCount {
		// Remove the oldest message (after systems)
		otherMessages = otherMessages[1:]

		// Recalculate with the new set
		h.Messages = append(systemMessages, otherMessages...)
		h.CurrentTokens = h.EstimateTokenCount()
	}

	// If we still exceed the token count, use AI to summarize the conversation
	if h.CurrentTokens > h.MaxTokenCount {
		// Generate a summary of the conversation
		summary, err := h.SummarizeCurrentContext()
		if err == nil && summary != "" {
			// Create a system message with the summary
			summaryMsg := Message{
				Role:    "system",
				Content: summary,
			}

			// Keep system messages plus the summary and the most recent exchanges
			summarizedMessages := []Message{}

			// Add original system messages (instructions, etc.)
			for _, msg := range systemMessages {
				// Skip any previous summary messages
				if !strings.HasPrefix(msg.Content, "Summary of conversation: ") {
					summarizedMessages = append(summarizedMessages, msg)
				}
			}

			// Add the new summary as a system message
			summarizedMessages = append(summarizedMessages, summaryMsg)

			// Add the most recent messages, up to a reasonable number
			recentCount := int(math.Min(float64(len(otherMessages)), 4))
			if recentCount > 0 {
				summarizedMessages = append(summarizedMessages, otherMessages[len(otherMessages)-recentCount:]...)
			}

			h.Messages = summarizedMessages
			h.CurrentTokens = h.EstimateTokenCount()
			return
		}

		// Fallback if summarization fails: just keep a subset of messages
		summarizedMessages := systemMessages

		// Add the most recent messages, up to a reasonable number
		recentCount := int(math.Min(float64(len(otherMessages)), 4))
		if recentCount > 0 {
			summarizedMessages = append(summarizedMessages, otherMessages[len(otherMessages)-recentCount:]...)
		}

		h.Messages = summarizedMessages
		h.CurrentTokens = h.EstimateTokenCount()
	}
}

// SummarizeCurrentContext uses the AI to summarize the conversation
// This is a placeholder for future implementation
func (h *ConversationHistory) SummarizeCurrentContext() (string, error) {
	// Implement actual summarization using OpenAI
	// First, get all messages since the last system message that's a summary
	var messagesToSummarize []Message
	var systemMessages []Message

	// Find messages to summarize (non-system) and preserve system messages
	for _, msg := range h.Messages {
		if msg.Role == "system" {
			// Check if this is already a summary we generated
			if strings.HasPrefix(msg.Content, "Summary of conversation: ") {
				// Don't include previous summaries in our list to summarize
				continue
			}
			systemMessages = append(systemMessages, msg)
		} else {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}

	// If we don't have enough messages to summarize, just return a basic count
	if len(messagesToSummarize) < 5 {
		messageCount := len(h.Messages)
		systemCount := len(systemMessages)
		userCount := 0
		assistantCount := 0

		for _, msg := range messagesToSummarize {
			switch msg.Role {
			case "user":
				userCount++
			case "assistant":
				assistantCount++
			}
		}

		summary := fmt.Sprintf(
			"Summary of conversation: %d messages (%d system, %d user, %d assistant)",
			messageCount, systemCount, userCount, assistantCount,
		)

		return summary, nil
	}

	// Otherwise, prepare messages for the summarization request
	// We need to create a new OpenAI client for this request
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		// Fall back to basic summary if we don't have an API key
		return fmt.Sprintf("Summary of conversation: %d messages", len(h.Messages)), nil
	}

	client := openai.NewClient(apiKey)

	// Prepare conversation for summarization
	var conversationText strings.Builder
	for _, msg := range messagesToSummarize {
		conversationText.WriteString(fmt.Sprintf("%s: %s\n\n", msg.Role, msg.Content))
	}

	// Create a completion request for summarization
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: "gpt-3.5-turbo", // Use a smaller model for summarization
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    "system",
					Content: "You are a helpful assistant that summarizes conversations. Create a concise summary of the following conversation, focusing on the key points and actions taken.",
				},
				{
					Role:    "user",
					Content: conversationText.String(),
				},
			},
			MaxTokens: 300,
		},
	)

	if err != nil {
		// If summarization fails, fall back to basic summary
		return fmt.Sprintf("Summary of conversation: %d messages", len(h.Messages)), nil
	}

	// Get the summary from the response
	if len(resp.Choices) > 0 {
		summary := "Summary of conversation: " + resp.Choices[0].Message.Content
		return summary, nil
	}

	// Fall back to basic summary if something went wrong
	return fmt.Sprintf("Summary of conversation: %d messages", len(h.Messages)), nil
}
