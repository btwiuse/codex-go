package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/config"
	"github.com/epuerta/codex-go/internal/ui"
	"github.com/spf13/cobra"
)

var (
	// Version is set during build
	Version = "dev"
	// GitCommit is set during build
	GitCommit = "none"
	// BuildDate is set during build
	BuildDate = "unknown"
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
	// Add global flags
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
	// Add support for view rollout flag
	rootCmd.PersistentFlags().StringP("view", "v", "", "Inspect a previously saved rollout instead of starting a session")

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

	// Check if we need to open the config
	if configFlag {
		openConfigInEditor()
		return
	}

	// Check if we're viewing a rollout
	if viewRollout != "" {
		viewSavedRollout(viewRollout)
		return
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override config with flags
	if model != "" {
		cfg.Model = model
	}

	// Set approval mode based on flags in order of priority
	if dangerouslyAutoApprove {
		// This is the most dangerous mode, so we set it explicitly
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

	// Create agent
	ai, err := agent.NewOpenAIAgent(cfg)
	if err != nil {
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
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
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

	handler := func(item agent.ResponseItem) {
		if item.Type == "message" && item.Message != nil && item.Message.Role == "assistant" {
			// The content in each item is the *full* message so far
			finalResponse = item.Message.Content
		}
		// We don't print anything here, just collect the last full message
	}

	if err := ai.SendMessage(ctx, messages, handler); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print final response after the stream completes
	fmt.Println(finalResponse)
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
	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Create an app instance
	app, err := NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating app: %v\n", err)
		os.Exit(1)
	}

	// Resolve path if not absolute
	if !filepath.IsAbs(rolloutPath) {
		rolloutPath = filepath.Join(cfg.CWD, rolloutPath)
	}

	// Load the rollout file
	if err := app.LoadRollout(rolloutPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading rollout: %v\n", err)
		os.Exit(1)
	}

	// Add a system message to indicate this is a view-only session
	app.ChatModel.AddSystemMessage(fmt.Sprintf("Viewing session from %s (read-only)",
		app.CurrentRollout.CreatedAt.Format("Jan 2, 2006 15:04")))

	// Create and run the program in view-only mode
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}

// runInteractiveMode runs the agent in interactive mode
func runInteractiveMode(ai *agent.OpenAIAgent, initialPrompt string, cfg *config.Config, images []string) {
	// Create the application instance which is now the main model
	// Note: This uses the NewApp function defined in app.go
	app, err := NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating app: %v\n", err)
		os.Exit(1)
	}

	// Add a welcome message
	app.ChatModel.AddSystemMessage("Welcome to Codex! Type a message to begin.")

	// Create the program using the App model
	p := tea.NewProgram(app, tea.WithAltScreen())

	// Handle initial prompt and images if provided
	if initialPrompt != "" || len(images) > 0 {
		// Add the user message visually first
		if initialPrompt != "" {
			app.ChatModel.AddUserMessage(initialPrompt)
		}

		// Send the initial prompt message via a command after the program starts
		go func() {
			// TODO: Implement image handling
			if initialPrompt != "" {
				p.Send(ui.SendMessageCmd{Content: initialPrompt})
			}
		}()
	}

	// Start the program
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	// Check for OpenAI API key
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("Error: OPENAI_API_KEY environment variable is not set")
		fmt.Println("Please set your OpenAI API key: export OPENAI_API_KEY=your-api-key-here")
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
