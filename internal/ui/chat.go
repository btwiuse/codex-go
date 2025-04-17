package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/logging"
	"github.com/google/uuid"
)

// --- UI Messages ---

// UserInputSubmitMsg signals that the user pressed Enter in the chat input
type UserInputSubmitMsg struct {
	Content string
}

// --- End UI Messages ---

// Message styles
var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true).
			PaddingLeft(1)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true).
			PaddingLeft(1)

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true).
			PaddingLeft(1)

	functionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true).
			PaddingLeft(1)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			PaddingLeft(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true).
			PaddingLeft(1)

	inputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			PaddingLeft(1).
			PaddingRight(1)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Italic(true).
			PaddingLeft(2)

	thinkingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true).
			PaddingLeft(1)

	commandStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true).
			PaddingLeft(1)

	commandOutputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("5")).
				PaddingLeft(1)
)

// CommandResult represents the result of a command execution
type CommandResult struct {
	Command  string        `json:"command"` // Store the original command
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
	Error    error         `json:"-"` // Don't marshal error
}

// Message represents a chat message
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	ANSI      bool      `json:"ansi"` // Whether the content contains ANSI escape codes

	// For thinking state - Maybe remove if only using status bar?
	// IsThinking    bool      `json:"-"`
	// ThinkingStart time.Time `json:"-"`

	// For command execution - Store the result directly
	CommandResult *CommandResult `json:"command_result,omitempty"`
}

// SendMessageCmd is a tea.Cmd to signal sending a message
type SendMessageCmd struct {
	Content string // Exported field
}

// ChatModel is the BubbleTea model for the chat UI
type ChatModel struct {
	messages       []Message // Local messages (for messages not yet in history)
	viewport       viewport.Model
	textInput      CustomTextInput
	ready          bool
	width          int
	height         int
	agent          agent.Agent    // Reference to the agent for history access
	showTimestamps bool           // Whether to show timestamps
	hideSystemMsgs bool           // Whether to hide system messages
	lastResponseID string         // To track the last response for the live update
	logger         logging.Logger // Add logger field

	// Fields for thinking state
	isThinking    bool
	thinkingStart time.Time
	thinkingSub   chan time.Time // For thinking timer updates
	currentStatus string         // Current status message during thinking

	// Status bar info
	sessionID    string
	workDir      string
	model        string
	approvalMode string

	// Callbacks
	onSendMessage func(content string)
}

// NewChatModel creates a new chat model
func NewChatModel() ChatModel {
	ti := NewCustomTextInput()
	ti.SetPrefix("user")
	ti.SetPlaceholder("Send a message or press tab to select a suggestion")
	ti.Focus()

	return ChatModel{
		messages:       []Message{},
		textInput:      ti,
		onSendMessage:  nil,
		showTimestamps: false,
		hideSystemMsgs: true,
		sessionID:      fmt.Sprintf("%08x", uuid.New().ID()),
		workDir:        getWorkDir(),
		model:          "o4-mini",            // Default model
		approvalMode:   "suggest",            // Default approval mode
		logger:         &logging.NilLogger{}, // Default to nil logger
	}
}

// getWorkDir returns the current working directory
func getWorkDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "~"
	}

	// Try to convert to home directory relative path if possible
	home, err := os.UserHomeDir()
	if err == nil {
		if strings.HasPrefix(dir, home) {
			dir = "~" + dir[len(home):]
		}
	}

	return dir
}

// SetSessionInfo sets the session information for display in the status bar
func (m *ChatModel) SetSessionInfo(sessionID, workDir, model, approvalMode string) {
	if sessionID != "" {
		m.sessionID = sessionID
	}
	if workDir != "" {
		m.workDir = workDir
	}
	if model != "" {
		m.model = model
	}
	if approvalMode != "" {
		m.approvalMode = approvalMode
	}
}

// SetAgent sets the agent reference for history access
func (m *ChatModel) SetAgent(a agent.Agent) {
	m.agent = a
}

// SetOnSendMessage sets the callback for when a message is sent
func (m *ChatModel) SetOnSendMessage(callback func(content string)) {
	m.onSendMessage = callback
}

