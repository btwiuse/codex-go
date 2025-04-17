package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/config"
	"github.com/epuerta/codex-go/internal/functions"
	"github.com/epuerta/codex-go/internal/sandbox"
	"github.com/epuerta/codex-go/internal/ui"
	"github.com/google/uuid"
)

// --- Agent Interaction Messages ---

type startAgentStreamMsg struct {
	content string
}

type agentResponseMsg struct {
	item agent.ResponseItem
}

type agentErrorMsg struct {
	err error
}

type agentStreamCompleteMsg struct{}

type agentFollowUpCompleteMsg struct{}

// Represents a function result to be sent back to the agent
type sendFunctionResultMsg struct {
	ctx          context.Context
	functionName string // Name from the *original* call
	callID       string // ID from the *original* call
	originalArgs string // Arguments JSON from the *original* call
	output       string // Result content from execution
	success      bool   // Result status from execution
}

// UserInputSubmitMsg signals that the user pressed Enter in the chat input
type UserInputSubmitMsg struct {
	Content string
}

// --- End Agent Interaction Messages ---

// App represents the main application and is the top-level Bubble Tea model
type App struct {
	Agent            agent.Agent
	ChatModel        ui.ChatModel // ChatModel is now a sub-model
	Config           *config.Config
	FunctionRegistry *functions.Registry
	IsRunning        bool
	Sandbox          sandbox.Sandbox

	// Rollout tracking
	CurrentRollout *AppRollout
	RolloutPath    string

	// We need width/height for layout
	width  int
	height int

	agentMsgChan      chan tea.Msg // Channel for agent messages
	isFirstAgentChunk bool         // Track if we are processing the first chunk of a stream
	isAgentProcessing bool         // Track if the agent is busy with a request/response cycle
}

