package functions

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/epuerta/codex-go/internal/fileops"
	"github.com/epuerta/codex-go/internal/sandbox"
)

// Registry holds registered functions
type Registry struct {
	functions map[string]Function
}

// Function represents a function that can be called by the agent
type Function func(args string) (string, error)

// NewRegistry creates a new function registry
func NewRegistry() *Registry {
	return &Registry{
		functions: make(map[string]Function),
	}
}

// Register adds a function to the registry
func (r *Registry) Register(name string, fn Function) {
	r.functions[name] = fn
}

// Get retrieves a function from the registry
func (r *Registry) Get(name string) Function {
	return r.functions[name]
}

// ReadFile reads the contents of a file
func ReadFile(args string) (string, error) {
	// Parse arguments
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Check if the path is valid
	if params.Path == "" {
		return "", fmt.Errorf("path parameter is required")
	}

	// Resolve the path
	absPath, err := filepath.Abs(params.Path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Read the file
	content, err := ioutil.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	return string(content), nil
}

// WriteFile writes content to a file
func WriteFile(args string) (string, error) {
	// Parse arguments
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Check if parameters are valid
	if params.Path == "" {
		return "", fmt.Errorf("path parameter is required")
	}

	// Resolve the path
	absPath, err := filepath.Abs(params.Path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Create the directory if it doesn't exist
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Write the file
	if err := ioutil.WriteFile(absPath, []byte(params.Content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), params.Path), nil
}

// PatchFile applies a patch to a file
func PatchFile(args string) (string, error) {
	// Parse arguments
	var params struct {
		Path      string `json:"path"`
		Patch     string `json:"patch"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		Type      string `json:"type"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Check if parameters are valid
	if params.Path == "" {
		return "", fmt.Errorf("path parameter is required")
	}
	if params.Type == "" {
		params.Type = "replace" // Default to replace
	}

	// Create a patch operation
	op := fileops.PatchOperation{
		Type:      params.Type,
		Path:      params.Path,
		Content:   params.Content,
		StartLine: params.StartLine,
		EndLine:   params.EndLine,
	}

	// Apply the patch
	result, err := fileops.ApplyPatch(op)
	if err != nil {
		return "", fmt.Errorf("failed to apply patch: %w", err)
	}

	return fmt.Sprintf("Successfully patched %s (%d -> %d lines)", params.Path, result.OriginalLines, result.NewLines), nil
}

// ExecuteCommand executes a shell command
func ExecuteCommand(args string) (string, error) {
	// Parse arguments
	var params struct {
		Command      string            `json:"command"`
		WorkingDir   string            `json:"workingDir"`
		Env          map[string]string `json:"env"`
		Timeout      int               `json:"timeout"`
		AllowNetwork bool              `json:"allowNetwork"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	// Check if command is valid
	if params.Command == "" {
		return "", fmt.Errorf("command parameter is required")
	}

	// Set working directory to current directory if not specified
	if params.WorkingDir == "" {
		var err error
		params.WorkingDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Set timeout
	timeout := time.Duration(params.Timeout) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second // Default timeout: 60 seconds
	}

	// Create sandbox options
	opts := sandbox.SandboxOptions{
		Command:         params.Command,
		WorkingDir:      params.WorkingDir,
		AllowNetwork:    params.AllowNetwork,
		AllowFileWrites: true, // Allow writes to the working directory
		Timeout:         timeout,
		Env:             params.Env,
	}

	// Create a sandbox
	sb := sandbox.NewSandbox()

	// Execute the command
	ctx := context.Background()
	result, err := sb.Execute(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	// Check if the command was successful
	if !result.Success {
		return "", fmt.Errorf("command failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	return result.Stdout, nil
}

// ListDirectory lists the contents of a directory
func ListDirectory(args string) (string, error) {
	// Parse arguments
	var params struct {
		Path string `json:"path"`
	}
	// Only unmarshal if args is not empty
	if args != "" {
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// Use current directory if path is not specified
	if params.Path == "" {
		var err error
		params.Path, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Resolve the path
	absPath, err := filepath.Abs(params.Path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// List the directory
	files, err := ioutil.ReadDir(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	// Format the output
	var result string
	result = fmt.Sprintf("Contents of %s:\n\n", absPath)

	for _, file := range files {
		fileType := "file"
		if file.IsDir() {
			fileType = "dir"
		}

		size := file.Size()
		var sizeStr string
		if size < 1024 {
			sizeStr = fmt.Sprintf("%dB", size)
		} else if size < 1024*1024 {
			sizeStr = fmt.Sprintf("%.1fKB", float64(size)/1024)
		} else {
			sizeStr = fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
		}

		result += fmt.Sprintf("[%s] %s (%s, %s)\n", fileType, file.Name(), sizeStr, file.ModTime().Format("2006-01-02 15:04:05"))
	}

	return result, nil
}
