package ui

import (
	"strings"
)

// FormatPatchForDisplay takes a raw patch string (potentially multi-file)
// from the agent's custom format and attempts to add standard +/- diff markers
// and color highlighting for better readability in the approval UI.
func FormatPatchForDisplay(rawPatch string) string {
	lines := strings.Split(rawPatch, "\n") // Split by newline

	var formatted strings.Builder
	var inEditBlock bool = false // Track if we are inside an ADD/DEL block

	for _, line := range lines {
		// Preserve empty lines within the block, but trim others for prefix checks
		isEmptyLine := len(strings.TrimSpace(line)) == 0
		trimmedLine := ""
		if !isEmptyLine {
			trimmedLine = strings.TrimSpace(line)
		}

		// Handle block markers (Keep default style)
		if strings.HasPrefix(trimmedLine, "// FILE:") || strings.HasPrefix(trimmedLine, "// EDIT:") {
			inEditBlock = strings.HasPrefix(trimmedLine, "// EDIT:")
			formatted.WriteString(line + "\n")
			continue
		}
		if strings.HasPrefix(trimmedLine, "// END_EDIT") {
			inEditBlock = false
			formatted.WriteString(line + "\n")
			continue
		}

		// Process lines within an edit block
		if inEditBlock {
			if strings.HasPrefix(trimmedLine, "ADD:") {
				content := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "ADD:"))
				formatted.WriteString(diffAddedStyle.Render("+ "+content) + "\n")
			} else if strings.HasPrefix(trimmedLine, "DEL:") || strings.HasPrefix(trimmedLine, "DELETE:") {
				content := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "DEL:"))
				if strings.HasPrefix(trimmedLine, "DELETE:") { // Handle both DEL and DELETE
					content = strings.TrimSpace(strings.TrimPrefix(trimmedLine, "DELETE:"))
				}
				formatted.WriteString(diffRemovedStyle.Render("- "+content) + "\n")
			} else {
				// Render context lines within the edit block with context style
				// Keep original leading/trailing whitespace for context lines if possible?
				// For simplicity, just prefix with two spaces for now.
				formatted.WriteString(diffContextStyle.Render("  "+line) + "\n")
			}
		} else {
			// Lines outside edit blocks are treated as metadata or ignored context
			// Render them with default/context style?
			formatted.WriteString(line + "\n") // Keep original styling
		}
	}

	return formatted.String()
}