// AppRollout represents a saved session that can be loaded later
type AppRollout struct {
	Messages      []agent.Message `json:"messages"`
	Responses     []agent.Message `json:"responses"`
	CommandsRun   []string        `json:"commands_run"`
	FilesModified []string        `json:"files_modified"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	SessionID     string          `json:"session_id"`
}

// NewApp creates a new application instance
func NewApp(config *config.Config) (*App, error) {
	// Initialize the agent
	a, err := agent.NewOpenAIAgent(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize agent: %w", err)
	}

	// Create chat model (no callback needed here)
	chatModel := ui.NewChatModel()

	// Set the agent reference in the chat model for history access
	chatModel.SetAgent(a)

	// Set the session info with the current information
	sessionID := uuid.New().String()[:16]
	chatModel.SetSessionInfo(
		sessionID,
		config.CWD,
		config.Model,
		string(config.ApprovalMode),
	)

	// Create function registry
	registry := functions.NewRegistry()

	// Register core functions
	registry.Register("read_file", functions.ReadFile)
	registry.Register("write_file", functions.WriteFile)
	registry.Register("patch_file", functions.PatchFile)
	registry.Register("execute_command", functions.ExecuteCommand)
	registry.Register("list_directory", functions.ListDirectory)

	// Create sandbox
	sb := sandbox.NewSandbox()

	app := &App{
		Agent:            a,
		ChatModel:        chatModel,
		Config:           config,
		FunctionRegistry: registry,
		IsRunning:        false,
		Sandbox:          sb,
		agentMsgChan:     make(chan tea.Msg), // Initialize channel
	}

	// Initialize repository context if not disabled
	if !config.DisableProjectDoc {
		if err := app.initRepositoryContext(); err != nil {
			// Log the error but continue
			// fmt.Fprintf(os.Stderr, "Warning: Failed to initialize repository context: %v\n", err) // Commented out again
		}
	}

	return app, nil
}

// Init initializes the application model
func (app *App) Init() tea.Cmd {
	// Start the dedicated channel listener command
	return tea.Batch(app.ChatModel.Init(), app.listenForAgentMessages())
}

// listenForAgentMessages returns a command that continuously listens on the
// agent message channel and sends received messages back to the App's Update loop.
func (app *App) listenForAgentMessages() tea.Cmd {
	return func() tea.Msg {
		msg := <-app.agentMsgChan // Block and wait for the next message
		logDebug("[DEBUG] listenForAgentMessages: Received %T from channel, returning to Update.", msg)
		// --- Revert: Return the message directly ---
		return msg
	}
}

// Update handles messages for the application model
func (app *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd // Keep cmd for potential non-agent commands from ChatModel.Update
	var agentMessageHandled bool = false
	var skipChatModelUpdate bool = false // Use flag

	// Remove debug logging
	// fmt.Fprintf(os.Stderr, "App.Update received msg type: %T\n", msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		app.width = msg.Width
		app.height = msg.Height
		// Allow ChatModel to handle the rest below

	case tea.KeyMsg:
		// Only handle Quit keys here
		// fmt.Fprintf(os.Stderr, "App.Update: KeyMsg received: %v, Type: %v, String: %q\n", msg, msg.Type, msg.String())
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc || (msg.String() == "q" && app.ChatModel.InputIsEmpty()) {
			app.IsRunning = false
			return app, tea.Quit // Quit directly
		}
		// Other keys fall through to ChatModel.Update below

	// --- NEW: Handle submit message from ChatModel ---
	case ui.UserInputSubmitMsg:
		// --- BEGIN /clear and /help handling ---
		if strings.HasPrefix(msg.Content, "/") {
			command := strings.TrimSpace(msg.Content)
			if command == "/clear" {
				logDebug("[DEBUG] User command: /clear")
				app.Agent.ClearHistory()                                // Clear agent's internal history
				app.ChatModel.ClearMessages()                           // Clear UI messages
				app.ChatModel.AddSystemMessage("Chat history cleared.") // Notify user
				skipChatModelUpdate = true
				cmd = nil // No further command needed immediately
				// Ensure we don't proceed to agent processing
			} else if command == "/help" {
				logDebug("[DEBUG] User command: /help")
				helpText := `Codex-Go Help:
  /clear : Clears the current conversation history.
  /help  : Shows this help message.
  Ctrl+C : Quits the application.
  Enter  : Sends your message to the assistant.`
				app.ChatModel.AddSystemMessage(helpText)
				skipChatModelUpdate = true
				cmd = nil // No further command needed immediately
				// Ensure we don't proceed to agent processing
			} else {
				// Unknown command, maybe notify user or just ignore?
				app.ChatModel.AddSystemMessage(fmt.Sprintf("Unknown command: %s", command))
				skipChatModelUpdate = true
				cmd = nil
			}
		} else {
			// --- Not a command, proceed with normal message handling ---
			// --- FIX: Check if agent is already processing ---
			if app.isAgentProcessing {
				logDebug("[WARN] User submitted input while agent is processing. Ignoring.")
				skipChatModelUpdate = true
				cmd = nil
			} else {
				logDebug("[DEBUG] User submitted input. Starting agent stream.")
				app.ChatModel.AddUserMessage(msg.Content)
				app.ChatModel.StartThinking()
				app.isFirstAgentChunk = true
				app.isAgentProcessing = true // <-- Set agent busy flag
				cmd = app.listenAgentStreamCmd(msg.Content)
				skipChatModelUpdate = true
			}
		}
		// --- END /clear and /help handling ---

	// --- Agent Message Cases (Handled by App) ---
	case agentResponseMsg:
		app.handleAgentResponseItem(msg.item)
		cmd = app.listenForAgentMessages()
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentErrorMsg:
		app.ChatModel.AddSystemMessage(fmt.Sprintf("Error: %v", msg.err))
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false // <-- Clear agent busy flag
		cmd = app.listenForAgentMessages()
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentStreamCompleteMsg: // Completion of initial stream WITHOUT tool calls
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false // <-- Clear agent busy flag
		cmd = app.listenForAgentMessages()
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentFollowUpCompleteMsg: // Completion of a follow-up stream
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false // <-- Clear agent busy flag
		cmd = app.listenForAgentMessages()
		agentMessageHandled = true
		skipChatModelUpdate = true

	case sendFunctionResultMsg:
		app.sendFunctionResultCmd(msg)
		cmd = app.listenForAgentMessages()
		agentMessageHandled = true
		skipChatModelUpdate = true

	}

	// Pass message down to ChatModel ONLY if it wasn't handled above
	if !skipChatModelUpdate {
		var updatedChatModel tea.Model
		updatedChatModel, cmd = app.ChatModel.Update(msg) // Assign cmd here
		if updatedChatModelTyped, ok := updatedChatModel.(ui.ChatModel); ok {
			app.ChatModel = updatedChatModelTyped
		}
		// Don't append cmd here if it was already set by a handled message
	} else if cmd == nil {
		// If handled BUT no specific cmd was returned (e.g. StopThinking messages)
		// ensure cmd is nil so we don't Batch an empty command later.
		// Note: This might need refinement depending on how tea.Batch handles nil cmds
	}

	// Force viewport update if an agent message modified the state
	if agentMessageHandled {
		app.ChatModel.ForceUpdateViewport()
	}

	// Return the single command OR batch if necessary (though batching is less likely now)
	// If cmd is nil from handled messages, tea.Batch(nil) might be okay or need explicit nil return?
	// Let's return cmd directly for now, assuming non-batch cases are dominant. Check Bubble Tea docs if batching nil is problematic.
	if cmd != nil {
		return app, cmd
	}

	// fmt.Fprintf(os.Stderr, "App.Update finished for msg type: %T\n", msg)
	return app, nil // Return nil command if nothing else to do
}

// View renders the application UI
func (app *App) View() string {
	// Delegate rendering to the ChatModel
	return app.ChatModel.View()
}

// Placeholder for the debug logger function definition - it should be here somewhere
// func logDebug(format string, args ...interface{}) { ... }
// Let's assume it's defined elsewhere or remove its calls for now

// listenAgentStreamCmd starts the agent stream goroutine which sends messages to app.agentMsgChan
func (app *App) listenAgentStreamCmd(content string) tea.Cmd {
	logDebug("[DEBUG] listenAgentStreamCmd: Starting agent stream goroutine for content: '%s'", content)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // Use a timeout
		defer cancel()

		message := agent.Message{Role: "user", Content: content}

		logDebug("[DEBUG] listenAgentStreamCmd: Goroutine started. Calling Agent.SendMessage...")
		streamEndedWithTools, err := app.Agent.SendMessage(ctx, []agent.Message{message}, func(itemJSON string) {
			// ADD LOG: Log the raw JSON received
			logDebug("[DEBUG] listenAgentStreamCmd Handler: Received JSON string: '%s'", itemJSON) // Log raw JSON

			// Unmarshal the JSON string back into a ResponseItem
			var item agent.ResponseItem
			err := json.Unmarshal([]byte(itemJSON), &item)
			if err != nil {
				logDebug("[ERROR] listenAgentStreamCmd Handler: Failed to unmarshal ResponseItem JSON: %v", err)
				// Send an error message back?
				app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("failed to unmarshal agent response: %w", err)}
				return
			}

			// --- FIX: Send specific message based on item.Type ---
			switch item.Type {
			case "message", "function_call":
				// Deep copy FunctionCall if present
				fcCopy := item.FunctionCall
				if item.FunctionCall != nil {
					copiedFC := *item.FunctionCall
					fcCopy = &copiedFC
				}
				itemToSend := agent.ResponseItem{
					Type:             item.Type,
					Message:          item.Message,
					FunctionCall:     fcCopy,
					ThinkingDuration: item.ThinkingDuration,
				}
				logDebug("[DEBUG] listenAgentStreamCmd Handler: Sending agentResponseMsg to channel (Type: %s).", item.Type)
				app.agentMsgChan <- agentResponseMsg{item: itemToSend}
			case "followup_complete":
				logDebug("[DEBUG] listenAgentStreamCmd Handler: Sending agentFollowUpCompleteMsg to channel.")
				app.agentMsgChan <- agentFollowUpCompleteMsg{}
			default:
				logDebug("[WARN] listenAgentStreamCmd Handler: Received unknown item type '%s'. Ignoring.", item.Type)
			}
		})
		logDebug("[DEBUG] listenAgentStreamCmd: Goroutine finished Agent.SendMessage call. Error: %v, EndedWithTools: %t", err, streamEndedWithTools)

		// After the stream is done, send completion or error message
		if err != nil {
			logDebug("[DEBUG] listenAgentStreamCmd: Goroutine sending agentErrorMsg to channel.")
			app.agentMsgChan <- agentErrorMsg{err: err}
		} else if !streamEndedWithTools {
			// --- FIX: Send complete ONLY if no error AND didn't end with tools ---
			logDebug("[DEBUG] listenAgentStreamCmd: Goroutine finished normally and without tool calls. Sending agentStreamCompleteMsg.")
			app.agentMsgChan <- agentStreamCompleteMsg{}
		} else {
			// Stream ended normally requesting tools, do nothing, wait for follow-up.
			logDebug("[DEBUG] listenAgentStreamCmd: Goroutine finished normally, ended with tool calls. NOT sending agentStreamCompleteMsg.")
		}
	}()

	// --- Return nil --- The listener started in Init handles receiving messages
	logDebug("[DEBUG] listenAgentStreamCmd: Returning nil command.")
	return nil
}

// handleAgentResponseItem processes a single response item from the agent
// --- Returns nil because subsequent actions are triggered by messages on the channel ---
func (app *App) handleAgentResponseItem(item agent.ResponseItem) /* tea.Cmd - REMOVED */ {
	logDebug("[DEBUG] App.handleAgentResponseItem received item type: %s", item.Type)

	switch item.Type {
	case "message":
		logDebug("[DEBUG] Handling 'message' item.")
		if item.Message != nil {
			logDebug("[DEBUG] Message item content: %q", item.Message.Content)
			logDebug("[DEBUG] isFirstAgentChunk state *before* processing message: %t", app.isFirstAgentChunk)
			app.ChatModel.SetThinkingStatus(fmt.Sprintf("Receiving message chunk..."))
			content := item.Message.Content
			logDebug("[DEBUG] Message content length: %d", len(content))

			// Add a debug system message to track message flow
			// app.ChatModel.AddSystemMessage(fmt.Sprintf("Agent is responding (%d chars)", len(content)))

			if app.isFirstAgentChunk {
				logDebug("[DEBUG] isFirstAgentChunk=true, adding new assistant message.")
				app.ChatModel.AddAssistantMessage(content)
				app.isFirstAgentChunk = false
				// app.ChatModel.AddSystemMessage("Added new assistant message")
				logDebug("[DEBUG] Called AddAssistantMessage, set isFirstAgentChunk=false")
			} else {
				logDebug("[DEBUG] isFirstAgentChunk=false, updating last assistant message.")
				app.ChatModel.UpdateLastAssistantMessage(content)
				// app.ChatModel.AddSystemMessage("Updated existing assistant message")
				logDebug("[DEBUG] Called UpdateLastAssistantMessage")
			}

			// Force UI update to show the new message
			app.ChatModel.ForceUpdateViewport()
			logDebug("[DEBUG] Called ForceUpdateViewport in message handler.")
		} else {
			logDebug("[WARN] Handling 'message' item, but item.Message is nil.")
		}
		// After processing message, wait for the next item from the *same* stream
		logDebug("[DEBUG] App.handleAgentResponseItem finished processing message.")

	case "function_call":
		if item.FunctionCall != nil {
			logDebug("[DEBUG] handleAgentResponseItem received function_call. Args: '%s'", item.FunctionCall.Arguments)
		} else {
			logDebug("[DEBUG] handleAgentResponseItem received function_call, but item.FunctionCall is nil.")
		}

		logDebug("[DEBUG] Handling 'function_call' item.")
		if item.FunctionCall != nil {
			app.ChatModel.SetThinkingStatus(fmt.Sprintf("Calling %s...", item.FunctionCall.Name))
			logDebug("[DEBUG] Function Call Name: %s, Args: %s, ID: %s", item.FunctionCall.Name, item.FunctionCall.Arguments, item.FunctionCall.ID)

			// Add UI element showing the function call is happening
			app.ChatModel.AddFunctionCallMessage(item.FunctionCall.Name, item.FunctionCall.Arguments)
			app.ChatModel.ForceUpdateViewport()

			// Add a user-visible message about what's happening
			statusMsg := fmt.Sprintf("Executing function: %s", item.FunctionCall.Name)
			app.ChatModel.AddSystemMessage(statusMsg)
			app.ChatModel.ForceUpdateViewport()

			var agentOutput string
			var success bool

			// --- Execute Command Case ---
			if item.FunctionCall.Name == "execute_command" {
				var args map[string]interface{}
				cmdStr := ""
				if err := json.Unmarshal([]byte(item.FunctionCall.Arguments), &args); err != nil {
					logDebug("[ERROR] Failed to unmarshal execute_command args: %v", err)
					agentOutput = fmt.Sprintf("Error parsing command args: %v", err)
					success = false
					app.ChatModel.AddSystemMessage(agentOutput)
				} else {
					var ok bool
					cmdStr, ok = args["command"].(string)
					if !ok || cmdStr == "" {
						logDebug("[ERROR] Missing or invalid 'command' argument in execute_command.")
						agentOutput = "Missing command argument for execute_command"
						success = false
						app.ChatModel.AddSystemMessage(agentOutput)
					} else {
						// Execute the command via Sandbox
						app.ChatModel.SetThinkingStatus(fmt.Sprintf("Executing: %s", cmdStr))
						result, err := app.Sandbox.Execute(context.Background(), sandbox.SandboxOptions{
							Command:    cmdStr,
							WorkingDir: app.Config.CWD, // Use configured CWD
							Timeout:    30 * time.Second,
						})
						logDebug("[DEBUG] Sandbox execution result: ExitCode=%d, StdoutLen=%d, StderrLen=%d, Error=%v", result.ExitCode, len(result.Stdout), len(result.Stderr), err)

						// Create CommandResult for UI
						uiResult := &ui.CommandResult{
							Command:  cmdStr,
							Stdout:   result.Stdout,
							Stderr:   result.Stderr,
							ExitCode: result.ExitCode,
							Duration: result.Duration, // Sandbox provides duration
							Error:    err,             // Include sandbox execution error
						}
						app.ChatModel.AddCommandMessage(cmdStr, uiResult)
						app.ChatModel.ForceUpdateViewport()

						// Determine result/output for agent
						agentOutput = result.Stdout
						success = err == nil && result.ExitCode == 0
						if !success {
							if err != nil {
								agentOutput = fmt.Sprintf("Execution Error: %v", err)
							} else {
								agentOutput = fmt.Sprintf("Command Failed (code %d): %s", result.ExitCode, result.Stderr)
							}
						}
					}
				}
				// Special case for read_file OR OTHER functions
			} else {
				fn := app.FunctionRegistry.Get(item.FunctionCall.Name)
				if fn == nil {
					agentOutput = fmt.Sprintf("Unknown function: %s", item.FunctionCall.Name)
					success = false
					logDebug("[ERROR] %s", agentOutput)
					app.ChatModel.AddSystemMessage(agentOutput)
				} else {
					// Execute the function from registry
					result, err := fn(item.FunctionCall.Arguments)
					logDebug("[DEBUG] Function '%s' execution result: ResultLen=%d, Error=%v", item.FunctionCall.Name, len(result), err)
					success = err == nil
					agentOutput = result
					if err != nil {
						agentOutput = fmt.Sprintf("Error: %v", err)
						app.ChatModel.AddSystemMessage(agentOutput) // Show error in UI too
					}

					// Add specific UI updates based on function if needed (e.g., read_file preview)
					if item.FunctionCall.Name == "read_file" && success {
						previewLen := 200 // characters
						preview := result
						if len(preview) > previewLen {
							preview = preview[:previewLen] + "..."
						}
						app.ChatModel.AddSystemMessage(fmt.Sprintf("Read file content (preview): %s", preview))
					}

					// Display result using specific function result message
					app.ChatModel.AddFunctionResultMessage(agentOutput, !success)
					app.ChatModel.ForceUpdateViewport()
				}
			}

			// After displaying function results, expect a new AI response
			app.isFirstAgentChunk = true

			// Show a message that we're sending results back to the assistant
			app.ChatModel.AddSystemMessage("Sending function results to assistant...")
			app.ChatModel.ForceUpdateViewport()

			// Prepare the message to send back results
			resultMsg := sendFunctionResultMsg{
				ctx:          context.Background(),
				functionName: item.FunctionCall.Name,
				callID:       item.FunctionCall.ID,
				originalArgs: item.FunctionCall.Arguments,
				output:       agentOutput,
				success:      success,
			}

			// --- FIX: Send the result message asynchronously ---
			logDebug("[DEBUG] App.handleAgentResponseItem: Starting goroutine to send sendFunctionResultMsg.")
			go func() {
				app.agentMsgChan <- resultMsg
				logDebug("[DEBUG] App.handleAgentResponseItem: Goroutine finished sending sendFunctionResultMsg.")
			}()
			// Return immediately
		}
	}

	// Return nil is implicit
	logDebug("[WARN] App.handleAgentResponseItem received unhandled item type: %s.", item.Type)
}

// sendFunctionResultCmd processes the function result and sends it back to the agent
// --- Returns nil because subsequent actions are triggered by messages on the channel ---
func (app *App) sendFunctionResultCmd(msg sendFunctionResultMsg) /* tea.Cmd - REMOVED */ {
	logDebug("[DEBUG] sendFunctionResultCmd: Preparing to send result for %s (callID: %s)", msg.functionName, msg.callID)
	if app.Agent != nil {
		// --- FIX: Run SendFunctionResult in a goroutine to avoid blocking Update loop ---
		go func() {
			err := app.Agent.SendFunctionResult(msg.ctx, msg.callID, msg.functionName, msg.output, msg.success)
			logDebug("[DEBUG] sendFunctionResultCmd Goroutine: Agent.SendFunctionResult returned error: %v", err)
			if err != nil {
				// Put an error message on the channel
				logDebug("[ERROR] sendFunctionResultCmd Goroutine: Sending agentErrorMsg due to SendFunctionResult failure.")
				app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("failed to send function result for %s: %w", msg.functionName, err)}
			} else {
				// Success case - the follow-up stream handler will put messages (or followup_complete) on the channel
				logDebug("[DEBUG] sendFunctionResultCmd Goroutine: Agent.SendFunctionResult success. Handler will send next messages.")
			}
		}()

		// *CRITICAL FIX*: Always reset isFirstAgentChunk to true AFTER INITIATING the send
		app.isFirstAgentChunk = true
		logDebug("[DEBUG] sendFunctionResultCmd: Reset isFirstAgentChunk=true")

		// Show a visible system message about what's happening
		app.ChatModel.AddSystemMessage("Function complete - waiting for assistant response...")
		app.ChatModel.ForceUpdateViewport()

		// Display a temporary message to show the function was executed
		app.ChatModel.SetThinkingStatus("Function executed, waiting for assistant response...")

		// --- Don't return wait cmd ---
		logDebug("[DEBUG] sendFunctionResultCmd: Finished initiating send.")
	} else {
		logDebug("[ERROR] sendFunctionResultCmd: Agent is nil!")
		// Maybe send error message directly? This shouldn't happen.
		app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("agent is nil, cannot send function result")}
	}
}

// needsApprovalForFunction determines if a function needs approval based on the current mode
func (app *App) needsApprovalForFunction(functionName string) bool {
	switch app.Config.ApprovalMode {
	case config.Suggest:
		// In suggest mode, all operations except reading need approval
		return functionName != "read_file" && functionName != "list_directory"
	case config.AutoEdit:
		// In auto-edit mode, only commands need approval
		return functionName == "execute_command"
	case config.FullAuto:
		// In full-auto mode, nothing needs approval
		return false
	default:
		// Default to most restrictive (suggest)
		return functionName != "read_file" && functionName != "list_directory"
	}
}

// askForApproval prompts the user for approval of a function call
func (app *App) askForApproval(functionName, args string) (bool, string) {
	// Determine what kind of operation is being performed
	var operationType, title, description string
	switch functionName {
	case "write_file":
		operationType = "write to file"
		title = "Approve File Write"
		description = "The assistant wants to write to a file on your filesystem"
	case "patch_file":
		operationType = "patch file"
		title = "Approve File Patch"
		description = "The assistant wants to modify a file on your filesystem"
	case "execute_command":
		operationType = "execute command"
		title = "Approve Command Execution"
		description = "The assistant wants to execute a shell command"
	default:
		operationType = "perform operation"
		title = "Approve Operation"
		description = "The assistant wants to perform an operation"
	}

	// Add a system message to show the approval request
	app.ChatModel.AddSystemMessage(fmt.Sprintf("Waiting for approval to %s: %s", operationType, args))

	// Use the approval UI to get user confirmation
	approved, err := ui.GetApproval(title, description, args)
	if err != nil {
		app.ChatModel.AddSystemMessage(fmt.Sprintf("Error in approval process: %v", err))
		return false, fmt.Sprintf("Error: %v", err)
	}

	var message string
	if approved {
		message = "Operation approved by user"
	} else {
		message = "Operation denied by user"
	}

	app.ChatModel.AddSystemMessage(message)
	return approved, message
}

// initRepositoryContext loads project-specific context from codex.md files
func (app *App) initRepositoryContext() error {
	repoContext, err := app.loadRepositoryContext()
	if err != nil {
		return err
	}

	if repoContext == "" {
		// No project context found, that's fine
		return nil
	}

	// Add the repository context as a system message
	ctx := context.Background()
	systemMsg := agent.Message{
		Role:    "system",
		Content: "Repository Context:\n" + repoContext,
	}

	// Add the message to the agent's history
	_, err = app.Agent.SendMessage(ctx, []agent.Message{systemMsg}, func(itemJSON string) {
		// No streamed response expected here, but handler signature must match.
		// We could potentially log itemJSON if needed for debugging.
	})
	return err // Return the error from SendMessage
}

// loadRepositoryContext looks for and loads codex.md files
func (app *App) loadRepositoryContext() (string, error) {
	var contextParts []string

	// Check for ProjectDocPath set in config (highest priority)
	if app.Config.ProjectDocPath != "" {
		data, err := os.ReadFile(app.Config.ProjectDocPath)
		if err != nil {
			return "", fmt.Errorf("failed to read project doc from path %s: %w", app.Config.ProjectDocPath, err)
		}
		contextParts = append(contextParts, string(data))
	}

	// Look for codex.md in the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	// Try to find the repository root by looking for a .git directory
	repoRoot, err := findRepositoryRoot(cwd)
	if err == nil {
		// Look for codex.md in the repo root (if different from cwd)
		if repoRoot != cwd {
			repoRootDocPath := filepath.Join(repoRoot, "codex.md")
			if _, err := os.Stat(repoRootDocPath); err == nil {
				data, err := os.ReadFile(repoRootDocPath)
				if err == nil {
					contextParts = append(contextParts, fmt.Sprintf("Repository Root codex.md:\n%s", string(data)))
				}
			}
		}
	}

	// Look for codex.md in the current directory
	cwdDocPath := filepath.Join(cwd, "codex.md")
	if _, err := os.Stat(cwdDocPath); err == nil {
		data, err := os.ReadFile(cwdDocPath)
		if err == nil {
			contextParts = append(contextParts, fmt.Sprintf("Current Directory codex.md:\n%s", string(data)))
		}
	}

	// Return the combined context
	return strings.Join(contextParts, "\n\n---\n\n"), nil
}

// findRepositoryRoot walks up the directory tree to find the repository root
func findRepositoryRoot(startDir string) (string, error) {
	currentDir := startDir
	for {
		// Check if the current directory has a .git subdirectory
		gitDir := filepath.Join(currentDir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			// Found the repository root
			return currentDir, nil
		}

		// Move up to the parent directory
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// Reached the filesystem root without finding a repository
			return "", fmt.Errorf("no git repository found")
		}
		currentDir = parentDir
	}
}

// SaveRollout saves the current session to a file
func (app *App) SaveRollout() error {
	if app.CurrentRollout == nil {
		app.CurrentRollout = &AppRollout{
			CreatedAt: time.Now(),
			SessionID: uuid.New().String(),
		}
	}

	// Update rollout data
	app.CurrentRollout.UpdatedAt = time.Now()

	// Get the messages from the history
	history := app.Agent.GetHistory()
	if history != nil {
		app.CurrentRollout.Messages = history.GetMessages()
	}

	// If rollout path not set, create a default one
	if app.RolloutPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}

		// Create rollouts directory if it doesn't exist
		rolloutsDir := filepath.Join(homeDir, ".codex", "rollouts")
		if err := os.MkdirAll(rolloutsDir, 0755); err != nil {
			return fmt.Errorf("failed to create rollouts directory: %w", err)
		}

		app.RolloutPath = filepath.Join(rolloutsDir, fmt.Sprintf("codex-session-%s.json", timestamp))
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(app.CurrentRollout, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal rollout: %w", err)
	}

	// Save to file
	if err := os.WriteFile(app.RolloutPath, data, 0644); err != nil {
		return fmt.Errorf("failed to save rollout: %w", err)
	}

	return nil
}

// LoadRollout loads a saved session from a file
func (app *App) LoadRollout(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read rollout file: %w", err)
	}

	var rollout AppRollout
	if err := json.Unmarshal(data, &rollout); err != nil {
		return fmt.Errorf("failed to unmarshal rollout: %w", err)
	}

	app.CurrentRollout = &rollout
	app.RolloutPath = path

	// Add the messages to the chat model
	for _, msg := range rollout.Messages {
		switch msg.Role {
		case "user":
			app.ChatModel.AddUserMessage(msg.Content)
		case "assistant":
			app.ChatModel.AddAssistantMessage(msg.Content)
		case "system":
			app.ChatModel.AddSystemMessage(msg.Content)
		}
	}

	return nil
}

// Placeholder definition for logDebug if it doesn't exist
// Ensure you have a proper logging mechanism (e.g., writing to a file)
// For now, just print to stderr for visibility during execution.
func logDebug(format string, args ...interface{}) {
	// In a real app, use a proper logger (e.g., logrus, zap)
	// and write to a file configured via CLI flags or config file.
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
