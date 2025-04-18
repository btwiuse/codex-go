package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ApprovalResultMsg is sent when the user makes a choice in the approval UI
type ApprovalResultMsg struct {
	Approved bool // true if approved, false if denied or cancelled
}

// Styles for approval UI
var (
	approvalTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("5")). // Magenta-ish
				Padding(0, 1).                   // Add some padding
				MarginBottom(1)

	approvalDescriptionStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("7")). // White/Gray
					Padding(0, 1).                   // Add some padding
					MarginBottom(1)

	approvalActionStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("63")). // Light Purple
				Padding(0, 1)

	approvalButtonStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Margin(1, 1) // Add margin around buttons

	approvalButtonActiveStyle = approvalButtonStyle.Copy().
					Foreground(lipgloss.Color("0")). // Black text
					Background(lipgloss.Color("10")) // Green background

	approvalButtonInactiveStyle = approvalButtonStyle.Copy().
					Foreground(lipgloss.Color("244")) // Gray text

	approvalHelpStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")). // Dark Gray
				MarginTop(1)

	approvalDialogStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("6")). // Cyan
				Padding(1)

	// Styles for Diff View
	diffAddedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))  // Green
	diffRemovedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))   // Red
	diffContextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // Gray (for unchanged lines/context)
)

// Key bindings
type approvalKeyMap struct {
	Select   key.Binding
	Confirm  key.Binding
	Cancel   key.Binding
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Approve  key.Binding
	Deny     key.Binding
	Help     key.Binding // Added Help key
}

func defaultApprovalKeyMap() approvalKeyMap {
	return approvalKeyMap{
		Select: key.NewBinding(
			key.WithKeys("left", "right", "h", "l", "tab", "shift+tab"),
			key.WithHelp("←/→/tab", "select"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc", "q", "ctrl+c"),
			key.WithHelp("esc/q", "cancel"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "scroll up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "scroll down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdn", "page down"),
		),
		Approve: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "approve"),
		),
		Deny: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "deny"),
		),
		Help: key.NewBinding( // Added Help key binding
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"), // Simple toggle description
		),
	}
}

// ApprovalModel is a bubble tea model for approval prompts
type ApprovalModel struct {
	Title        string
	Description  string
	Action       string // The *raw* arguments or content being approved
	Approved     bool   // Tracks the currently selected option (true = yes)
	YesText      string
	NoText       string
	keyMap       approvalKeyMap
	showFullHelp bool // Added state for toggling help

	viewport viewport.Model
	ready    bool // Viewport readiness flag
	// Store terminal dimensions for Place function in View
	terminalWidth  int
	terminalHeight int
	// Store calculated dialog dimensions
	dialogWidth  int
	dialogHeight int
}

// NewApprovalModel creates a new approval model
func NewApprovalModel(title, description, action string) ApprovalModel {
	vp := viewport.New(0, 0)                     // Initialize with zero size, will be set later
	vp.Style = lipgloss.NewStyle().MarginLeft(1) // Ensure content doesn't touch scrollbar

	return ApprovalModel{
		Title:        title,
		Description:  description,
		Action:       action,
		Approved:     true, // Default selection to Approve
		YesText:      "Approve",
		NoText:       "Deny",
		keyMap:       defaultApprovalKeyMap(),
		showFullHelp: false, // Start with short help
		viewport:     vp,
		ready:        false,
	}
}

