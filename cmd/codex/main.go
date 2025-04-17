package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/config"
	"github.com/epuerta/codex-go/internal/logging"
	"github.com/epuerta/codex-go/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	// Version is set during build
	Version = "dev"
	// GitCommit is set during build
	GitCommit = "none"
	// BuildDate is set during build
	BuildDate = "unknown"

	// Logger instance - global within main package for simplicity
	appLogger logging.Logger
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "codex [flags] [prompt]",
	Short: "Lightweight coding agent that runs in your terminal",
	Long: `Codex is a lightweight coding agent that runs in your terminal.
It can help you with programming tasks, explain code, suggest changes,
and execute commands - all via natural language.

Examples:
  codex "Write a Go function to parse JSON"
  codex "Explain this codebase to me"
  codex --approval-mode full-auto "Create a CLI tool that converts markdown to HTML"`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// Call the run implementation directly
		runCmdImpl(cmd, args)
	},
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildDate),
}

func init() {
	// Add global flags using cobra/pflag
	rootCmd.PersistentFlags().StringP("model", "m", "gpt-4o", "AI model to use for completions")
	rootCmd.PersistentFlags().StringP("approval-mode", "a", "suggest", "Approval mode: suggest, auto-edit, or full-auto")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Non-interactive mode that only prints the assistant's final output")
	rootCmd.PersistentFlags().StringArrayP("image", "i", nil, "Path to image file(s) to include as input")
	rootCmd.PersistentFlags().Bool("no-project-doc", false, "Do not automatically include the repository's 'codex.md'")
	rootCmd.PersistentFlags().String("project-doc", "", "Include an additional markdown file as context")
	rootCmd.PersistentFlags().Bool("full-stdout", false, "Do not truncate stdout/stderr from command outputs")
	rootCmd.PersistentFlags().Bool("auto-edit", false, "Automatically approve file edits; still prompt for commands")
	rootCmd.PersistentFlags().Bool("full-auto", false, "Automatically approve edits and commands when executed in the sandbox")
	rootCmd.PersistentFlags().Bool("dangerously-auto-approve-everything", false, "Skip all confirmation prompts and execute commands without sandboxing. EXTREMELY DANGEROUS - use only in ephemeral environments.")
	rootCmd.PersistentFlags().BoolP("config", "c", false, "Open the instructions file in your editor")
	rootCmd.PersistentFlags().StringP("view", "v", "", "Inspect a previously saved rollout instead of starting a session")

	// Add logging flags
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging to a file")
	rootCmd.PersistentFlags().String("log-file", "", "Path to the log file (default: ~/.cache/codex-go/logs/codex-go-<timestamp>.log)")

	// Bind standard Go flags to pflag
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	// Add subcommands
	rootCmd.AddCommand(completionCmd())
}

// completionCmd creates the completion command for shell completion scripts
func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for Codex.
To load completions:

Bash:
  $ source <(codex completion bash)

Zsh:
  $ source <(codex completion zsh)

Fish:
  $ codex completion fish | source
