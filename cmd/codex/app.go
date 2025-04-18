package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/config"
	"github.com/epuerta/codex-go/internal/fileops"
	"github.com/epuerta/codex-go/internal/functions"
	"github.com/epuerta/codex-go/internal/logging"
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
	Logger           logging.Logger

	// Rollout tracking
	CurrentRollout *AppRollout
	RolloutPath    string

	// We need width/height for layout
	width  int
	height int

	agentMsgChan      chan tea.Msg // Channel for agent messages
	isFirstAgentChunk bool         // Track if we are processing the first chunk of a stream
	isAgentProcessing bool         // Track if the agent is busy with a request/response cycle

	// State for Approval UI
	isAwaitingApproval  bool
	approvalModel       ui.ApprovalModel
	pendingFunctionCall *agent.FunctionCall // Store the function call needing approval
	pendingApprovalArgs string              // Store the specific args shown in the prompt
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
func NewApp(config *config.Config, logger logging.Logger) (*App, error) {
	logger.Log("Initializing App...")
	// Initialize the agent
	a, err := agent.NewOpenAIAgent(config, logger)
	if err != nil {
		logger.Log("Failed to initialize agent: %v", err)
		return nil, fmt.Errorf("failed to initialize agent: %w", err)
	}

	// Create chat model (no callback needed here)
	chatModel := ui.NewChatModel()

	// Set the logger
	chatModel.SetLogger(logger)

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
		Logger:           logger,
		agentMsgChan:     make(chan tea.Msg),
		// Initialize approval state
		isAwaitingApproval: false,
	}

	logger.Log("Repository context check: DisableProjectDoc=%t", config.DisableProjectDoc)
	// Initialize repository context if not disabled
	if !config.DisableProjectDoc {
		if err := app.initRepositoryContext(); err != nil {
			// Log the error but continue
			logger.Log("Warning: Failed to initialize repository context: %v", err)
		}
	}

	logger.Log("App initialized successfully.")
	return app, nil
}

// Init initializes the application model
func (app *App) Init() tea.Cmd {
	app.Logger.Log("App.Init called")
	// Start the dedicated channel listener command
	return tea.Batch(app.ChatModel.Init(), app.listenForAgentMessages())
}

// listenForAgentMessages returns a command that continuously listens on the
// agent message channel and sends received messages back to the App's Update loop.
func (app *App) listenForAgentMessages() tea.Cmd {
	return func() tea.Msg {
		msg := <-app.agentMsgChan // Block and wait for the next message
		app.Logger.Log("listenForAgentMessages: Received %T from channel, returning to Update.", msg)
		return msg
	}
}

