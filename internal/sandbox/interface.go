package sandbox

import (
	"context"
	"io"
	"time"
)

// CommandResult represents the result of executing a command
type CommandResult struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	Success    bool
	Error      error
	Duration   time.Duration
	Command    string
	WorkingDir string
}

// SandboxOptions configures the sandbox behavior
type SandboxOptions struct {
	// Command to execute
	Command string

	// Working directory
	WorkingDir string

	// Allow network access
	AllowNetwork bool

	// Allow file writes outside working directory
	AllowFileWrites bool

	// Timeout for command execution
	Timeout time.Duration

	// Environment variables to set
	Env map[string]string

	// Input to provide to the command
	Stdin io.Reader

	// Capture stdout and stderr
	Stdout io.Writer
	Stderr io.Writer
}

// Sandbox defines the interface for sandboxed command execution
type Sandbox interface {
	// Execute runs a command in the sandbox with the given options
	Execute(ctx context.Context, opts SandboxOptions) (*CommandResult, error)

	// IsAvailable checks if this sandbox implementation is available on the current system
	IsAvailable() bool

	// Name returns the name of the sandbox implementation
	Name() string
}

// NewSandbox creates a new sandbox based on the current platform
func NewSandbox() Sandbox {
	// Try platform-specific sandboxes in order of preference
	if sb := NewMacOSSandbox(); sb.IsAvailable() {
		return sb
	}

	if sb := NewLinuxSandbox(); sb.IsAvailable() {
		return sb
	}

	// Fall back to basic sandbox
	return NewBasicSandbox()
}