// SetLogger sets the logger for the model
func (m *ChatModel) SetLogger(logger logging.Logger) {
	if logger != nil {
		m.logger = logger
	} else {
		m.logger = &logging.NilLogger{}
	}
}

// Init initializes the model
func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tea.EnterAltScreen, m.thinkTick())
}

// AddMessage adds a message to the local messages (for messages not yet in history)
func (m *ChatModel) AddMessage(msg Message) {
	m.messages = append(m.messages, msg)

	// If viewport is ready, update it
	if m.ready {
		m.updateViewport()
	}
}

// AddUserMessage adds a user message to the local messages
func (m *ChatModel) AddUserMessage(content string) {
	m.AddMessage(Message{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// AddAssistantMessage adds an assistant message to the local messages
func (m *ChatModel) AddAssistantMessage(content string) {
	// Use logger instead of direct stderr output
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("AddAssistantMessage called with content length: %d", len(content))
	}

	m.AddMessage(Message{
		Role:      "assistant",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// AddSystemMessage adds a system message to the local messages
func (m *ChatModel) AddSystemMessage(content string) {
	m.AddMessage(Message{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// AddFunctionCallMessage adds a function call message to the local messages
func (m *ChatModel) AddFunctionCallMessage(name, args string) {
	// Use logger instead of direct stderr output
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("AddFunctionCallMessage called with name: %s", name)
	}

	m.AddMessage(Message{
		Role:      "function_call",
		Content:   fmt.Sprintf("Call: %s\nArgs: %s", name, args),
		Timestamp: time.Now(),
	})
}

// AddFunctionResultMessage adds a function result message to the local messages
func (m *ChatModel) AddFunctionResultMessage(result string, isError bool) {
	// Use logger instead of direct stderr output
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("AddFunctionResultMessage called with isError: %v", isError)
	}

	m.AddMessage(Message{
		Role:      "function_result",
		Content:   result,
		Timestamp: time.Now(),
		ANSI:      strings.Contains(result, "\x1b["), // Check for ANSI escape codes
	})
}

// UpdateLastAssistantMessage updates the content of the last assistant message
func (m *ChatModel) UpdateLastAssistantMessage(additionalContent string) {
	// Use logger instead of direct stderr output
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("UpdateLastAssistantMessage called with content length: %d", len(additionalContent))
	}

	// Find the last assistant message
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			// Append the new content
			// Assuming the full message content comes in each chunk for now
			// To append: m.messages[i].Content += additionalContent
			m.messages[i].Content = additionalContent
			if m.logger != nil && m.logger.IsEnabled() {
				m.logger.Log("Updated assistant message at index %d", i)
			}

			// Update the viewport if ready
			if m.ready {
				m.updateViewport()
			}
			return
		}
	}

	// If no assistant message found, create a new one
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("No existing assistant message found, creating new one")
	}
	m.AddAssistantMessage(additionalContent)
}

// ToggleTimestamps toggles the display of timestamps
func (m *ChatModel) ToggleTimestamps() {
	m.showTimestamps = !m.showTimestamps
	if m.ready {
		m.updateViewport()
	}
}

// ToggleSystemMessages toggles the display of system messages
func (m *ChatModel) ToggleSystemMessages() {
	m.hideSystemMsgs = !m.hideSystemMsgs
	if m.ready {
		m.updateViewport()
	}
}

// ClearHistory clears the conversation history
func (m *ChatModel) ClearHistory() {
	if m.agent != nil {
		m.agent.ClearHistory()
	}
	m.messages = []Message{}
	if m.ready {
		m.updateViewport()
	}
}

// updateViewport updates the viewport content with messages from the local messages slice
func (m *ChatModel) updateViewport() {
	var sb strings.Builder

	// --- REMOVED History Merging Logic ---
	// We will now only render messages explicitly added to m.messages by the App
	var allMessages []Message

	// Build the list of messages to display ONLY from local m.messages
	for _, msg := range m.messages {
		// Skip system messages if hidden OR any message containing DEBUG:
		if (m.hideSystemMsgs && msg.Role == "system") ||
			strings.Contains(msg.Content, "DEBUG:") {
			continue
		}
		allMessages = append(allMessages, msg)
	}

	// --- NEW: Filter out function call/result messages if a subsequent assistant message exists ---
	filteredMessages := []Message{}
	assistantResponseFound := false
	// Iterate backwards to easily find the last assistant message
	for i := len(allMessages) - 1; i >= 0; i-- {
		if allMessages[i].Role == "assistant" {
			assistantResponseFound = true
			break // Stop searching once the latest assistant message is found
		}
	}

	if assistantResponseFound {
		// If an assistant response exists, filter out preceding function messages
		for _, msg := range allMessages {
			if msg.Role != "function_call" && msg.Role != "function_result" {
				filteredMessages = append(filteredMessages, msg)
			}
		}
	} else {
		// If no assistant response found yet (e.g., during the function call), keep all messages
		filteredMessages = allMessages
	}
	// --- End Filtering ---

	// Render the filtered messages with a separator between them
	for i, msg := range filteredMessages { // Use filteredMessages now
		// Add a separator line between messages
		if i > 0 {
			separatorStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Width(m.width - 4)

			separator := separatorStyle.Render("───────────────────")
			sb.WriteString(separator)
			sb.WriteString("\n\n")
		}

		formattedMsg := formatMessage(msg, m.width-2, m.showTimestamps)
		sb.WriteString(formattedMsg)
		sb.WriteString("\n\n")
	}

	finalContent := sb.String()

	// Set the viewport content
	m.viewport.SetContent(finalContent)

	// Safety check - only scroll to bottom if there's content and viewport is properly sized
	if len(finalContent) > 0 && m.viewport.Height > 0 {
		// Scroll to the bottom
		m.viewport.GotoBottom()
	}
}

// formatMessage formats a single message for display
func formatMessage(msg Message, width int, showTimestamp bool) string {
	var prefix string
	var style lipgloss.Style
	var renderedContent string
	var finalRendered string

	switch msg.Role {
	case "user":
		prefix = "user"
		style = userStyle.Copy().Bold(true) // Make user messages bold

		// Create a user message style with different border
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("5")). // Purple for user
			Padding(0, 1).
			Width(width - 4)

		renderedContent = wordWrap(msg.Content, width-len(prefix)-6) // Account for border and padding

		// Combine prefix and content
		prefixedContent := style.Render(prefix) + " " + renderedContent

		// Apply border
		finalRendered = borderStyle.Render(prefixedContent)
		return finalRendered

	case "assistant":
		prefix = "codex"
		style = assistantStyle.Copy().Bold(true)                     // Make assistant messages bold
		renderedContent = wordWrap(msg.Content, width-len(prefix)-6) // Account for border and padding
	case "system":
		prefix = "system"
		style = systemStyle
		renderedContent = wordWrap(msg.Content, width-len(prefix)-2)
	case "thinking":
		// Special styling for thinking messages
		prefix = "thinking"
		style = thinkingStyle.Copy().Background(lipgloss.Color("8")).Bold(true)
		renderedContent = "Assistant is processing your request..."
	// Refactored Command Formatting
	case "command":
		// Render the command itself
		cmdPrefix := "command"
		cmdStyle := commandStyle // Use existing style for prefix
		cmdLine := "$ " + msg.Content
		formattedCmd := cmdStyle.Render(cmdPrefix) + " " + cmdLine

		// Render the result if available
		formattedResult := ""
		if msg.CommandResult != nil {
			resultPrefix := ""
			resultStyle := commandOutputStyle // Use existing style
			resultOutput := ""

			if msg.CommandResult.ExitCode == 0 {
				resultPrefix = "command.stdout"
				resultOutput = msg.CommandResult.Stdout
			} else {
				resultPrefix = "command.stderr"
				resultOutput = msg.CommandResult.Stderr
				if resultOutput == "" && msg.CommandResult.Error != nil {
					resultOutput = msg.CommandResult.Error.Error()
				}
			}

			// Add metadata
			metadata := fmt.Sprintf("(code: %d, duration: %s)",
				msg.CommandResult.ExitCode,
				msg.CommandResult.Duration.Round(time.Millisecond)) // More precision for duration

			// TODO: Implement truncation logic like "... (X more lines)"
			formattedResult = resultStyle.Render(resultPrefix+" "+metadata) + "\n" + resultOutput
		}

		// Combine command and result with spacing
		renderedContent = formattedCmd
		if formattedResult != "" {
			renderedContent += "\n\n" + formattedResult // Add space before result
		}
		// Use a neutral style for the combined block, prefixes handle color
		style = lipgloss.NewStyle() // Or maybe keep commandStyle?
		prefix = ""                 // Reset prefix as it's part of renderedContent

	case "function_call": // How to display non-command tools?
		prefix = "tool"
		style = functionStyle // Reuse style for now
		renderedContent = wordWrap(msg.Content, width-len(prefix)-2)
	case "function_result":
		prefix = "tool.result"
		style = commandOutputStyle // Reuse style for now
		renderedContent = wordWrap(msg.Content, width-len(prefix)-2)

	default:
		prefix = msg.Role
		style = infoStyle
		renderedContent = wordWrap(msg.Content, width-len(prefix)-2)
	}

	// Add a border and padding for assistant messages to make them stand out
	if msg.Role == "assistant" {
		// Create a bordered style for assistant messages
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("2")). // Green for assistant
			Padding(0, 1).
			Width(width - 4)

		// Combine prefix and content
		prefixedContent := style.Render(prefix) + " " + renderedContent

		// Apply the border to the entire message
		finalRendered = borderStyle.Render(prefixedContent)
	} else {
		// Normal rendering for other messages
		if prefix != "" {
			finalRendered = style.Render(prefix) + " " + renderedContent
		} else {
			finalRendered = renderedContent // Use content directly if prefix was handled internally
		}
	}

	// Add timestamp if needed
	if showTimestamp {
		timeStr := msg.Timestamp.Format("15:04:05")
		finalRendered += "\n" + timestampStyle.Render(timeStr)
	}

	return finalRendered
}