// SetSize calculates layout dimensions based on terminal size
func (m *ApprovalModel) SetSize(termWidth, termHeight int) {
	m.terminalWidth = termWidth
	m.terminalHeight = termHeight

	if !m.ready && termWidth > 0 && termHeight > 0 {
		m.ready = true // Mark as ready once we have dimensions
	}

	// --- Calculate Dialog Box Width ---
	desiredDialogWidth := int(float64(termWidth) * 0.8)
	minDialogWidth := 40
	maxDialogWidth := 120
	dialogW := desiredDialogWidth
	if dialogW < minDialogWidth {
		dialogW = minDialogWidth
	}
	if dialogW > maxDialogWidth {
		dialogW = maxDialogWidth
	}
	if dialogW > termWidth-2 { // Ensure fits with padding
		dialogW = termWidth - 2
	}
	m.dialogWidth = dialogW

	// --- Calculate Viewport Width ---
	vpHorizontalPadding := approvalDialogStyle.GetHorizontalPadding() + approvalActionStyle.GetHorizontalPadding() + m.viewport.Style.GetHorizontalMargins()
	vpWidth := m.dialogWidth - vpHorizontalPadding
	if vpWidth < 0 {
		vpWidth = 0
	}
	m.viewport.Width = vpWidth

	// --- Wrap Content for Height Calculation ---
	wrappedAction := lipgloss.NewStyle().Width(m.viewport.Width).Render(m.Action)
	m.viewport.SetContent(wrappedAction)

	// --- Calculate Non-Viewport Height ---
	titleView := m.renderTitle(vpWidth)
	descView := m.renderDescription(vpWidth)
	buttonsView := m.renderButtons()
	helpView := m.renderHelp(vpWidth)
	nonViewportHeight := lipgloss.Height(titleView) +
		lipgloss.Height(descView) +
		lipgloss.Height(buttonsView) +
		lipgloss.Height(helpView) +
		approvalDialogStyle.GetVerticalPadding() + // Dialog border/padding
		approvalActionStyle.GetVerticalPadding() + // Action box border/padding
		approvalTitleStyle.GetVerticalMargins() + // Margins between elements
		approvalDescriptionStyle.GetVerticalMargins() +
		approvalButtonStyle.GetVerticalMargins()*2 + // Button row margins
		approvalHelpStyle.GetVerticalMargins()

	// --- Calculate Viewport and Dialog Height ---
	// Available height within terminal for the dialog content itself
	maxAvailableHeight := termHeight - approvalDialogStyle.GetVerticalPadding() - 2 // Subtract dialog padding and small buffer
	if maxAvailableHeight < 0 {
		maxAvailableHeight = 0
	}

	// Calculate ideal viewport height based on available space
	idealVpHeight := maxAvailableHeight - nonViewportHeight

	// Apply constraints: min, max, and available space
	minViewportHeight := 3
	maxViewportHeight := 15 // << The requested limit

	finalVpHeight := idealVpHeight // Start with ideal
	if finalVpHeight < minViewportHeight {
		finalVpHeight = minViewportHeight
	}
	if finalVpHeight > maxViewportHeight {
		finalVpHeight = maxViewportHeight
	}

	m.viewport.Height = finalVpHeight

	// Calculate final dialog height based on the *constrained* viewport height
	m.dialogHeight = nonViewportHeight + m.viewport.Height

	// Ensure final dialog height still fits within the absolute max available height
	if m.dialogHeight > maxAvailableHeight {
		m.dialogHeight = maxAvailableHeight
		// If we capped the dialog height, we might need to shrink the viewport *again*
		// This edge case happens if nonViewportHeight is very large
		newVpHeight := m.dialogHeight - nonViewportHeight
		if newVpHeight < minViewportHeight { // Ensure min is still respected
			newVpHeight = minViewportHeight
		}
		m.viewport.Height = newVpHeight
	}

	// Ensure viewport content is set after final dimensions
	m.viewport.SetContent(wrappedAction)
}

// renderTitle renders the title, wrapped to width
func (m ApprovalModel) renderTitle(maxWidth int) string {
	style := approvalTitleStyle.Copy().Width(maxWidth)
	return style.Render(m.Title)
}

// renderDescription renders the description, wrapped to width
func (m ApprovalModel) renderDescription(maxWidth int) string {
	style := approvalDescriptionStyle.Copy().Width(maxWidth)
	return style.Render(m.Description)
}

// Init initializes the model
func (m ApprovalModel) Init() tea.Cmd {
	return nil // No initial command needed
}

