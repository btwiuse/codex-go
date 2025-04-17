package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CustomTextInput is a text input component that supports multiline text input
type CustomTextInput struct {
	textInput    textinput.Model
	value        string
	width        int
	height       int
	cursorPos    int
	prefix       string
	placeholder  string
	focused      bool
	showCursor   bool
	style        lipgloss.Style
	prefixStyle  lipgloss.Style
	cursorStyle  lipgloss.Style
	blurredStyle lipgloss.Style
}

// NewCustomTextInput creates a new custom text input
func NewCustomTextInput() CustomTextInput {
	ti := textinput.New()
	ti.Placeholder = "Type your message..."
	ti.Focus()
	ti.CharLimit = 4096
	ti.Width = 80

	return CustomTextInput{
		textInput:   ti,
		value:       "",
		cursorPos:   0,
		prefix:      "user",
		placeholder: "Send a message or press tab to select a suggestion",
		focused:     true,
		showCursor:  true,
		style: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("7")).
			Padding(0, 1),
		prefixStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true),
		cursorStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Underline(true),
		blurredStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),
	}
}

// Init initializes the model
func (m CustomTextInput) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages for the model
func (m CustomTextInput) Update(msg tea.Msg) (CustomTextInput, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			// Submit the value
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	m.textInput, cmd = m.textInput.Update(msg)
	m.value = m.textInput.Value()
	return m, cmd
}

// View renders the model
func (m CustomTextInput) View() string {
	if !m.focused {
		return m.blurredStyle.Render(m.placeholder)
	}

	// Render the cursor differently with our styling
	cursor := "█"
	if !m.showCursor {
		cursor = " "
	}

	// Format as "user: "
	prefix := m.prefixStyle.Render(m.prefix)

	// Only show cursor if there's no content
	if m.value == "" {
		return fmt.Sprintf("%s %s", prefix, cursor)
	}

	// Show the text with cursor
	return fmt.Sprintf("%s %s", prefix, m.value)
}

// Focus focuses the model
func (m *CustomTextInput) Focus() {
	m.focused = true
	m.textInput.Focus()
}

// Blur blurs the model
func (m *CustomTextInput) Blur() {
	m.focused = false
	m.textInput.Blur()
}

// SetValue sets the value of the model
func (m *CustomTextInput) SetValue(value string) {
	m.value = value
	m.textInput.SetValue(value)
}

// Value returns the current value of the model
func (m CustomTextInput) Value() string {
	return m.value
}

// SetPlaceholder sets the placeholder text
func (m *CustomTextInput) SetPlaceholder(placeholder string) {
	m.placeholder = placeholder
	m.textInput.Placeholder = placeholder
}

// SetWidth sets the width of the input field
func (m *CustomTextInput) SetWidth(width int) {
	m.width = width
	m.textInput.Width = width
}

// SetPrefix sets the prefix text
func (m *CustomTextInput) SetPrefix(prefix string) {
	m.prefix = prefix
}
