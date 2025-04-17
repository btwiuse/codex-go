package internal

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/epuerta/codex-go/internal/ui"
)

// A simple test program for the UI
func RunUITest() {
	// Create UI model
	chatModel := ui.NewChatModel()
	chatModel.SetSessionInfo(
		"test-session",
		"/current/dir",
		"gpt-4o",
		"suggest",
	)

	// Add some test messages
	chatModel.AddUserMessage("Hello, can you help me?")
	chatModel.AddSystemMessage("System test message")
	chatModel.AddAssistantMessage("I'm here to help! What can I do for you today?")

	// Add a function call message
	chatModel.AddFunctionCallMessage("read_file", `{"path": "test.txt"}`)
	chatModel.AddFunctionResultMessage("Sample file contents", false)

	// Add follow-up messages
	chatModel.AddUserMessage("Thanks!")
	chatModel.AddAssistantMessage("You're welcome! Let me know if you need anything else.")

	// Create the program
	p := tea.NewProgram(chatModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