// Helper function to truncate content for logs
func truncateForLog(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

// Update handles messages for the model
func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	// Handle think tick
	if _, ok := msg.(thinkTickMsg); ok && m.isThinking {
		if m.ready {
			m.updateViewport()
		}
		return m, m.thinkTick()
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			// Only handle enter if there's text input
			if m.textInput.Value() != "" {
				userMsg := m.textInput.Value()
				m.textInput.SetValue("") // Clear input here
				// Return a command that sends the UserInputSubmitMsg
				return m, func() tea.Msg {
					return UserInputSubmitMsg{Content: userMsg}
				}
			}
			// If input was empty, do nothing
			return m, nil // Prevent Enter from being processed further down
		case tea.KeyCtrlT:
			// Toggle timestamps
			m.ToggleTimestamps()
		case tea.KeyCtrlS:
			// Toggle system messages
			m.ToggleSystemMessages()
		case tea.KeyCtrlX:
			// Clear history
			m.ClearHistory()
		}
	case tea.WindowSizeMsg:
		// Record window size
		m.width = msg.Width
		m.height = msg.Height

		// Set up the viewport if not ready
		if !m.ready {
			headerHeight := 6 // Status bar takes up space
			footerHeight := 3 // Input box and help text take up space

			// Make sure we have a valid height to work with
			viewportHeight := msg.Height - headerHeight - footerHeight
			if viewportHeight < 1 {
				viewportHeight = 1 // Ensure minimum height of 1
			}

			m.viewport = viewport.New(msg.Width, viewportHeight)
			m.viewport.YPosition = headerHeight
			// m.viewport.HighPerformanceRendering = true // Disable this for debugging

			// Update text input width
			m.textInput.SetWidth(msg.Width - 2)

			// Set the ready flag before updating viewport
			m.ready = true

			// Now that we're ready, update the viewport
			m.updateViewport()
		} else {
			// If already initialized, just resize the viewport
			// Make sure we have a valid height to work with
			viewportHeight := msg.Height - 9
			if viewportHeight < 1 {
				viewportHeight = 1 // Ensure minimum height of 1
			}

			m.viewport.Width = msg.Width
			m.viewport.Height = viewportHeight
			m.textInput.SetWidth(msg.Width - 2)
			m.updateViewport()
		}
	}

	// Only update the viewport if we're ready
	if m.ready {
		// Update viewport
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update text input ONLY IF the message was not KeyEnter
	// (KeyEnter is handled by App.Update now)
	if keyMsg, ok := msg.(tea.KeyMsg); !ok || keyMsg.Type != tea.KeyEnter {
		newInput, inputCmd := m.textInput.Update(msg)
		m.textInput = newInput
		cmds = append(cmds, inputCmd)
	}

	// Add thinking tick if in thinking state
	if m.isThinking {
		cmds = append(cmds, m.thinkTick())
	}

	return m, tea.Batch(cmds...)
}