// Update handles messages for the application model
func (app *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd // Use a slice for batching
	var agentMessageHandled bool = false
	var skipChatModelUpdate bool = false

	app.Logger.Log("App.Update received msg type: %T, isAwaitingApproval: %t", msg, app.isAwaitingApproval)

	// *** Approval UI Handling ***
	if app.isAwaitingApproval {
		switch approvalMsg := msg.(type) {
		case ui.ApprovalResultMsg:
			app.Logger.Log("Received ApprovalResultMsg: Approved=%t", approvalMsg.Approved)
			app.isAwaitingApproval = false // Exit approval mode

			app.ChatModel.SetThinkingStatus("Processing function result...")

			var agentOutput string
			var success bool
			functionName := app.pendingFunctionCall.Name
			handlerExecuted := false // Flag to prevent fallthrough

			if approvalMsg.Approved {
				app.Logger.Log("Approval granted for %s. Executing...", functionName)

				// *** Execute the approved function ***
				if functionName == "execute_command" {
					handlerExecuted = true // Mark as handled
					cmdStr := app.pendingApprovalArgs
					app.Logger.Log("Executing approved command via sandbox: %s", cmdStr)
					result, err := app.Sandbox.Execute(context.Background(), sandbox.SandboxOptions{Command: cmdStr, WorkingDir: app.Config.CWD, Timeout: 30 * time.Second})
					uiResult := &ui.CommandResult{Command: cmdStr, Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, Duration: result.Duration, Error: err}
					app.ChatModel.AddCommandMessage(cmdStr, uiResult)
					app.ChatModel.ForceUpdateViewport()
					agentOutput = result.Stdout
					success = err == nil && result.ExitCode == 0
					if !success {
						if err != nil {
							agentOutput = fmt.Sprintf("Execution Error: %v", err)
						} else {
							agentOutput = fmt.Sprintf("Command Failed (code %d): %s", result.ExitCode, result.Stderr)
						}
					}
					app.Logger.Log("Executed command. Agent output: %s, Success: %t", agentOutput, success)

				} else if functionName == "patch_file" {
					handlerExecuted = true // Mark as handled
					patchContent := app.pendingApprovalArgs
					app.Logger.Log("Executing approved patch. Content length: %d", len(patchContent))
					app.ChatModel.SetThinkingStatus("Applying patch...")
					app.Logger.Log("Calling fileops.ParseCustomPatch...")
					operations, parseErr := fileops.ParseCustomPatch(patchContent)
					if parseErr != nil {
						app.Logger.Log("ERROR: Failed to parse custom patch: %v", parseErr)
						agentOutput = fmt.Sprintf("Error parsing patch: %v", parseErr)
						success = false
						patchResMsg := &fileops.CustomPatchResult{Success: false, Error: parseErr, Operation: "parse_patch"}
						app.Logger.Log("Adding patch parse error message to UI: %+v", *patchResMsg)
						app.ChatModel.AddPatchResultMessage(patchResMsg)
						app.Logger.Log("Calling ForceUpdateViewport after adding parse error.")
						app.ChatModel.ForceUpdateViewport()
						app.Logger.Log("ForceUpdateViewport completed after adding parse error.")
					} else {
						app.Logger.Log("Parsed %d operations from patch. Calling fileops.ApplyCustomPatch...", len(operations))
						applyResults, applyErr := fileops.ApplyCustomPatch(operations)
						app.Logger.Log("ApplyCustomPatch finished. Results count: %d, Overall error: %v", len(applyResults), applyErr)

						successCount, failureCount := 0, 0
						// Log chat messages state before adding results (Removed GetMessageCount call)
						app.Logger.Log("Adding patch results to UI...")
						for i, res := range applyResults {
							app.Logger.Log("Processing applyResult %d: Success=%t, Path=%s, Op=%s, Error=%v", i+1, res.Success, res.Path, res.Operation, res.Error)
							if res.Success {
								successCount++
							} else {
								failureCount++
							}
							// Log before adding
							app.Logger.Log("Adding patch result message %d to UI: %+v", i+1, *res)
							app.ChatModel.AddPatchResultMessage(res)
							// Log before forcing update
							app.Logger.Log("Calling ForceUpdateViewport after adding result %d.", i+1)
							app.ChatModel.ForceUpdateViewport()
							app.Logger.Log("ForceUpdateViewport completed for result %d.", i+1)
						}
						app.Logger.Log("Finished adding %d patch result messages to ChatModel.", len(applyResults))
						// Log chat messages state after adding results (Removed GetMessageCount call)
						app.Logger.Log("Finished processing patch apply results.")

						if applyErr != nil {
							agentOutput = fmt.Sprintf("Patch application finished with errors. Succeeded: %d, Failed: %d. First error: %v", successCount, failureCount, applyErr)
							success = false
						} else if failureCount > 0 {
							agentOutput = fmt.Sprintf("Patch application finished. Succeeded: %d, Failed: %d.", successCount, failureCount)
							success = false
						} else {
							agentOutput = fmt.Sprintf("Patch application finished successfully. Operations applied: %d.", successCount)
							success = true
						}
						app.Logger.Log("Patch application summary for agent: %s", agentOutput)
					}
				}

				// *** Generic Handler for other approved functions (if not handled above) ***
				if !handlerExecuted {
					fn := app.FunctionRegistry.Get(functionName)
					if fn != nil {
						app.Logger.Log("Executing approved registered function: %s", functionName)
						app.ChatModel.SetThinkingStatus(fmt.Sprintf("Executing: %s", functionName))
						result, err := fn(app.pendingFunctionCall.Arguments)
						app.Logger.Log("Approved Function '%s' execution result: ResultLen=%d, Error=%v", functionName, len(result), err)
						success = err == nil
						agentOutput = result
						if err != nil {
							agentOutput = fmt.Sprintf("Error: %v", err)
							app.ChatModel.AddSystemMessage(agentOutput)
						}
						app.ChatModel.AddFunctionResultMessage(agentOutput, !success)
						app.ChatModel.ForceUpdateViewport()
					} else {
						app.Logger.Log("ERROR: Approved function %s not found in registry!", functionName)
						agentOutput = fmt.Sprintf("Internal error: approved function %s not found", functionName)
						success = false
					}
				}
			} else { // Denied
				agentOutput = fmt.Sprintf("Operation '%s' denied by user.", functionName)
				success = false
				app.Logger.Log("Approval denied for %s.", functionName)
				app.ChatModel.AddSystemMessage(agentOutput)
				app.ChatModel.ForceUpdateViewport()
			}

			// --- Send result back to agent ---
			resultMsg := sendFunctionResultMsg{
				ctx:          context.Background(),
				functionName: app.pendingFunctionCall.Name,
				callID:       app.pendingFunctionCall.ID,
				originalArgs: app.pendingFunctionCall.Arguments,
				output:       agentOutput,
				success:      success,
			}
			app.Logger.Log("App.Update (ApprovalResultMsg): Starting goroutine to send sendFunctionResultMsg for %s.", resultMsg.functionName)
			go func() {
				time.Sleep(50 * time.Millisecond)
				app.agentMsgChan <- resultMsg
			}()
			app.pendingFunctionCall = nil
			app.pendingApprovalArgs = ""

			skipChatModelUpdate = true

		case tea.KeyMsg, tea.MouseMsg: // Pass other messages to approval model
			app.Logger.Log("Passing msg %T to ApprovalModel", msg)
			var updatedApprovalModel ui.ApprovalModel
			updatedApprovalModel, cmd = app.approvalModel.Update(msg)
			app.approvalModel = updatedApprovalModel
			cmds = append(cmds, cmd)
			skipChatModelUpdate = true

		default:
			app.Logger.Log("Ignoring msg %T while awaiting approval", msg)
			skipChatModelUpdate = true
		}
		// Return early to avoid processing other cases while approval is active
		return app, tea.Batch(cmds...)
	}
	// *** End Approval UI Handling ***

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		app.Logger.Log("Received WindowSizeMsg: Width=%d, Height=%d", msg.Width, msg.Height)
		app.width = msg.Width
		app.height = msg.Height
		// Pass size to approval model if active
		if app.isAwaitingApproval {
			app.approvalModel.SetSize(msg.Width, msg.Height)
		}

	case tea.KeyMsg:
		app.Logger.Log("Received KeyMsg: Type=%v, Rune=%q, Alt=%t", msg.Type, msg.Runes, msg.Alt)
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc || (msg.String() == "q" && app.ChatModel.InputIsEmpty()) {
			app.Logger.Log("Quit key detected. Shutting down.")
			app.Agent.Cancel() // Cancel any pending agent work
			app.IsRunning = false
			return app, tea.Quit
		}

	case ui.UserInputSubmitMsg:
		if strings.HasPrefix(msg.Content, "/") {
			command := strings.TrimSpace(msg.Content)
			if command == "/clear" {
				app.Logger.Log("User command: /clear")
				app.Agent.ClearHistory()
				app.ChatModel.ClearMessages()
				app.ChatModel.AddSystemMessage("Chat history cleared.")
				skipChatModelUpdate = true
				cmd = nil
			} else if command == "/help" {
				app.Logger.Log("User command: /help")
				helpText := `Codex-Go Help:
  /clear : Clears the current conversation history.
  /help  : Shows this help message.
  Ctrl+C : Quits the application.
  Enter  : Sends your message to the assistant.`
				app.ChatModel.AddSystemMessage(helpText)
				skipChatModelUpdate = true
				cmd = nil
			} else {
				app.Logger.Log("User command: Unknown command: %s", command)
				app.ChatModel.AddSystemMessage(fmt.Sprintf("Unknown command: %s", command))
				skipChatModelUpdate = true
				cmd = nil
			}
		} else {
			if app.isAgentProcessing {
				app.Logger.Log("WARN: User submitted input while agent is processing. Ignoring.")
				skipChatModelUpdate = true
				cmd = nil
			} else {
				app.Logger.Log("User submitted input. Starting agent stream: %q", msg.Content)
				app.ChatModel.AddUserMessage(msg.Content)
				app.ChatModel.StartThinking()
				app.isFirstAgentChunk = true
				app.isAgentProcessing = true
				cmd = app.listenAgentStreamCmd(msg.Content)
				skipChatModelUpdate = true
			}
		}

	case agentResponseMsg:
		app.Logger.Log("Received agentResponseMsg")
		app.handleAgentResponseItem(msg.item)
		// Refocus input *unless* we are now awaiting approval
		if !app.isAwaitingApproval {
			cmds = append(cmds, textinput.Blink)
		}
		cmds = append(cmds, app.listenForAgentMessages()) // Ensure we continue listening
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentErrorMsg:
		app.Logger.Log("ERROR: Received agentErrorMsg: %v", msg.err)
		app.ChatModel.AddSystemMessage(fmt.Sprintf("Error: %v", msg.err))
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false
		cmds = append(cmds, app.listenForAgentMessages(), textinput.Blink)
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentStreamCompleteMsg:
		app.Logger.Log("Received agentStreamCompleteMsg (no tool calls)")
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false
		cmds = append(cmds, app.listenForAgentMessages(), textinput.Blink)
		agentMessageHandled = true
		skipChatModelUpdate = true

	case agentFollowUpCompleteMsg:
		app.Logger.Log("Received agentFollowUpCompleteMsg")
		app.ChatModel.StopThinking()
		app.isFirstAgentChunk = false
		app.isAgentProcessing = false
		cmds = append(cmds, app.listenForAgentMessages(), textinput.Blink)
		agentMessageHandled = true
		skipChatModelUpdate = true

	case sendFunctionResultMsg:
		app.Logger.Log("Received sendFunctionResultMsg for %s", msg.functionName)
		app.sendFunctionResultCmd(msg)
		cmds = append(cmds, app.listenForAgentMessages())
		agentMessageHandled = true
		skipChatModelUpdate = true

	}

	if !skipChatModelUpdate {
		app.Logger.Log("Passing message %T to ChatModel.Update", msg)
		var updatedChatModel tea.Model
		updatedChatModel, cmd = app.ChatModel.Update(msg)
		if updatedChatModelTyped, ok := updatedChatModel.(ui.ChatModel); ok {
			app.ChatModel = updatedChatModelTyped
		}
		cmds = append(cmds, cmd)
	} else if !agentMessageHandled && len(cmds) == 0 {
		// If we skipped chat model update AND no other command was generated AND it wasn't an agent message we handled,
		// we still need to re-listen for agent messages.
		app.Logger.Log("Skipped ChatModel.Update for %T, no other command/agent message. Ensuring agent listener continues.", msg)
		cmds = append(cmds, app.listenForAgentMessages())
	}

	if agentMessageHandled {
		app.Logger.Log("Agent message handled, forcing viewport update.")
		app.ChatModel.ForceUpdateViewport()
	}

	if len(cmds) > 0 {
		batchCmd := tea.Batch(cmds...)
		app.Logger.Log("App.Update returning %d command(s).", len(cmds))
		return app, batchCmd
	}

	app.Logger.Log("App.Update finished for %T, returning nil command", msg)
	// Ensure we always return a command to listen for the next agent message if nothing else is pending
	return app, app.listenForAgentMessages()
}

