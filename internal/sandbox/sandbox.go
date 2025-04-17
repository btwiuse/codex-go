package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// ExecutionResult represents the result of a command execution
type ExecutionResult struct {
	Command        string
	Args           []string
	Output         string
	Error          string
	ExitCode       int
	Duration       time.Duration
	StartTime      time.Time
	NetworkEnabled bool
}

// Options represents options for command execution
type Options struct {
	Cwd              string
	NetworkEnabled   bool
	AllowedPaths     []string
	AllowedCommands  []string
	MaxOutputSize    int
	Timeout          time.Duration
	EnvironmentVars  []string
	WorkingDirectory string
}

// DefaultOptions returns the default sandbox options
func DefaultOptions() Options {
	cwd, _ := os.Getwd()
	return Options{
		Cwd:             cwd,
		NetworkEnabled:  false,
		AllowedPaths:    []string{cwd},
		MaxOutputSize:   1024 * 1024, // 1 MB
		Timeout:         60 * time.Second,
		EnvironmentVars: os.Environ(),
	}
}

// Executor defines the interface for executing commands
type Executor interface {
	// Execute executes a command with the given options
	Execute(ctx context.Context, command string, args []string, options Options) (*ExecutionResult, error)
}

// CreateExecutor creates the appropriate executor for the current operating system
func CreateExecutor() (Executor, error) {
	switch runtime.GOOS {
	case "darwin":
		return NewMacOSExecutor(), nil
	case "linux":
		return NewLinuxExecutor(), nil
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

// BasicExecutor is a basic implementation of Executor
type BasicExecutor struct{}

// Execute executes a command in a basic sandbox
func (e *BasicExecutor) Execute(ctx context.Context, command string, args []string, options Options) (*ExecutionResult, error) {
	startTime := time.Now()

	// Validate the command and arguments
	if !isCommandAllowed(command, options.AllowedCommands) {
		return &ExecutionResult{
			Command:   command,
			Args:      args,
			Error:     fmt.Sprintf("command not allowed: %s", command),
			ExitCode:  -1,
			StartTime: startTime,
			Duration:  time.Since(startTime),
		}, errors.New("command not allowed")
	}

	// Create a new command
	cmd := exec.CommandContext(ctx, command, args...)

	// Set working directory
	if options.WorkingDirectory != "" {
		cmd.Dir = options.WorkingDirectory
	} else {
		cmd.Dir = options.Cwd
	}

	// Set environment variables
	cmd.Env = options.EnvironmentVars

	// If network is disabled, modify the command to run in a network-disabled environment
	if !options.NetworkEnabled {
		// This would be handled differently depending on the OS
		// For now, just set a flag
		cmd.Env = append(cmd.Env, "CODEX_NETWORK_DISABLED=1")
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the command
	err := cmd.Run()

	// Create the result
	result := &ExecutionResult{
		Command:        command,
		Args:           args,
		Output:         stdout.String(),
		Error:          stderr.String(),
		ExitCode:       0,
		StartTime:      startTime,
		Duration:       time.Since(startTime),
		NetworkEnabled: options.NetworkEnabled,
	}

	// Handle errors
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = err.Error()
		}
	}

	return result, nil
}

// isCommandAllowed checks if a command is allowed to run
func isCommandAllowed(command string, allowedCommands []string) bool {
	// If allowed commands is empty, allow all commands
	if len(allowedCommands) == 0 {
		return true
	}

	// Check if the command is in the allowedCommands list
	for _, allowed := range allowedCommands {
		if command == allowed {
			return true
		}
	}

	return false
}

// TruncateOutput truncates the output to the maximum size
func TruncateOutput(output string, maxSize int) string {
	if len(output) <= maxSize {
		return output
	}

	half := maxSize / 2
	return output[:half] + "\n...[truncated]...\n" + output[len(output)-half:]
}

// NewMacOSExecutor creates a new executor for macOS
func NewMacOSExecutor() Executor {
	return &BasicExecutor{}
}

// NewLinuxExecutor creates a new executor for Linux
func NewLinuxExecutor() Executor {
	return &BasicExecutor{}
}

// RunCommand runs a command with the default options
func RunCommand(ctx context.Context, command string, args []string) (*ExecutionResult, error) {
	executor, err := CreateExecutor()
	if err != nil {
		return nil, err
	}

	options := DefaultOptions()
	return executor.Execute(ctx, command, args, options)
}

// ExecuteCommand executes a command with the given options and approval mode
func ExecuteCommand(cmd string, approvalMode string, sandboxed bool) (*CommandResult, error) {
	// If we're in dangerous mode, bypass sandbox
	if approvalMode == "dangerous" {
		return executeUnsandboxedCommand(cmd)
	}

	// Choose the right sandbox based on the configuration
	var sb Sandbox
	if sandboxed {
		sb = NewSandbox()
	} else {
		sb = NewBasicSandbox()
	}

	// Set up the options
	opts := SandboxOptions{
		Command: cmd,
		Timeout: 30 * time.Second, // Default timeout
	}

	// Execute the command
	return sb.Execute(context.Background(), opts)
}

// executeUnsandboxedCommand runs a command directly without any sandboxing
// DANGER: This is extremely dangerous and should only be used in trusted environments
func executeUnsandboxedCommand(cmd string) (*CommandResult, error) {
	startTime := time.Now()

	// Prepare the command for execution
	execCmd := exec.Command("sh", "-c", cmd)

	// Set up pipes for stdout and stderr
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// Read stdout and stderr
	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to read stdout: %w", err)
	}

	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to read stderr: %w", err)
	}

	// Wait for the command to finish
	err = execCmd.Wait()
	duration := time.Since(startTime)

	// Get the exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	// Create the result
	result := &CommandResult{
		Stdout:   string(stdoutBytes),
		Stderr:   string(stderrBytes),
		ExitCode: exitCode,
		Success:  exitCode == 0,
		Error:    err,
		Duration: duration,
		Command:  cmd,
	}

	return result, nil
}
