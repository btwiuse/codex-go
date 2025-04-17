package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// BasicSandbox implements the Sandbox interface with minimal restrictions
// It's intended as a fallback when platform-specific sandboxes are not available
type BasicSandbox struct{}

// NewBasicSandbox creates a new basic sandbox
func NewBasicSandbox() Sandbox {
	return &BasicSandbox{}
}

// Name returns the name of the sandbox
func (s *BasicSandbox) Name() string {
	return "Basic Environment Sandbox"
}

// IsAvailable always returns true since this is a fallback implementation
func (s *BasicSandbox) IsAvailable() bool {
	return true
}

// Execute runs a command with basic restrictions
func (s *BasicSandbox) Execute(ctx context.Context, opts SandboxOptions) (*CommandResult, error) {
	startTime := time.Now()

	// Build the command
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", opts.Command)
	cmd.Dir = opts.WorkingDir

	// Set up restricted environment
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"TERM=" + os.Getenv("TERM"),
		"LANG=" + os.Getenv("LANG"),
		"CODEX_SANDBOX=1", // Mark that we're running in a sandbox
	}

	// Add custom environment variables
	if opts.Env != nil {
		for k, v := range opts.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	cmd.Env = env

	// Set up stdin, stdout, stderr
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	var stdout, stderr bytes.Buffer
	if opts.Stdout != nil {
		cmd.Stdout = io.MultiWriter(&stdout, opts.Stdout)
	} else {
		cmd.Stdout = &stdout
	}

	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(&stderr, opts.Stderr)
	} else {
		cmd.Stderr = &stderr
	}

	// Apply timeout if specified
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", opts.Command)
		cmd.Dir = opts.WorkingDir
		cmd.Env = env
		if opts.Stdin != nil {
			cmd.Stdin = opts.Stdin
		}
		cmd.Stdout = cmd.Stdout
		cmd.Stderr = cmd.Stderr
	}

	// Execute the command
	err := cmd.Run()
	duration := time.Since(startTime)

	// Build the result
	result := &CommandResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		Duration:   duration,
		Command:    opts.Command,
		WorkingDir: opts.WorkingDir,
		Success:    err == nil,
	}

	if err != nil {
		result.Error = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	return result, nil
}