// View renders the application UI
func (app *App) View() string {
	if app.isAwaitingApproval {
		// Ensure the approval model has the correct size based on current terminal dimensions
		app.approvalModel.SetSize(app.width, app.height)
		// Render the approval UI (it handles its own centering via lipgloss.Place)
		approvalView := app.approvalModel.View()
		return approvalView
	} else {
		return app.ChatModel.View()
	}
}

// listenAgentStreamCmd starts the agent stream goroutine which sends messages to app.agentMsgChan
func (app *App) listenAgentStreamCmd(content string) tea.Cmd {
	app.Logger.Log("listenAgentStreamCmd: Starting agent stream goroutine for content: %q", content)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		message := agent.Message{Role: "user", Content: content}

		app.Logger.Log("listenAgentStreamCmd: Goroutine started. Calling Agent.SendMessage...")
		streamEndedWithTools, err := app.Agent.SendMessage(ctx, []agent.Message{message}, func(itemJSON string) {
			app.Logger.Log("listenAgentStreamCmd Handler: Received JSON string: %q", itemJSON)

			var item agent.ResponseItem
			err := json.Unmarshal([]byte(itemJSON), &item)
			if err != nil {
				app.Logger.Log("ERROR: listenAgentStreamCmd Handler: Failed to unmarshal ResponseItem JSON: %v. JSON: %s", err, itemJSON)
				app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("failed to unmarshal agent response: %w", err)}
				return
			}

			switch item.Type {
			case "message", "function_call":
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
				app.Logger.Log("listenAgentStreamCmd Handler: Sending agentResponseMsg to channel (Type: %s).", item.Type)
				app.agentMsgChan <- agentResponseMsg{item: itemToSend}
			case "followup_complete":
				app.Logger.Log("listenAgentStreamCmd Handler: Sending agentFollowUpCompleteMsg to channel.")
				app.agentMsgChan <- agentFollowUpCompleteMsg{}
			default:
				app.Logger.Log("WARN: listenAgentStreamCmd Handler: Received unknown item type '%s'. Ignoring.", item.Type)
			}
		})
		app.Logger.Log("listenAgentStreamCmd: Goroutine finished Agent.SendMessage call. Error: %v, EndedWithTools: %t", err, streamEndedWithTools)

		if err != nil {
			app.Logger.Log("listenAgentStreamCmd: Goroutine sending agentErrorMsg to channel.")
			app.agentMsgChan <- agentErrorMsg{err: err}
		} else if !streamEndedWithTools {
			app.Logger.Log("listenAgentStreamCmd: Goroutine finished normally and without tool calls. Sending agentStreamCompleteMsg.")
			app.agentMsgChan <- agentStreamCompleteMsg{}
		} else {
			app.Logger.Log("listenAgentStreamCmd: Goroutine finished normally, ended with tool calls. NOT sending agentStreamCompleteMsg.")
		}
	}()

	app.Logger.Log("listenAgentStreamCmd: Returning nil command.")
	return nil
}