// View renders the chat UI
func (m ChatModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Create status bar
	sessionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7")).
		Background(lipgloss.Color("0")).
		Bold(true).
		Width(m.width).
		Padding(0, 1)

	statusLine1 := sessionStyle.Render("codex-go")

	// Add thinking indicator to the status bar if active
	statusInfo := fmt.Sprintf("localhost session: %s\n• workdir: %s\n• model: %s\n• approval: %s",
		m.sessionID, m.workDir, m.model, m.approvalMode)

	if m.isThinking {
		elapsed := time.Since(m.thinkingStart).Round(time.Second)
		thinkingStatus := fmt.Sprintf("THINKING: %s", elapsed)
		if m.currentStatus != "" {
			thinkingStatus += fmt.Sprintf(" - %s", m.currentStatus)
		}
		// Add thinking status to status bar with bright color
		statusInfo += fmt.Sprintf("\n• %s", lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // Bright yellow
			Bold(true).
			Render(thinkingStatus))
	}

	statusLine2 := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7")).
		Background(lipgloss.Color("0")).
		Padding(0, 1).
		Width(m.width).
		Render(statusInfo)

	statusBar := lipgloss.JoinVertical(lipgloss.Left, statusLine1, statusLine2)

	// Add key bindings help
	helpText := infoStyle.Render("send q or ctrl+c to exit | send \"/clear\" to reset | send \"/help\" for commands | press enter to send")

	// Get viewport content - make sure we've updated it
	// No need to force update on every view since we already do it after message processing
	viewContent := m.viewport.View()

	// If thinking, also add a visible indicator at the bottom of messages for extra visibility
	if m.isThinking {
		elapsed := time.Since(m.thinkingStart).Round(time.Second)
		thinkingText := fmt.Sprintf("thinking for %s", elapsed)
		if m.currentStatus != "" {
			thinkingText = fmt.Sprintf("%s - %s", thinkingText, m.currentStatus)
		}

		// Add to the bottom of the viewport with a more visible style
		thinkingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // Bright yellow
			Background(lipgloss.Color("8")).  // Dark gray background
			Bold(true).
			Width(m.width-2).       // Full width minus padding
			Align(lipgloss.Center). // Center text
			Padding(0, 0)

		// Add to the bottom of the viewport
		if viewContent != "" {
			viewContent += "\n\n"
		}
		viewContent += thinkingStyle.Render(thinkingText)
	}

	// Combine the status bar, viewport, help text, and textinput
	finalView := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n",
		statusBar,
		viewContent, // Use our adjusted viewport content
		helpText,
		m.textInput.View(),
	)
	return finalView
}

