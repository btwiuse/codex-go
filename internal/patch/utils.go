package patch

import (
	"fmt"
	"regexp"
	"strings"
)

// ParseSimplePatch parses a simplified patch format that's easier for LLMs to generate
// It handles a simple format like:
// ```
// *** Begin Patch
// *** Add File: path/to/new/file.go
// + package main
// +
// + func main() {
// +     fmt.Println("Hello, world!")
// + }
// *** Update File: path/to/existing/file.go
// Context line 1
// Context line 2
// - line to remove
// + line to add
// Context line 3
// *** Delete File: path/to/unwanted/file.go
// *** End Patch
// ```
func ParseSimplePatch(patchText string) ([]*PatchOperation, error) {
	// Define regex patterns for parsing
	patchStartRegex := regexp.MustCompile(`^\*\*\* Begin Patch\s*$`)
	updateFileRegex := regexp.MustCompile(`^\*\*\* Update File:\s+(.+)\s*$`)
	addFileRegex := regexp.MustCompile(`^\*\*\* Add File:\s+(.+)\s*$`)
	deleteFileRegex := regexp.MustCompile(`^\*\*\* Delete File:\s+(.+)\s*$`)
	endRegex := regexp.MustCompile(`^\*\*\* End Patch\s*$`)
	addLineRegex := regexp.MustCompile(`^\+\s(.*)$`)
	delLineRegex := regexp.MustCompile(`^-\s(.*)$`)

	// Split text into lines
	lines := strings.Split(strings.TrimSpace(patchText), "\n")

	// Check if it starts with Begin Patch
	if len(lines) == 0 || !patchStartRegex.MatchString(lines[0]) {
		return nil, fmt.Errorf("patch must start with '*** Begin Patch'")
	}

	// Check if it ends with End Patch
	if len(lines) < 2 || !endRegex.MatchString(lines[len(lines)-1]) {
		return nil, fmt.Errorf("patch must end with '*** End Patch'")
	}

	// Process the lines and create operations
	var operations []*PatchOperation
	var currentOp *PatchOperation

	// Skip first line (Begin Patch) and last line (End Patch)
	for i := 1; i < len(lines)-1; i++ {
		line := lines[i]

		// Check for operation headers
		if matches := updateFileRegex.FindStringSubmatch(line); len(matches) > 1 {
			// Finish previous operation if any
			if currentOp != nil {
				operations = append(operations, currentOp)
			}

			// Start new update operation
			currentOp = &PatchOperation{
				Type: "update",
				Path: matches[1],
				// These will be filled in as we process lines
				Context:  make([]string, 0),
				AddLines: make([]string, 0),
				DelLines: make([]string, 0),
			}
			continue
		}

		if matches := addFileRegex.FindStringSubmatch(line); len(matches) > 1 {
			// Finish previous operation if any
			if currentOp != nil {
				operations = append(operations, currentOp)
			}

			// Start new add operation
			currentOp = &PatchOperation{
				Type:    "add",
				Path:    matches[1],
				Content: "",
			}
			continue
		}

		if matches := deleteFileRegex.FindStringSubmatch(line); len(matches) > 1 {
			// Finish previous operation if any
			if currentOp != nil {
				operations = append(operations, currentOp)
			}

			// Add delete operation directly
			operations = append(operations, &PatchOperation{
				Type: "delete",
				Path: matches[1],
			})

			// Reset current operation
			currentOp = nil
			continue
		}

		// Process content based on the current operation
		if currentOp == nil {
			continue // Skip lines not in an operation
		}

		// Handle content based on operation type
		switch currentOp.Type {
		case "add":
			if matches := addLineRegex.FindStringSubmatch(line); len(matches) > 0 {
				// Add file content (extracted from regex group)
				content := matches[1]
				if currentOp.Content == "" {
					currentOp.Content = content
				} else {
					currentOp.Content += "\n" + content
				}
			}

		case "update":
			if matches := addLineRegex.FindStringSubmatch(line); len(matches) > 0 {
				// Added line (extracted from regex group)
				currentOp.AddLines = append(currentOp.AddLines, matches[1])
			} else if matches := delLineRegex.FindStringSubmatch(line); len(matches) > 0 {
				// Deleted line (extracted from regex group)
				currentOp.DelLines = append(currentOp.DelLines, matches[1])
			} else {
				// Context line
				currentOp.Context = append(currentOp.Context, line)
			}
		}
	}

	// Add the last operation if any
	if currentOp != nil {
		operations = append(operations, currentOp)
	}

	return operations, nil
}

// PatchOperation represents a simplified patch operation
// This is used as an intermediate representation that can be converted to the more powerful
// format used by the robust patching system
type PatchOperation struct {
	Type     string   // "update", "add", "delete", "move"
	Path     string   // Path to the file
	Content  string   // Content for the file or hunk
	Context  []string // Context lines for fuzzy matching
	AddLines []string // Lines to add
	DelLines []string // Lines to delete
	MoveTo   string   // Path to move the file to (for move operations)
}

// ConvertToCustomPatchFormat converts a slice of PatchOperation to the robust patch format
func ConvertToCustomPatchFormat(operations []*PatchOperation) string {
	var sb strings.Builder

	sb.WriteString(PatchBeginMarker + "\n")

	for _, op := range operations {
		switch op.Type {
		case "add":
			sb.WriteString(AddFilePrefix + op.Path + "\n")
			for _, line := range strings.Split(op.Content, "\n") {
				sb.WriteString("+" + line + "\n")
			}

		case "delete":
			sb.WriteString(DeleteFilePrefix + op.Path + "\n")

		case "update":
			sb.WriteString(UpdateFilePrefix + op.Path + "\n")

			// Write context and modification lines
			// We'll need to interleave them to preserve order
			for _, line := range op.Context {
				sb.WriteString(" " + line + "\n")
			}

			for i := 0; i < len(op.DelLines); i++ {
				sb.WriteString("-" + op.DelLines[i] + "\n")
			}

			for i := 0; i < len(op.AddLines); i++ {
				sb.WriteString("+" + op.AddLines[i] + "\n")
			}

			sb.WriteString(EndOfFileMarker + "\n")
		}
	}

	sb.WriteString(PatchEndMarker + "\n")

	return sb.String()
}

// GetLinesAddedDeleted counts how many lines were added and deleted
func GetLinesAddedDeleted(results []*PatchResult) (int, int) {
	added := 0
	deleted := 0

	for _, result := range results {
		added += result.LineStats.Added
		if result.OperationType == "delete" {
			deleted += result.LineStats.Original
		} else if result.OperationType == "update" {
			deleted += result.LineStats.Original - result.LineStats.New
		}
	}

	return added, deleted
}