// handleAgentResponseItem processes a single response item from the agent
func (app *App) handleAgentResponseItem(item agent.ResponseItem) {
	app.Logger.Log("App.handleAgentResponseItem received item type: %s", item.Type)

	switch item.Type {
	case "message":
		app.Logger.Log("Handling 'message' item.")
		if item.Message != nil {
			app.Logger.Log("Message item content length: %d", len(item.Message.Content))
			app.Logger.Log("isFirstAgentChunk state *before* processing message: %t", app.isFirstAgentChunk)
			app.ChatModel.SetThinkingStatus(fmt.Sprintf("Receiving message chunk..."))
			content := item.Message.Content

			if app.isFirstAgentChunk {
				app.Logger.Log("isFirstAgentChunk=true, adding new assistant message.")
				app.ChatModel.AddAssistantMessage(content)
				app.isFirstAgentChunk = false
				app.Logger.Log("Called AddAssistantMessage, set isFirstAgentChunk=false")
			} else {
				app.Logger.Log("isFirstAgentChunk=false, updating last assistant message.")
				app.ChatModel.UpdateLastAssistantMessage(content)
				app.Logger.Log("Called UpdateLastAssistantMessage")
			}

			app.ChatModel.ForceUpdateViewport()
			app.Logger.Log("Called ForceUpdateViewport in message handler.")
		} else {
			app.Logger.Log("WARN: Handling 'message' item, but item.Message is nil.")
		}
		app.Logger.Log("App.handleAgentResponseItem finished processing message.")

	case "function_call":
		if item.FunctionCall != nil {
			app.Logger.Log("Handling 'function_call' item. Name: %s, ID: %s, Full Args JSON: %s", item.FunctionCall.Name, item.FunctionCall.ID, item.FunctionCall.Arguments)
			app.ChatModel.SetThinkingStatus(fmt.Sprintf("Evaluating %s...", item.FunctionCall.Name))
			app.ChatModel.AddFunctionCallMessage(item.FunctionCall.Name, item.FunctionCall.Arguments)
			app.ChatModel.ForceUpdateViewport()

			// --- Decide if Approval Needed --- Moved earlier
			needsApproval := app.needsApprovalForFunction(item.FunctionCall.Name)
			var argsForApproval string
			if needsApproval {
				if item.FunctionCall.Name == "execute_command" || item.FunctionCall.Name == "patch_file" || item.FunctionCall.Name == "write_file" {
					var argsMap map[string]interface{}
					if err := json.Unmarshal([]byte(item.FunctionCall.Arguments), &argsMap); err == nil {
						if cmd, ok := argsMap["command"].(string); ok {
							argsForApproval = cmd
						} else if patch, ok := argsMap["code_edit"].(string); ok {
							argsForApproval = patch
						} else if patch, ok := argsMap["patch_content"].(string); ok {
							argsForApproval = patch
						} else if content, ok := argsMap["content"].(string); ok {
							argsForApproval = content
						} else {
							argsForApproval = item.FunctionCall.Arguments
						}
					} else {
						argsForApproval = item.FunctionCall.Arguments
					}
				} else {
					argsForApproval = item.FunctionCall.Arguments
				}
			}
			app.Logger.Log("Determined argsForApproval: %s", argsForApproval)

			if needsApproval {
				app.Logger.Log("Function %s requires approval. Triggering approval state.", item.FunctionCall.Name)
				app.askForApproval(item.FunctionCall.Name, argsForApproval, item.FunctionCall)
				// Stop processing here, wait for ApprovalResultMsg
				return
			}

			// --- Execute Function Directly (No Approval Needed) ---
			app.Logger.Log("Function %s does not require approval. Executing directly.", item.FunctionCall.Name)
			app.ChatModel.SetThinkingStatus(fmt.Sprintf("Executing: %s...", item.FunctionCall.Name))
			var agentOutput string
			var success bool

			if item.FunctionCall.Name == "execute_command" {
				var args map[string]interface{}
				cmdStr := ""
				if err := json.Unmarshal([]byte(item.FunctionCall.Arguments), &args); err != nil {
					agentOutput = fmt.Sprintf("Error parsing command args: %v", err)
					success = false
					app.ChatModel.AddSystemMessage(agentOutput)
				} else {
					var ok bool
					cmdStr, ok = args["command"].(string)
					if !ok || cmdStr == "" {
						agentOutput = "Missing command argument for execute_command"
						success = false
						app.ChatModel.AddSystemMessage(agentOutput)
					} else {
						result, err := app.Sandbox.Execute(context.Background(), sandbox.SandboxOptions{
							Command:    cmdStr,
							WorkingDir: app.Config.CWD,
							Timeout:    30 * time.Second,
						})
						uiResult := &ui.CommandResult{Command: cmdStr, Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, Duration: result.Duration, Error: err}
						app.ChatModel.AddCommandMessage(cmdStr, uiResult)
						agentOutput = result.Stdout
						success = err == nil && result.ExitCode == 0
						if !success { /* Set error output */
						}
					}
				}
			} else if item.FunctionCall.Name == "patch_file" {
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(item.FunctionCall.Arguments), &args); err != nil {
					agentOutput = fmt.Sprintf("Error parsing patch_file args: %v", err)
					success = false
					app.ChatModel.AddSystemMessage(agentOutput)
				} else {
					patchContent := ""
					if pc, ok := args["code_edit"].(string); ok {
						patchContent = pc
					} else if pc, ok := args["patch_content"].(string); ok {
						patchContent = pc
					}
					if patchContent == "" {
						agentOutput = "Missing patch_content argument for patch_file"
						success = false
						app.ChatModel.AddSystemMessage(agentOutput)
					} else {
						operations, parseErr := fileops.ParseCustomPatch(patchContent)
						if parseErr != nil {
							agentOutput = fmt.Sprintf("Error parsing patch: %v", parseErr)
							success = false
							app.ChatModel.AddPatchResultMessage(&fileops.CustomPatchResult{Success: false, Error: parseErr, Operation: "parse_patch"})
						} else {
							applyResults, applyErr := fileops.ApplyCustomPatch(operations)
							successCount, failureCount := 0, 0
							for _, res := range applyResults {
								if res.Success {
									successCount++
								} else {
									failureCount++
								}
								app.ChatModel.AddPatchResultMessage(res)
								app.ChatModel.ForceUpdateViewport()
							}
							if applyErr != nil {
								agentOutput = fmt.Sprintf("Patch application finished with errors. Succeeded: %d, Failed: %d. First error: %v", successCount, failureCount, applyErr)
								success = false
							} else if failureCount > 0 {
								agentOutput = fmt.Sprintf("Patch application finished. Succeeded: %d, Failed: %d.", successCount, failureCount)
								success = false
							} else {
								agentOutput = fmt.Sprintf("Patch application finished successfully. Operations applied: %d.", successCount)
								success = true
							}
						}
					}
				}
			} else { // Generic function from registry
				fn := app.FunctionRegistry.Get(item.FunctionCall.Name)
				if fn == nil { /* Handle unknown function */
					agentOutput = fmt.Sprintf("Unknown function: %s", item.FunctionCall.Name)
					success = false
					app.ChatModel.AddSystemMessage(agentOutput)
				} else {
					result, err := fn(item.FunctionCall.Arguments)
					success = err == nil
					agentOutput = result
					if err != nil { /* Set agentOutput, add system message */
						agentOutput = fmt.Sprintf("Error: %v", err)
						app.ChatModel.AddSystemMessage(agentOutput)
					}
					app.ChatModel.AddFunctionResultMessage(agentOutput, !success)
				}
			}

			// --- Send result back to agent --- (Only if approval wasn't needed)
			resultMsg := sendFunctionResultMsg{
				ctx:          context.Background(),
				functionName: item.FunctionCall.Name,
				callID:       item.FunctionCall.ID,
				originalArgs: item.FunctionCall.Arguments,
				output:       agentOutput,
				success:      success,
			}

			app.Logger.Log("App.handleAgentResponseItem (Direct Execute): Starting goroutine to send sendFunctionResultMsg for %s.", resultMsg.functionName)
			go func() {
				time.Sleep(50 * time.Millisecond)
				app.agentMsgChan <- resultMsg
				app.Logger.Log("App.handleAgentResponseItem (Direct Execute): Goroutine finished sending sendFunctionResultMsg.")
			}()

		} else {
			app.Logger.Log("WARN: Handling 'function_call' item, but item.FunctionCall is nil.")
		}

	default:
		app.Logger.Log("WARN: App.handleAgentResponseItem received unhandled item type: %s.", item.Type)
	}
}