// Simple ticker for thinking updates
type thinkTickMsg struct{}

func (m ChatModel) thinkTick() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
		return thinkTickMsg{}
	})
}

// StartThinking starts the thinking timer
func (m *ChatModel) StartThinking() {
	m.isThinking = true
	m.thinkingStart = time.Now()
	m.currentStatus = "initializing..."

	// Add a temporary message to show the thinking state
	m.AddMessage(Message{
		Role:      "thinking",
		Content:   "Thinking...",
		Timestamp: time.Now(),
	})

	// If viewport is ready, update it to show the thinking state immediately
	if m.ready {
		m.updateViewport()
	}
}

// StopThinking stops the thinking timer
func (m *ChatModel) StopThinking() {
	m.isThinking = false
	m.currentStatus = ""

	// Remove the thinking message
	var nonThinkingMessages []Message
	for _, msg := range m.messages {
		if msg.Role != "thinking" {
			nonThinkingMessages = append(nonThinkingMessages, msg)
		}
	}
	m.messages = nonThinkingMessages

	// Update viewport to remove thinking indicator
	if m.ready {
		m.updateViewport()
	}
}

// SetThinkingStatus updates the current status during thinking
func (m *ChatModel) SetThinkingStatus(status string) {
	m.currentStatus = status

	// Update viewport to show the new status
	if m.ready && m.isThinking {
		m.updateViewport()
	}
}

