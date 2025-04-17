package agent

import (
	"context"
)

// Message represents a single message in a conversation
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call from the AI
type FunctionCall struct {
	Name      string            // Name of the function to call
	Arguments string            // JSON string of arguments
	ID        string            // Unique ID for the function call
	Metadata  map[string]string // Additional metadata
}

// FunctionCallOutput represents the output of a function call
type FunctionCallOutput struct {
	CallID  string // ID of the function call
	Output  string // Output of the function call (typically JSON)
	Error   string // Error message if any
	Success bool   // Whether the function call was successful
}

// ResponseItem represents a single response item from the AI
type ResponseItem struct {
	Type             string              `json:"type"` // "message", "function_call", "followup_complete"
	Message          *Message            `json:"message,omitempty"`
	FunctionCall     *FunctionCall       `json:"functionCall,omitempty"`
	FunctionOutput   *FunctionCallOutput `json:"functionOutput,omitempty"`
	ThinkingDuration int64               `json:"thinkingDuration"`
}

// ResponseHandler is a callback for handling streaming response items
type ResponseHandler func(itemJSON string)

// CommandConfirmation represents user confirmation for a command
type CommandConfirmation struct {
	Approved        bool   // Whether the command is approved
	DenyMessage     string // Message to show if denied
	ModifiedCommand string // Modified command if any
}

// FileChangeConfirmation represents user confirmation for a file change
type FileChangeConfirmation struct {
	Approved     bool   // Whether the file change is approved
	DenyMessage  string // Message to show if denied
	ModifiedDiff string // Modified diff if any
}

// Agent defines the interface for AI agents
type Agent interface {
	// SendMessage sends a message to the AI and streams the response
	// Returns true if the stream finished requesting tool calls, false otherwise.
	SendMessage(ctx context.Context, messages []Message, handler ResponseHandler) (bool, error)

	// SendFileChange sends a file change to the AI for approval
	SendFileChange(ctx context.Context, filePath string, diff string) (*FileChangeConfirmation, error)

	// GetCommandConfirmation gets user confirmation for a command
	GetCommandConfirmation(ctx context.Context, command string, args []string) (*CommandConfirmation, error)

	// ClearHistory clears the conversation history
	ClearHistory()

	// GetHistory returns the conversation history
	GetHistory() *ConversationHistory

	// Cancel cancels the current streaming response
	Cancel()

	// Close closes the agent and releases any resources
	Close() error

	// SendFunctionResult sends a function result back to the agent
	SendFunctionResult(ctx context.Context, callID, functionName, output string, success bool) error
}