// sendFunctionResultCmd processes the function result and sends it back to the agent
func (app *App) sendFunctionResultCmd(msg sendFunctionResultMsg) {
	app.Logger.Log("sendFunctionResultCmd: Preparing to send result for %s (callID: %s), success=%t", msg.functionName, msg.callID, msg.success)
	if app.Agent != nil {
		go func() {
			app.Logger.Log("sendFunctionResultCmd Goroutine: Calling Agent.SendFunctionResult for %s...", msg.functionName)
			err := app.Agent.SendFunctionResult(msg.ctx, msg.callID, msg.functionName, msg.output, msg.success)
			app.Logger.Log("sendFunctionResultCmd Goroutine: Agent.SendFunctionResult returned error: %v", err)
			if err != nil {
				app.Logger.Log("ERROR: sendFunctionResultCmd Goroutine: Sending agentErrorMsg due to SendFunctionResult failure: %v", err)
				app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("failed to send function result for %s: %w", msg.functionName, err)}
			} else {
				app.Logger.Log("sendFunctionResultCmd Goroutine: Agent.SendFunctionResult success. Handler will send next messages.")
			}
		}()

		app.isFirstAgentChunk = true
		app.Logger.Log("sendFunctionResultCmd: Reset isFirstAgentChunk=true")

		app.ChatModel.AddSystemMessage("Function complete - waiting for assistant response...")
		app.ChatModel.ForceUpdateViewport()

		app.ChatModel.SetThinkingStatus("Function executed, waiting for assistant response...")

		app.Logger.Log("sendFunctionResultCmd: Finished initiating send.")
	} else {
		app.Logger.Log("ERROR: sendFunctionResultCmd: Agent is nil!")
		app.agentMsgChan <- agentErrorMsg{err: fmt.Errorf("agent is nil, cannot send function result")}
	}
}