`,
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				cmd.Root().GenFishCompletion(os.Stdout, true)
			}
		},
	}

	return cmd
}

// runCmdImpl implements the run command functionality
func runCmdImpl(cmd *cobra.Command, args []string) {
	// Get flags
	model, _ := cmd.Flags().GetString("model")
	approvalModeStr, _ := cmd.Flags().GetString("approval-mode")
	quiet, _ := cmd.Flags().GetBool("quiet")
	noProjectDoc, _ := cmd.Flags().GetBool("no-project-doc")
	projectDoc, _ := cmd.Flags().GetString("project-doc")
	fullStdout, _ := cmd.Flags().GetBool("full-stdout")
	autoEdit, _ := cmd.Flags().GetBool("auto-edit")
	fullAuto, _ := cmd.Flags().GetBool("full-auto")
	dangerouslyAutoApprove, _ := cmd.Flags().GetBool("dangerously-auto-approve-everything")
	configFlag, _ := cmd.Flags().GetBool("config")
	viewRollout, _ := cmd.Flags().GetString("view")
	images, _ := cmd.Flags().GetStringArray("image")
	// Get logging flags
	debugFlag, _ := cmd.Flags().GetBool("debug")
	logFileFlag, _ := cmd.Flags().GetString("log-file")

	// --- Initialize Logger FIRST ---
	var err error
	if debugFlag {
		logPath := logFileFlag
		if logPath == "" {
			// Determine default log path
			cacheDir, err := os.UserCacheDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not get user cache directory: %v. Logging to current dir.\n", err)
				cacheDir = "."
			}
			logDir := filepath.Join(cacheDir, "codex-go", "logs")
			logFile := fmt.Sprintf("codex-go-%s.log", time.Now().Format("20060102-150405"))
			logPath = filepath.Join(logDir, logFile)
		}
		appLogger, err = logging.NewFileLogger(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating file logger: %v\n", err)
			os.Exit(1)
		}
		// Ensure logger is closed on exit
		defer func() {
			if appLogger != nil {
				if closeErr := appLogger.Close(); closeErr != nil {
					fmt.Fprintf(os.Stderr, "Error closing logger: %v\n", closeErr)
				}
			}
		}()

		// Optional: Add symlink logic here
		createLatestLogSymlink(logPath)

		appLogger.Log("--- Codex-Go Session Start --- Version: %s, Commit: %s, Built: %s", Version, GitCommit, BuildDate)
		appLogger.Log("Debug logging enabled. Log file: %s", logPath)
	} else {
		appLogger = logging.NewNilLogger()
	}
	// --- End Logger Initialization ---

	// Check if we need to open the config
	if configFlag {
		openConfigInEditor()
		return
	}

	// Check if we're viewing a rollout
	if viewRollout != "" {
		viewSavedRollout(viewRollout) // Logger is already initialized
		return
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		appLogger.Log("Error loading config: %v", err) // Use logger
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override config with flags
	if model != "" {
		cfg.Model = model
	}
	// Set logging config AFTER loading base config but before using it
	cfg.Debug = debugFlag
	cfg.LogFile = logFileFlag // Store the *flag* value, logger uses resolved path

	// Set approval mode based on flags in order of priority
	if dangerouslyAutoApprove {
		cfg.ApprovalMode = config.DangerousAutoApprove
	} else if fullAuto {
		cfg.ApprovalMode = config.FullAuto
	} else if autoEdit {
		cfg.ApprovalMode = config.AutoEdit
	} else if approvalModeStr != "" {
		switch strings.ToLower(approvalModeStr) {
		case "suggest":
			cfg.ApprovalMode = config.Suggest
		case "auto-edit":
			cfg.ApprovalMode = config.AutoEdit
		case "full-auto":
			cfg.ApprovalMode = config.FullAuto
		case "dangerous":
			cfg.ApprovalMode = config.DangerousAutoApprove
		default:
			appLogger.Log("Invalid approval mode: %s. Using 'suggest'.", approvalModeStr) // Use logger
			fmt.Fprintf(os.Stderr, "Invalid approval mode: %s. Using 'suggest'.\n", approvalModeStr)
		}
	}

	// Set full stdout option
	cfg.FullStdout = fullStdout

	// Override project doc settings
	if noProjectDoc {
		cfg.DisableProjectDoc = true
	}
	if projectDoc != "" {
		cfg.ProjectDocPath = projectDoc
	}

	appLogger.Log("Config loaded: Model=%s, ApprovalMode=%s, CWD=%s", cfg.Model, cfg.ApprovalMode, cfg.CWD)

	// Create agent
	ai, err := agent.NewOpenAIAgent(cfg, appLogger)
	if err != nil {
		appLogger.Log("Error creating agent: %v", err) // Use logger
		fmt.Fprintf(os.Stderr, "Error creating agent: %v\n", err)
		os.Exit(1)
	}
	defer ai.Close()

	// Get prompt from args
	var prompt string
	if len(args) > 0 {
		prompt = strings.Join(args, " ")
	}

	// If quiet mode, run with prompt and exit
	if quiet {
		if prompt == "" {
			appLogger.Log("Error: quiet mode requires a prompt.") // Use logger
			fmt.Fprintf(os.Stderr, "Error: quiet mode requires a prompt.\n")
			os.Exit(1)
		}

		runQuietMode(ai, prompt, cfg)
		return
	}

	// Run interactive mode
	runInteractiveMode(ai, prompt, cfg, images)
}

// runQuietMode runs the agent in quiet mode with a prompt
func runQuietMode(ai *agent.OpenAIAgent, prompt string, cfg *config.Config) {
	appLogger.Log("Running in quiet mode with prompt: %s", prompt)
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		appLogger.Log("Cancellation signal received.") // Use logger
		fmt.Println("\nCancelling...")
		cancel()
		ai.Cancel()
	}()

	// Create messages including system prompt
	messages := []agent.Message{}
	if cfg.Instructions != "" {
		messages = append(messages, agent.Message{Role: "system", Content: cfg.Instructions})
	}
	messages = append(messages, agent.Message{Role: "user", Content: prompt})

	// Send message and collect response
	var finalResponse string

	handler := func(itemJSON string) {
		appLogger.Log("Quiet mode received item: %s", itemJSON) // Use logger
		// Unmarshal
		var item agent.ResponseItem
		if err := json.Unmarshal([]byte(itemJSON), &item); err != nil {
			appLogger.Log("[ERROR] Quiet mode failed to unmarshal response: %v", err) // Use logger
			fmt.Fprintf(os.Stderr, "[ERROR] Quiet mode failed to unmarshal response: %v\n", err)
			return
		}

		if item.Type == "message" && item.Message != nil && item.Message.Role == "assistant" {
			// Content in each item is the full message so far.
			finalResponse = item.Message.Content
		}
		// We don't print streamed parts in quiet mode, just collect the final full message.
	}

	_, err := ai.SendMessage(ctx, messages, handler)
	if err != nil {
		appLogger.Log("Error sending message in quiet mode: %v", err) // Use logger
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print final response after the stream completes
	fmt.Println(finalResponse)
	appLogger.Log("Quiet mode finished.") // Use logger
}

// openConfigInEditor opens the instructions file in the user's editor
func openConfigInEditor() {
	// Get config directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Join(homeDir, ".codex")
	instructionsPath := filepath.Join(configDir, "instructions.md")

	// Ensure the directory exists
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Create the file if it doesn't exist
	if _, err := os.Stat(instructionsPath); os.IsNotExist(err) {
		defaultInstructions := `# Codex Instructions

