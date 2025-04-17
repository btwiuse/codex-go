package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles for approval UI
var (
	approvalTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("5")).
				MarginBottom(1).
				Width(80)

	approvalDescriptionStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("7")).
					MarginBottom(1).
					Width(80)

	approvalActionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("3")).
				MarginBottom(1).
				Width(80)

	yesButtonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Background(lipgloss.Color("0")).
			Padding(0, 1).
			MarginRight(1)

	noButtonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Background(lipgloss.Color("0")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Underline(true)
)

// ApprovalModel is a bubble tea model for approval prompts
type ApprovalModel struct {
	Title       string
	Description string
	Action      string
	Approved    bool // true = yes, false = no
	Done        bool // When true, the user has made a selection
	YesText     string
	NoText      string
}

// NewApprovalModel creates a new approval model
func NewApprovalModel(title, description, action string) ApprovalModel {
	return ApprovalModel{
		Title:       title,
		Description: description,
		Action:      action,
		Approved:    false, // Default to "no" for safety
		Done:        false,
		YesText:     "Approve",
		NoText:      "Deny",
	}
}

// Init initializes the model
func (m ApprovalModel) Init() tea.Cmd {
	return nil
}

// Update handles updates to the model
func (m ApprovalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "q", "ctrl+c"))):
			m.Done = true
			m.Approved = false
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("left", "h"))):
			m.Approved = true
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("right", "l"))):
			m.Approved = false
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
			m.Done = true
			m.Approved = true
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
			m.Done = true
			m.Approved = false
			return m, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			m.Done = true
			return m, tea.Quit
		}
	}

	return m, nil
}

// View renders the model
func (m ApprovalModel) View() string {
	var sb strings.Builder

	// Title
	sb.WriteString(approvalTitleStyle.Render(m.Title))
	sb.WriteString("\n")

	// Description
	sb.WriteString(approvalDescriptionStyle.Render(m.Description))
	sb.WriteString("\n")

	// Action
	sb.WriteString(approvalActionStyle.Render(m.Action))
	sb.WriteString("\n\n")

	// Buttons
	yes := m.YesText
	no := m.NoText

	if m.Approved {
		yes = selectedStyle.Render(yes)
	} else {
		no = selectedStyle.Render(no)
	}

	sb.WriteString(fmt.Sprintf("%s %s", yesButtonStyle.Render(yes), noButtonStyle.Render(no)))
	sb.WriteString("\n\n")

	// Help
	sb.WriteString("(Use arrow keys to select, Enter to confirm, Esc to cancel)")

	return sb.String()
}

// GetApproval runs the approval UI and returns the result
func GetApproval(title, description, action string) (bool, error) {
	model := NewApprovalModel(title, description, action)

	p := tea.NewProgram(model)
	result, err := p.Run()
	if err != nil {
		return false, fmt.Errorf("error running approval UI: %w", err)
	}

	finalModel, ok := result.(ApprovalModel)
	if !ok {
		return false, fmt.Errorf("unexpected model type: %T", result)
	}

	return finalModel.Approved, nil
}