// needsApprovalForFunction determines if a function needs approval based on the current mode
func (app *App) needsApprovalForFunction(functionName string) bool {
	// Logging the check
	app.Logger.Log("Checking approval for function '%s' with mode '%s'", functionName, app.Config.ApprovalMode)

	switch app.Config.ApprovalMode {
	case config.Suggest:
		needs := functionName != "read_file" && functionName != "list_directory"
		app.Logger.Log("Suggest Mode: Needs approval = %t", needs)
		return needs
	case config.AutoEdit:
		needs := functionName == "execute_command"
		app.Logger.Log("AutoEdit Mode: Needs approval = %t", needs)
		return needs
	case config.FullAuto:
		app.Logger.Log("FullAuto Mode: Needs approval = false")
		return false
	case config.DangerousAutoApprove:
		app.Logger.Log("Dangerous Mode: Needs approval = false")
		return false
	default:
		app.Logger.Log("WARN: Unknown approval mode '%s', defaulting to 'suggest' behavior.", app.Config.ApprovalMode)
		return functionName != "read_file" && functionName != "list_directory"
	}
}

// askForApproval sets the state to show the approval UI instead of blocking
func (app *App) askForApproval(functionName, argsToDisplay string, originalCall *agent.FunctionCall) {
	app.Logger.Log("Setting state to ask for approval: Function=%s, Args=%q", functionName, argsToDisplay)
	var title, description string
	switch functionName {
	case "write_file":
		title = "Approve File Write"
		description = "The assistant wants to write to a file on your filesystem:"
	case "patch_file":
		title = "Approve File Patch"
		description = "The assistant wants to modify a file using the following patch:"
	case "execute_command":
		title = "Approve Command Execution"
		description = "The assistant wants to execute the following shell command:"
	default:
		title = "Approve Operation"
		description = fmt.Sprintf("The assistant wants to perform the '%s' operation with arguments:", functionName)
	}

	app.approvalModel = ui.NewApprovalModel(title, description, argsToDisplay)
	app.isAwaitingApproval = true
	app.pendingFunctionCall = originalCall  // Store the original call details
	app.pendingApprovalArgs = argsToDisplay // Store the args shown to the user

	// Update UI immediately to show the prompt
	app.ChatModel.SetThinkingStatus(fmt.Sprintf("Awaiting approval for %s...", functionName))
	// No need to add system message here, the approval UI itself is the message
	app.ChatModel.ForceUpdateViewport() // Force update might be needed if View logic depends on state

	// We don't return anything or block here
	app.Logger.Log("Approval state set. Waiting for ui.ApprovalResultMsg.")
}