You are a helpful AI assistant designed to help with coding tasks.
You can write code, explain concepts, and help debug issues.

You have access to run commands and edit files on the user's system.
Always explain what you're doing before making changes.
`
		if err := os.WriteFile(instructionsPath, []byte(defaultInstructions), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating instructions file: %v\n", err)
			os.Exit(1)
		}
	}

	// Open the file in the user's editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}

	cmd := exec.Command(editor, instructionsPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error opening editor: %v\n", err)
		os.Exit(1)
	}
}

// viewSavedRollout loads and displays a saved rollout file
func viewSavedRollout(rolloutPath string) {
	appLogger.Log("Viewing rollout: %s", rolloutPath)
	// Load config
	cfg, err := config.Load()
	if err != nil {
		appLogger.Log("Error loading config for viewing rollout: %v", err)
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	// Ensure logging config is set for the view session if needed
	cfg.Debug = appLogger.IsEnabled() // Inherit debug status
	if _, ok := appLogger.(*logging.FileLogger); ok {
		// If we have a file logger, we're already logging to a file
		// For simplicity, the global appLogger is already initialized.
	}

	// Create an app instance
	app, err := NewApp(cfg, appLogger) // Pass logger
	if err != nil {
		appLogger.Log("Error creating app for viewing rollout: %v", err)
		fmt.Fprintf(os.Stderr, "Error creating app: %v\n", err)
		os.Exit(1)
	}

	// Resolve path if not absolute
	if !filepath.IsAbs(rolloutPath) {
		rolloutPath = filepath.Join(cfg.CWD, rolloutPath)
	}

	// Load the rollout file
	if err := app.LoadRollout(rolloutPath); err != nil {
		appLogger.Log("Error loading rollout file %s: %v", rolloutPath, err)
		fmt.Fprintf(os.Stderr, "Error loading rollout: %v\n", err)
		os.Exit(1)
	}

	// Add a system message to indicate this is a view-only session
	app.ChatModel.AddSystemMessage(fmt.Sprintf("Viewing session from %s (read-only)",
		app.CurrentRollout.CreatedAt.Format("Jan 2, 2006 15:04")))

	// Create and run the program in view-only mode
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		appLogger.Log("Error running Bubble Tea program for viewing rollout: %v", err)
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
	appLogger.Log("Finished viewing rollout.")
}

// runInteractiveMode runs the agent in interactive mode
func runInteractiveMode(ai *agent.OpenAIAgent, initialPrompt string, cfg *config.Config, images []string) {
	appLogger.Log("Starting interactive mode...")

	// Create the main application model, passing the logger
	app, err := NewApp(cfg, appLogger)
	if err != nil {
		appLogger.Log("Error creating app for interactive mode: %v", err)
		fmt.Fprintf(os.Stderr, "Error creating app: %v\n", err)
		os.Exit(1)
	}

	// Handle images if provided
	// ... (image handling logic - needs logger integration if errors occur)

	// Create Bubble Tea program
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Start the program
	app.IsRunning = true

	// Done channel to signal when p.Run() finishes
	programDone := make(chan struct{})

	go func() {
		if _, err := p.Run(); err != nil {
			appLogger.Log("Error running Bubble Tea program: %v", err)
			fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		}
		close(programDone)
		appLogger.Log("Bubble Tea p.Run() has completed")
	}()

	// If there's an initial prompt, send it as the first message
	if initialPrompt != "" {
		appLogger.Log("Sending initial prompt: %s", initialPrompt)
		p.Send(ui.UserInputSubmitMsg{Content: initialPrompt})
	}

	// Handle graceful shutdown on signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for either signal or program exit
	select {
	case <-sigChan:
		appLogger.Log("Shutdown signal received.")
		fmt.Println("\nShutting down...")

		// Cancel the agent operations
		if ai != nil {
			appLogger.Log("Cancelling agent operations...")
			ai.Cancel()
		}

		// Call Close on the App to clean up resources
		appLogger.Log("Closing app resources...")
		if err := app.Close(); err != nil {
			appLogger.Log("Error closing app: %v", err)
		}

		// Exit Bubble Tea
		p.Quit()

		// Give a timeout for graceful shutdown
		select {
		case <-programDone:
			appLogger.Log("Program exited gracefully after shutdown signal.")
		case <-time.After(1 * time.Second):
			appLogger.Log("Timeout waiting for program to exit. Forcing shutdown.")
		}

	case <-programDone:
		appLogger.Log("Bubble Tea program exited normally.")
	}

	// Final cleanup
	appLogger.Log("--- Codex-Go Session End ---")
}

// main is the entry point of the application
func main() {
	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		// Cobra already prints the error
		// If logger was initialized, log the final error too
		if appLogger != nil && appLogger.IsEnabled() {
			appLogger.Log("Cobra command execution failed: %v", err)
		} else {
			// Ensure error is printed even if logger failed/disabled
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

// createLatestLogSymlink attempts to create or update the latest.log symlink.
func createLatestLogSymlink(logPath string) {
	if runtime.GOOS == "windows" {
		// Symlinks are tricky on Windows, skip for now.
		return
	}
	logDir := filepath.Dir(logPath)
	linkPath := filepath.Join(logDir, "latest.log")

	// Remove existing link if it exists
	_ = os.Remove(linkPath) // Ignore error if it doesn't exist

	// Create new link
	err := os.Symlink(filepath.Base(logPath), linkPath)
	if err != nil {
		// Log the error but don't fail the application
		if appLogger != nil {
			appLogger.Log("Warning: Failed to create/update latest.log symlink: %v", err)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Failed to create/update latest.log symlink: %v\n", err)
		}
	}
}
