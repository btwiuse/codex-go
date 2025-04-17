package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// MacOSSandbox implements the Sandbox interface using macOS Seatbelt
type MacOSSandbox struct{}

// NewMacOSSandbox creates a new macOS sandbox
func NewMacOSSandbox() Sandbox {
	return &MacOSSandbox{}
}

// Name returns the name of the sandbox
func (s *MacOSSandbox) Name() string {
	return "macOS Seatbelt"
}

// IsAvailable checks if sandbox-exec is available on the system
func (s *MacOSSandbox) IsAvailable() bool {
	_, err := exec.LookPath("sandbox-exec")
	return err == nil
}

// Execute runs a command in the sandbox
func (s *MacOSSandbox) Execute(ctx context.Context, opts SandboxOptions) (*CommandResult, error) {
	startTime := time.Now()

	// Create the sandbox profile
	profile, err := s.createSandboxProfile(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox profile: %w", err)
	}

	// Write the profile to a temporary file
	profileFile, err := os.CreateTemp("", "codex-sandbox-*.sb")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file for sandbox profile: %w", err)
	}
	defer os.Remove(profileFile.Name())

	if _, err := profileFile.WriteString(profile); err != nil {
		return nil, fmt.Errorf("failed to write sandbox profile: %w", err)
	}
	if err := profileFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close sandbox profile file: %w", err)
	}

	// Build the command
	cmd := exec.CommandContext(ctx, "sandbox-exec", "-f", profileFile.Name(), "/bin/sh", "-c", opts.Command)
	cmd.Dir = opts.WorkingDir

	// Set up environment
	if opts.Env != nil {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

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
	err = cmd.Run()
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

// createSandboxProfile creates a macOS Seatbelt profile based on the options
func (s *MacOSSandbox) createSandboxProfile(opts SandboxOptions) (string, error) {
	// Get absolute path of working directory
	workDir, err := filepath.Abs(opts.WorkingDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path of working directory: %w", err)
	}

	// Home directory for configuration files
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Get temporary directory
	tempDir := os.TempDir()

	// Build the sandbox profile
	profile := `(version 1)

; Basic process controls
(allow process*)
(allow sysctl*)
(allow signal)
(allow mach*)
(allow file-ioctl)

; Default deny
(deny default)

; Allow file reads from system paths
(allow file-read*
    (subpath "/usr")
    (subpath "/bin")
    (subpath "/sbin")
    (subpath "/Library")
    (subpath "/System")
    (literal "/etc")
    (subpath "/etc")
    (subpath "/dev")
    (literal "/tmp")
    (subpath "/private/var")
)

; Allow reads from home directory config files
(allow file-read*
    (subpath "` + filepath.Join(homeDir, ".codex") + `")
    (subpath "` + filepath.Join(homeDir, ".config") + `")
)

; Allow reads from temporary directory
(allow file-read*
    (subpath "` + tempDir + `")
)

; Allow reads from working directory
(allow file-read*
    (subpath "` + workDir + `")
)

; Conditionally allow file writes based on options
`

	// Allow writes to the working directory by default
	profile += `
(allow file-write*
    (subpath "` + workDir + `")
    (subpath "` + tempDir + `")
)
`

	// Handle network access
	if !opts.AllowNetwork {
		profile += `
; Deny network access
(deny network*)
`
	} else {
		profile += `
; Allow network access
(allow network*)
`
	}

	return profile, nil
}