// initRepositoryContext loads project-specific context from codex.md files
func (app *App) initRepositoryContext() error {
	app.Logger.Log("Initializing repository context...")
	repoContext, err := app.loadRepositoryContext()
	if err != nil {
		app.Logger.Log("Error loading repository context: %v", err)
		return err
	}

	if repoContext == "" {
		app.Logger.Log("No repository context found (codex.md files). Skipping.")
		return nil
	}

	app.Logger.Log("Found repository context. Adding as system message.")
	ctx := context.Background()
	systemMsg := agent.Message{
		Role:    "system",
		Content: "Repository Context:\n" + repoContext,
	}

	app.Logger.Log("Sending repository context to agent history...")
	_, err = app.Agent.SendMessage(ctx, []agent.Message{systemMsg}, func(itemJSON string) {
		app.Logger.Log("Repository context SendMessage handler received item (should be empty): %s", itemJSON)
	})
	if err != nil {
		app.Logger.Log("Error sending repository context to agent: %v", err)
	}
	return err
}

// loadRepositoryContext looks for and loads codex.md files
func (app *App) loadRepositoryContext() (string, error) {
	var contextParts []string

	if app.Config.ProjectDocPath != "" {
		app.Logger.Log("Loading project doc from specified path: %s", app.Config.ProjectDocPath)
		data, err := os.ReadFile(app.Config.ProjectDocPath)
		if err != nil {
			return "", fmt.Errorf("failed to read project doc from path %s: %w", app.Config.ProjectDocPath, err)
		}
		contextParts = append(contextParts, string(data))
	}

	cwd := app.Config.CWD
	repoRoot, err := findRepositoryRoot(cwd)
	if err == nil {
		app.Logger.Log("Found repository root: %s", repoRoot)
		if repoRoot != cwd {
			repoRootDocPath := filepath.Join(repoRoot, "codex.md")
			if _, err := os.Stat(repoRootDocPath); err == nil {
				app.Logger.Log("Found codex.md in repository root: %s", repoRootDocPath)
				data, err := os.ReadFile(repoRootDocPath)
				if err == nil {
					contextParts = append(contextParts, fmt.Sprintf("Repository Root codex.md:\n%s", string(data)))
				}
			}
		}
	} else {
		app.Logger.Log("Could not find repository root starting from %s: %v", cwd, err)
	}

	cwdDocPath := filepath.Join(cwd, "codex.md")
	if _, err := os.Stat(cwdDocPath); err == nil {
		app.Logger.Log("Found codex.md in current directory: %s", cwdDocPath)
		data, err := os.ReadFile(cwdDocPath)
		if err == nil {
			contextParts = append(contextParts, fmt.Sprintf("Current Directory codex.md:\n%s", string(data)))
		}
	}

	combinedContext := strings.Join(contextParts, "\n\n---\n\n")
	app.Logger.Log("Combined repository context length: %d bytes", len(combinedContext))
	return combinedContext, nil
}

