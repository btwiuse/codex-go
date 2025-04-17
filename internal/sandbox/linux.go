package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// LinuxSandbox implements the Sandbox interface using environment restrictions
type LinuxSandbox struct{}

// NewLinuxSandbox creates a new Linux sandbox
func NewLinuxSandbox() Sandbox {
	return &LinuxSandbox{}
}

// Name returns the name of the sandbox
func (s *LinuxSandbox) Name() string {
	return "Linux Environment Sandbox"
}

// IsAvailable checks if this sandbox is available on the system
func (s *LinuxSandbox) IsAvailable() bool {
	return runtime.GOOS == "linux"
}

// Execute runs a command in the sandbox
func (s *LinuxSandbox) Execute(ctx context.Context, opts SandboxOptions) (*CommandResult, error) {
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

// Future enhancement: Implement UnshareCommand for namespaces isolation
// func (s *LinuxSandbox) createUnshareCommand(opts SandboxOptions) (*exec.Cmd, error) {
// 	// This would use Linux namespaces with unshare for better isolation
// 	// Similar to the Docker approach but without requiring Docker
// 	// Implementation would use "unshare" command with appropriate flags
// 	// for mount, network, pid isolation
// }
