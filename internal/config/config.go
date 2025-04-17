package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ApprovalMode represents the approval policy for commands and file changes
type ApprovalMode string

const (
	// Suggest mode requires approval for both file edits and commands
	Suggest ApprovalMode = "suggest"
	// AutoEdit mode automatically approves file edits but requires approval for commands
	AutoEdit ApprovalMode = "auto-edit"
	// FullAuto mode automatically approves both file edits and commands (sandbox enforced)
	FullAuto ApprovalMode = "full-auto"
	// DangerousAutoApprove mode automatically approves everything without sandboxing
	// EXTREMELY DANGEROUS - use only in ephemeral environments
	DangerousAutoApprove ApprovalMode = "dangerous"
)

// Config holds all configuration options for the application
type Config struct {
	// API configuration
	APIKey     string `mapstructure:"api_key"`
	Model      string `mapstructure:"model"`
	BaseURL    string `mapstructure:"base_url"`
	APITimeout int    `mapstructure:"api_timeout"` // in seconds

	// Project configuration
	CWD               string `mapstructure:"cwd"`
	ProjectDocPath    string `mapstructure:"project_doc_path"`
	DisableProjectDoc bool   `mapstructure:"disable_project_doc"`
	Instructions      string `mapstructure:"instructions"`

	// UI configuration
	FullStdout bool `mapstructure:"full_stdout"` // Don't truncate command output

	// Approval configuration
	ApprovalMode ApprovalMode `mapstructure:"approval_mode"`

	// Logging configuration
	Debug   bool   `mapstructure:"debug"`    // Enable debug logging
	LogFile string `mapstructure:"log_file"` // Path to log file
}

const (
	// Default configuration values
	DefaultModel      = "gpt-4o"
	DefaultBaseURL    = "https://api.openai.com/v1"
	DefaultAPITimeout = 60 // seconds
	DefaultConfigDir  = ".codex"
)

// Load loads configuration from files, environment variables, and flags
func Load() (*Config, error) {
	// Initialize config with defaults
	config := &Config{
		Model:        DefaultModel,
		BaseURL:      DefaultBaseURL,
		APITimeout:   DefaultAPITimeout,
		ApprovalMode: Suggest,
		CWD:          getWorkingDirectory(),
	}

	// Set up viper
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Add config path
	configDir := getConfigDir()
	v.AddConfigPath(configDir)

	// Set environment variable prefix
	v.SetEnvPrefix("CODEX")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Allow special handling for OpenAI API key
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		config.APIKey = apiKey
	}

	// Attempt to read the config file
	if err := v.ReadInConfig(); err != nil {
		// Config file not found is not an error
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Unmarshal config to struct
	if err := v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Load instructions from file if it exists
	instructionsPath := filepath.Join(configDir, "instructions.md")
	if _, err := os.Stat(instructionsPath); err == nil {
		data, err := os.ReadFile(instructionsPath)
		if err == nil {
			config.Instructions = string(data)
		}
	}

	// Load project doc if it exists and is not disabled
	if !config.DisableProjectDoc && config.ProjectDocPath == "" {
		// Check for codex.md in current directory
		projectDocPath := filepath.Join(config.CWD, "codex.md")
		if _, err := os.Stat(projectDocPath); err == nil {
			config.ProjectDocPath = projectDocPath
		}
	}

	return config, nil
}

// LoadProjectDoc loads the content of the project documentation file if specified
func (c *Config) LoadProjectDoc() (string, error) {
	if c.DisableProjectDoc || c.ProjectDocPath == "" {
		return "", nil
	}

	data, err := os.ReadFile(c.ProjectDocPath)
	if err != nil {
		return "", fmt.Errorf("error reading project doc: %w", err)
	}

	return string(data), nil
}

// getConfigDir returns the path to the config directory
func getConfigDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	configDir := filepath.Join(homeDir, DefaultConfigDir)

	// Create the directory if it doesn't exist
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		os.MkdirAll(configDir, 0755)
	}

	return configDir
}

// getWorkingDirectory returns the current working directory
func getWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