// findRepositoryRoot walks up the directory tree to find the repository root
func findRepositoryRoot(startDir string) (string, error) {
	currentDir := startDir
	for {
		gitDir := filepath.Join(currentDir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
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

	app.CurrentRollout.UpdatedAt = time.Now()

	history := app.Agent.GetHistory()
	if history != nil {
		app.CurrentRollout.Messages = history.GetMessages()
	}

	if app.RolloutPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		homeDir, err := os.UserHomeDir()
		if err != nil {
			app.Logger.Log("Error getting home directory for rollout path: %v", err)
			return fmt.Errorf("failed to get home directory: %w", err)
		}

		rolloutsDir := filepath.Join(homeDir, ".codex", "rollouts")
		if err := os.MkdirAll(rolloutsDir, 0755); err != nil {
			app.Logger.Log("Error creating rollouts directory %s: %v", rolloutsDir, err)
			return fmt.Errorf("failed to create rollouts directory: %w", err)
		}
		app.RolloutPath = filepath.Join(rolloutsDir, fmt.Sprintf("codex-session-%s.json", timestamp))
	}

	app.Logger.Log("Saving rollout to: %s", app.RolloutPath)
	data, err := json.MarshalIndent(app.CurrentRollout, "", "  ")
	if err != nil {
		app.Logger.Log("Error marshaling rollout: %v", err)
		return fmt.Errorf("failed to marshal rollout: %w", err)
	}

	if err := os.WriteFile(app.RolloutPath, data, 0644); err != nil {
		app.Logger.Log("Error writing rollout file %s: %v", app.RolloutPath, err)
		return fmt.Errorf("failed to save rollout: %w", err)
	}

	app.Logger.Log("Rollout saved successfully.")
	return nil
}

// LoadRollout loads a saved session from a file
func (app *App) LoadRollout(path string) error {
	app.Logger.Log("Loading rollout from: %s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		app.Logger.Log("Error reading rollout file %s: %v", path, err)
		return fmt.Errorf("failed to read rollout file: %w", err)
	}

	var rollout AppRollout
	if err := json.Unmarshal(data, &rollout); err != nil {
		app.Logger.Log("Error unmarshaling rollout from %s: %v", path, err)
		return fmt.Errorf("failed to unmarshal rollout: %w", err)
	}

	app.CurrentRollout = &rollout
	app.RolloutPath = path
	app.Logger.Log("Rollout loaded successfully. SessionID: %s, CreatedAt: %s", rollout.SessionID, rollout.CreatedAt)

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
	app.Logger.Log("Loaded %d messages from rollout into ChatModel.", len(rollout.Messages))

	return nil
}

// Placeholder definition for logDebug if it doesn't exist
// Ensure you have a proper logging mechanism (e.g., writing to a file)
// For now, just print to stderr for visibility during execution.
func logDebug(format string, args ...interface{}) {
	// Check if the global logger is enabled before logging
	if appLogger != nil && appLogger.IsEnabled() {
		appLogger.Log(format, args...)
	}
	// No longer print to stderr by default
	// fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Close closes the application and cleans up all resources
func (app *App) Close() error {
	app.Logger.Log("App.Close: Cleaning up resources...")

	// Set the app as not running
	app.IsRunning = false

	// Cancel agent operations
	if app.Agent != nil {
		app.Logger.Log("App.Close: Cancelling agent...")
		app.Agent.Cancel()
		if err := app.Agent.Close(); err != nil {
			app.Logger.Log("App.Close: Error closing agent: %v", err)
			// Continue with cleanup despite errors
		}
	}

	// Ensure sandbox is closed if needed
	if closer, ok := app.Sandbox.(io.Closer); ok {
		app.Logger.Log("App.Close: Closing sandbox...")
		if err := closer.Close(); err != nil {
			app.Logger.Log("App.Close: Error closing sandbox: %v", err)
			// Continue with cleanup despite errors
		}
	}

	// Save current session state if needed
	app.Logger.Log("App.Close: Saving rollout...")
	if err := app.SaveRollout(); err != nil {
		app.Logger.Log("App.Close: Error saving rollout: %v", err)
		// Continue with cleanup despite errors
	}

	// Close the agent message channel to unblock any waiting goroutines
	if app.agentMsgChan != nil {
		app.Logger.Log("App.Close: Closing agent message channel...")
		close(app.agentMsgChan)
	}

	app.Logger.Log("App.Close: Cleanup complete")
	return nil
}