// Update handles updates to the model
func (m ApprovalModel) Update(msg tea.Msg) (ApprovalModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	// Ensure model is ready before processing inputs
	if !m.ready {
		if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
			m.SetSize(sizeMsg.Width, sizeMsg.Height)
		}
		// Ignore other messages until ready
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)

	case tea.KeyMsg:
		// Give viewport priority for scrolling keys if content overflows
		contentOverflows := m.viewport.TotalLineCount() > m.viewport.Height
		isScrollingKey := key.Matches(msg, m.keyMap.Up) || key.Matches(msg, m.keyMap.Down) || key.Matches(msg, m.keyMap.PageUp) || key.Matches(msg, m.keyMap.PageDown)

		if contentOverflows && isScrollingKey {
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		} else {
			// Handle non-scrolling keys or if content fits
			switch {
			case key.Matches(msg, m.keyMap.Select):
				m.Approved = !m.Approved // Toggle selection

			case key.Matches(msg, m.keyMap.Confirm):
				cmds = append(cmds, func() tea.Msg { return ApprovalResultMsg{Approved: m.Approved} })
			case key.Matches(msg, m.keyMap.Approve):
				m.Approved = true
				cmds = append(cmds, func() tea.Msg { return ApprovalResultMsg{Approved: true} })
			case key.Matches(msg, m.keyMap.Deny):
				m.Approved = false
				cmds = append(cmds, func() tea.Msg { return ApprovalResultMsg{Approved: false} })

			case key.Matches(msg, m.keyMap.Cancel):
				m.Approved = false // Treat cancel as denial for simplicity
				cmds = append(cmds, func() tea.Msg { return ApprovalResultMsg{Approved: false} })

			case key.Matches(msg, m.keyMap.Help):
				m.showFullHelp = !m.showFullHelp
				// Recalculate layout as help height might change
				m.SetSize(m.terminalWidth, m.terminalHeight)
			}
		}

	// Pass mouse events to the viewport for potential scrolling
	case tea.MouseMsg:
		if m.viewport.TotalLineCount() > m.viewport.Height {
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Ensure viewport updates are applied if triggered by mouse or other means
	// We already handled specific keys, this handles general updates
	// m.viewport, cmd = m.viewport.Update(msg) // This might double-process keys, avoid it unless needed
	// cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// renderButtons renders the Approve/Deny buttons
func (m ApprovalModel) renderButtons() string {
	yesStyle := approvalButtonInactiveStyle
	noStyle := approvalButtonInactiveStyle

	if m.Approved {
		yesStyle = approvalButtonActiveStyle
	} else {
		noStyle = approvalButtonActiveStyle
	}

	yes := yesStyle.Render(m.YesText)
	no := noStyle.Render(m.NoText)

	// Join buttons side-by-side, centered within available space
	// Use dialogWidth for centering context if needed, but simple join is usually fine
	return lipgloss.JoinHorizontal(lipgloss.Center, yes, no)
}

// renderHelp builds and renders the help string
func (m ApprovalModel) renderHelp(maxWidth int) string {
	// Base keys available always
	keys := []key.Binding{m.keyMap.Select, m.keyMap.Confirm, m.keyMap.Approve, m.keyMap.Deny, m.keyMap.Cancel, m.keyMap.Help}

	// Add scrolling keys if content overflows
	if m.viewport.TotalLineCount() > m.viewport.Height {
		keys = append(keys, m.keyMap.Up, m.keyMap.Down, m.keyMap.PageUp, m.keyMap.PageDown)
	}

	// Build help string manually
	var helpBuilder strings.Builder
	activeKeys := 0 // Track how many keys are actually added to the builder
	for _, k := range keys {
		// Use FullHelp if toggled, otherwise ShortHelp
		helpMsg := ""
		if m.showFullHelp {
			// Access help details directly from the binding
			helpMsg = fmt.Sprintf("%s: %s", k.Help().Key, k.Help().Desc)
		} else {
			helpMsg = k.Help().Key // Only show the key in short help
		}

		// Hide Approve/Deny keys from short help to avoid clutter
		// Compare primary key representation for equality check
		isApproveKey := k.Keys()[0] == m.keyMap.Approve.Keys()[0] // Assuming first key is representative
		isDenyKey := k.Keys()[0] == m.keyMap.Deny.Keys()[0]
		if !m.showFullHelp && (isApproveKey || isDenyKey) {
			continue
		}

		// Add separator if not the first item being added
		if activeKeys > 0 {
			helpBuilder.WriteString(" • ")
		}
		helpBuilder.WriteString(helpMsg)
		activeKeys++
	}

	// Apply style and wrap
	style := approvalHelpStyle.Copy().Width(maxWidth)
	return style.Render(helpBuilder.String())
}

// View renders the approval UI
func (m ApprovalModel) View() string {
	if !m.ready {
		// Return empty string or minimal message until ready
		// Using Place requires dimensions, so wait until SetSize is called
		return ""
	}

	// Use calculated dialog width for rendering internal elements
	contentWidth := m.viewport.Width // Width available inside action box border/padding

	titleView := m.renderTitle(contentWidth)
	descView := m.renderDescription(contentWidth)
	actionView := approvalActionStyle.
		Width(m.viewport.Width).   // Use viewport width for the action box style
		Height(m.viewport.Height). // Use viewport height for the action box style
		Render(m.viewport.View())  // Render the viewport content
	buttonsView := m.renderButtons()
	helpView := m.renderHelp(contentWidth) // Render help within content width

	// Combine elements vertically
	ui := lipgloss.JoinVertical(lipgloss.Left,
		titleView,
		descView,
		actionView, // Render the styled viewport
		buttonsView,
		helpView,
	)

	// Apply dialog styling with calculated width and height
	// Subtract padding *before* rendering content inside
	dialogContentWidth := m.dialogWidth - approvalDialogStyle.GetHorizontalPadding()
	// Height calculation is complex due to wrapping; let Render handle it, or use MaxHeight
	dialogView := approvalDialogStyle.
		Width(dialogContentWidth). // Set width for the box itself
		// MaxHeight(m.dialogHeight - approvalDialogStyle.GetVerticalPadding()). // Optional: constrain height
		Render(ui)

	// Center the dialog in the terminal using stored terminal dimensions
	return lipgloss.Place(m.terminalWidth, m.terminalHeight, lipgloss.Center, lipgloss.Center, dialogView)
}