// FromAgentMessage converts an agent message to a chat message
func FromAgentMessage(agentMessage agent.Message) Message {
	return Message{
		Role:      agentMessage.Role,
		Content:   agentMessage.Content,
		Timestamp: time.Now(),
	}
}

// FromAgentResponseItem converts an agent response item to a chat message
func FromAgentResponseItem(item agent.ResponseItem) []Message {
	var messages []Message

	switch item.Type {
	case "message":
		if item.Message != nil {
			messages = append(messages, Message{
				Role:      item.Message.Role,
				Content:   item.Message.Content,
				Timestamp: time.Now(),
			})
		}
	case "function_call":
		if item.FunctionCall != nil {
			messages = append(messages, Message{
				Role:      "function_call",
				Content:   fmt.Sprintf("%s(%s)", item.FunctionCall.Name, item.FunctionCall.Arguments),
				Timestamp: time.Now(),
			})
		}
	case "function_call_output":
		if item.FunctionOutput != nil {
			messages = append(messages, Message{
				Role:      "function_result",
				Content:   item.FunctionOutput.Output,
				Timestamp: time.Now(),
				ANSI:      true, // Assume function output may contain ANSI codes
			})
		}
	}

	return messages
}

// wordWrap wraps text at the specified width
func wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}

	var sb strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if len(line) <= width {
			sb.WriteString(line)
		} else {
			// Simple word wrapping
			words := strings.Fields(line)
			lineLength := 0

			for _, word := range words {
				if lineLength+len(word)+1 > width {
					sb.WriteString("\n")
					lineLength = 0
				} else if lineLength > 0 {
					sb.WriteString(" ")
					lineLength++
				}

				sb.WriteString(word)
				lineLength += len(word)
			}
		}

		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// AddCommandMessage adds a command execution message to the local messages
func (m *ChatModel) AddCommandMessage(cmdStr string, result *CommandResult) {
	// Use logger instead of direct stderr output
	if m.logger != nil && m.logger.IsEnabled() {
		m.logger.Log("AddCommandMessage called with command: %s", cmdStr)
	}

	m.AddMessage(Message{
		Role:          "command",
		Content:       cmdStr,
		Timestamp:     time.Now(),
		CommandResult: result,
	})
}

// InputIsEmpty returns true if the input field is empty
func (m ChatModel) InputIsEmpty() bool {
	return m.textInput.Value() == ""
}

// InputValue returns the current value of the text input
func (m *ChatModel) InputValue() string {
	return m.textInput.Value()
}

// SetInputValue sets the value of the text input
func (m *ChatModel) SetInputValue(s string) {
	m.textInput.SetValue(s)
}

// ForceUpdateViewport explicitly calls updateViewport if the model is ready.
func (m *ChatModel) ForceUpdateViewport() {
	// Only force update if the viewport is ready
	if m.ready && m.viewport.Height > 0 {
		m.updateViewport()
	}
}

// ClearMessages clears the locally displayed messages in the UI.
func (m *ChatModel) ClearMessages() {
	m.messages = []Message{}
	// Optionally, force a viewport update after clearing
	m.ForceUpdateViewport()
}
