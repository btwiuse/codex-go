package fileops

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AgentPatchOperation represents a single operation derived from the custom agent format
type AgentPatchOperation struct {
	Type    string // "add" or "remove"
	Path    string // Path to the file
	Content string // Content to add or remove (without ADD:/DEL: prefix)
	// Note: Line numbers are not directly available in this format
}

// ParseAgentPatch parses the agent's specific patch format.
// It looks for // FILE:, // EDIT:, // END_EDIT, ADD:, and DEL: markers.
func ParseAgentPatch(patchContent string) ([]AgentPatchOperation, error) {
	var operations []AgentPatchOperation
	lines := strings.Split(patchContent, "\n")

	currentFile := ""
	inEditBlock := false
	var fileParseError error

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "// FILE:") {
			currentFile = strings.TrimSpace(strings.TrimPrefix(trimmedLine, "// FILE:"))
			if currentFile == "" {
				fileParseError = fmt.Errorf("found '// FILE:' marker with no filename")
			}
			inEditBlock = false
			continue
		}

		if strings.HasPrefix(trimmedLine, "// EDIT:") {
			if currentFile == "" {
				fileParseError = fmt.Errorf("found '// EDIT:' marker before '// FILE:' marker")
			}
			inEditBlock = true
			continue
		}

		if strings.HasPrefix(trimmedLine, "// END_EDIT") {
			inEditBlock = false
			continue
		}

		if inEditBlock && currentFile != "" {
			if strings.HasPrefix(line, "ADD:") {
				content := strings.TrimPrefix(line, "ADD:")
				// Remove potential leading space after prefix
				content = strings.TrimPrefix(content, " ")
				operations = append(operations, AgentPatchOperation{
					Type:    "add",
					Path:    currentFile,
					Content: content,
				})
			} else if strings.HasPrefix(line, "DEL:") {
				content := strings.TrimPrefix(line, "DEL:")
				// Remove potential leading space after prefix
				content = strings.TrimPrefix(content, " ")
				operations = append(operations, AgentPatchOperation{
					Type:    "remove",
					Path:    currentFile,
					Content: content,
				})
			}
		}
	}

	return operations, fileParseError
}

// ApplyAgentPatch applies a series of custom agent patch operations.
// This version attempts to remove lines based on content match (ignoring leading/trailing space)
// and appends added lines.
func ApplyAgentPatch(operations []AgentPatchOperation) ([]*AgentPatchResult, error) {
	var results []*AgentPatchResult
	var overallError error
	opsByFile := make(map[string][]AgentPatchOperation)
	for _, op := range operations {
		opsByFile[op.Path] = append(opsByFile[op.Path], op)
	}

	for path, ops := range opsByFile {
		result := &AgentPatchResult{Path: path, Success: false} // Default to failure
		results = append(results, result)

		// ----------------- Start Revised Logic -----------------

		// 1. Collect lines to delete and lines to add
		linesToDelete := make(map[string]bool)
		var linesToAdd []string
		deleteOpCount := 0 // Keep track of DEL operations for reporting
		addOpCount := 0
		for _, op := range ops {
			if op.Type == "remove" {
				// Split multi-line content into individual lines for deletion map
				for _, lineToDelete := range strings.Split(op.Content, "\n") {
					trimmedLine := strings.TrimSpace(lineToDelete)
					if trimmedLine != "" { // Avoid adding empty lines from blank DEL blocks
						linesToDelete[trimmedLine] = true
					}
				}
				deleteOpCount++
			} else if op.Type == "add" {
				linesToAdd = append(linesToAdd, op.Content)
				addOpCount++
			}
		}

		// 2. Read original file (handle potential creation)
		contentBytes, readErr := ioutil.ReadFile(path)
		isNotExist := os.IsNotExist(readErr)

		if readErr != nil && !isNotExist {
			result.Error = fmt.Errorf("failed to read file %s: %w", path, readErr)
			if overallError == nil {
				overallError = result.Error
			}
			continue // Skip to next file
		}

		// Check if we should create the file
		shouldCreate := isNotExist && addOpCount > 0
		if isNotExist && !shouldCreate {
			// File doesn't exist, and we aren't adding anything, so it's an error if trying to delete
			if deleteOpCount > 0 {
				result.Error = fmt.Errorf("file %s does not exist and cannot apply deletions", path)
				if overallError == nil {
					overallError = result.Error
				}
			} else {
				// No error, but nothing to do
				result.Success = true
				result.Diff = "File does not exist, no operation performed."
			}
			continue // Skip to next file
		}

		var originalLines []string
		if !isNotExist {
			originalLines = strings.Split(string(contentBytes), "\n")
		}
		result.OriginalLines = len(originalLines)

		// 3. Build new content excluding deleted lines
		modifiedLines := make([]string, 0, len(originalLines))
		actualDeletions := 0
		for _, line := range originalLines {
			if !linesToDelete[strings.TrimSpace(line)] {
				modifiedLines = append(modifiedLines, line) // Keep the original line
			} else {
				actualDeletions++
			}
		}

		// 4. Append added lines
		modifiedLines = append(modifiedLines, linesToAdd...)

		// 5. Check if changes were actually made
		linesWereModified := (actualDeletions > 0) || (addOpCount > 0) || shouldCreate

		if linesWereModified {
			newContent := strings.Join(modifiedLines, "\n")
			// Ensure directory exists if creating file
			if shouldCreate {
				dir := filepath.Dir(path)
				if err := os.MkdirAll(dir, 0755); err != nil {
					result.Error = fmt.Errorf("failed to create directory for %s: %w", path, err)
					if overallError == nil {
						overallError = result.Error
					}
					continue // Skip to next file
				}
			}
			// Write the file
			if err := ioutil.WriteFile(path, []byte(newContent), 0644); err != nil {
				result.Error = fmt.Errorf("failed to write changes to file %s: %w", path, err)
				if overallError == nil {
					overallError = result.Error
				}
				continue // Skip to next file
			}
			result.Success = true
			result.NewLines = len(modifiedLines)
			result.Diff = fmt.Sprintf("Applied +%d/-%d lines.", addOpCount, actualDeletions)
		} else {
			result.Success = true
			result.Diff = "No effective changes applied."
			result.NewLines = len(originalLines)
		}
		// ----------------- End Revised Logic -----------------
	}

	return results, overallError
}

// Helper to check if any operation implies file creation for the agent patch format
func shouldCreateFileForAgentPatch(ops []AgentPatchOperation) bool {
	for _, op := range ops {
		if op.Type == "add" {
			return true
		}
	}
	return false
}

// PatchOperation represents a single patch operation
type PatchOperation struct {
	Type      string // "add", "remove", "replace"
	Path      string // Path to the file
	Content   string // Content to add or replace with
	StartLine int    // Start line for the operation (1-indexed)
	EndLine   int    // End line for the operation (1-indexed)
}

// PatchResult represents the result of applying a patch
type PatchResult struct {
	Success       bool
	Error         error
	Path          string
	OriginalLines int
	NewLines      int
	Diff          string
}

// ApplyPatch applies a patch operation to a file
func ApplyPatch(op PatchOperation) (*PatchResult, error) {
	// Ensure the file exists or create it if adding new content
	if op.Type == "add" && !fileExists(op.Path) {
		// Ensure directory exists
		dir := filepath.Dir(op.Path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// Create empty file
		if err := ioutil.WriteFile(op.Path, []byte{}, 0644); err != nil {
			return nil, fmt.Errorf("failed to create file %s: %w", op.Path, err)
		}
	} else if !fileExists(op.Path) {
		return nil, fmt.Errorf("file not found: %s", op.Path)
	}

	// Read the file content
	content, err := ioutil.ReadFile(op.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", op.Path, err)
	}

	// Split into lines
	lines := strings.Split(string(content), "\n")
	originalLines := len(lines)

	// Apply the patch based on type
	var newContent string
	var diff string

	switch op.Type {
	case "add":
		newContent, diff = addContent(lines, op)
	case "remove":
		newContent, diff = removeContent(lines, op)
	case "replace":
		newContent, diff = replaceContent(lines, op)
	default:
		return nil, fmt.Errorf("unknown patch operation type: %s", op.Type)
	}

	// Write the new content back to the file
	if err := ioutil.WriteFile(op.Path, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write to file %s: %w", op.Path, err)
	}

	// Calculate new line count
	newLines := len(strings.Split(newContent, "\n"))

	return &PatchResult{
		Success:       true,
		Path:          op.Path,
		OriginalLines: originalLines,
		NewLines:      newLines,
		Diff:          diff,
	}, nil
}

// ParseUnifiedDiff parses a unified diff format and converts it to patch operations
func ParseUnifiedDiff(diff string, basePath string) ([]PatchOperation, error) {
	var operations []PatchOperation

	// Regex to match file headers in unified diff
	fileHeaderRegex := regexp.MustCompile(`^--- (a/)?(.+?)\s.*\n\+\+\+ (b/)?(.+?)(\s.*)?$`)

	// Regex to match hunks in unified diff
	hunkHeaderRegex := regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

	// Split diff into sections by file
	lines := strings.Split(diff, "\n")
	currentFile := ""
	inHunk := false
	srcStart := 0
	dstStart := 0
	hunkContent := []string{}

	for i, line := range lines {
		// Check for file header
		if fileHeaderMatch := fileHeaderRegex.FindStringSubmatch(line); fileHeaderMatch != nil && i+1 < len(lines) {
			// Process previous hunk if any
			if inHunk && len(hunkContent) > 0 {
				op := processHunk(hunkContent, srcStart, dstStart, currentFile)
				operations = append(operations, op)
				hunkContent = []string{}
			}

			// Get the new file path
			currentFile = filepath.Join(basePath, fileHeaderMatch[4])
			inHunk = false
			continue
		}

		// Check for hunk header
		if hunkHeaderMatch := hunkHeaderRegex.FindStringSubmatch(line); hunkHeaderMatch != nil {
			// Process previous hunk if any
			if inHunk && len(hunkContent) > 0 {
				op := processHunk(hunkContent, srcStart, dstStart, currentFile)
				operations = append(operations, op)
				hunkContent = []string{}
			}

			// Parse hunk header numbers
			srcStart = atoi(hunkHeaderMatch[1])
			dstStart = atoi(hunkHeaderMatch[3])

			inHunk = true
			continue
		}

		// Collect hunk content
		if inHunk {
			hunkContent = append(hunkContent, line)
		}
	}

	// Process the last hunk if any
	if inHunk && len(hunkContent) > 0 {
		op := processHunk(hunkContent, srcStart, dstStart, currentFile)
		operations = append(operations, op)
	}

	return operations, nil
}

// Helper function to process a hunk and convert it to a patch operation
func processHunk(hunkContent []string, srcStart, dstStart int, filePath string) PatchOperation {
	var addedContent []string
	var removedContent []string
	var contextBefore, contextAfter []string
	inAddition := false
	inRemoval := false

	for _, line := range hunkContent {
		if len(line) == 0 {
			continue
		}

		switch line[0] {
		case '+':
			addedContent = append(addedContent, line[1:])
			inAddition = true
		case '-':
			removedContent = append(removedContent, line[1:])
			inRemoval = true
		default:
			if !inAddition && !inRemoval {
				contextBefore = append(contextBefore, line[1:])
			} else {
				contextAfter = append(contextAfter, line[1:])
			}
		}
	}

	// Determine operation type
	var opType string
	var content string
	var startLine, endLine int

	if len(removedContent) > 0 && len(addedContent) > 0 {
		// Replace operation
		opType = "replace"
		content = strings.Join(addedContent, "\n")
		startLine = srcStart
		endLine = srcStart + len(removedContent) - 1
	} else if len(removedContent) > 0 {
		// Remove operation
		opType = "remove"
		content = ""
		startLine = srcStart
		endLine = srcStart + len(removedContent) - 1
	} else {
		// Add operation
		opType = "add"
		content = strings.Join(addedContent, "\n")
		startLine = dstStart
		endLine = dstStart
	}

	return PatchOperation{
		Type:      opType,
		Path:      filePath,
		Content:   content,
		StartLine: startLine,
		EndLine:   endLine,
	}
}

// Helper function to add content to lines
func addContent(lines []string, op PatchOperation) (string, string) {
	// Adjust for 1-indexed to 0-indexed
	insertLine := op.StartLine - 1
	if insertLine < 0 {
		insertLine = 0
	}
	if insertLine > len(lines) {
		insertLine = len(lines)
	}

	// Insert the content
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertLine]...)
	newLines = append(newLines, op.Content)
	newLines = append(newLines, lines[insertLine:]...)

	// Create a diff representation
	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("--- %s (original)\n", op.Path))
	diff.WriteString(fmt.Sprintf("+++ %s (modified)\n", op.Path))
	diff.WriteString(fmt.Sprintf("@@ -%d,0 +%d,%d @@\n", insertLine+1, insertLine+1, 1))
	diff.WriteString("+ " + op.Content + "\n")

	return strings.Join(newLines, "\n"), diff.String()
}

// Helper function to remove content from lines
func removeContent(lines []string, op PatchOperation) (string, string) {
	// Adjust for 1-indexed to 0-indexed
	startLine := op.StartLine - 1
	endLine := op.EndLine - 1

	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}
	if startLine > endLine {
		return strings.Join(lines, "\n"), ""
	}

	// Remove the lines
	newLines := make([]string, 0, len(lines)-(endLine-startLine+1))
	newLines = append(newLines, lines[:startLine]...)
	newLines = append(newLines, lines[endLine+1:]...)

	// Create a diff representation
	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("--- %s (original)\n", op.Path))
	diff.WriteString(fmt.Sprintf("+++ %s (modified)\n", op.Path))
	diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,0 @@\n", startLine+1, endLine-startLine+1, startLine+1))
	for i := startLine; i <= endLine; i++ {
		diff.WriteString("- " + lines[i] + "\n")
	}

	return strings.Join(newLines, "\n"), diff.String()
}

// Helper function to replace content in lines
func replaceContent(lines []string, op PatchOperation) (string, string) {
	// Adjust for 1-indexed to 0-indexed
	startLine := op.StartLine - 1
	endLine := op.EndLine - 1

	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}
	if startLine > endLine {
		return strings.Join(lines, "\n"), ""
	}

	// Replace the lines
	replacementLines := strings.Split(op.Content, "\n")
	newLines := make([]string, 0, len(lines)-(endLine-startLine+1)+len(replacementLines))
	newLines = append(newLines, lines[:startLine]...)
	newLines = append(newLines, replacementLines...)
	newLines = append(newLines, lines[endLine+1:]...)

	// Create a diff representation
	var diff bytes.Buffer
	diff.WriteString(fmt.Sprintf("--- %s (original)\n", op.Path))
	diff.WriteString(fmt.Sprintf("+++ %s (modified)\n", op.Path))
	diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", startLine+1, endLine-startLine+1, startLine+1, len(replacementLines)))
	for i := startLine; i <= endLine; i++ {
		diff.WriteString("- " + lines[i] + "\n")
	}
	for _, line := range replacementLines {
		diff.WriteString("+ " + line + "\n")
	}

	return strings.Join(newLines, "\n"), diff.String()
}

// Helper function to check if file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// Helper function to convert string to int
func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

// AgentPatchResult represents the result of applying an agent patch operation
type AgentPatchResult struct {
	Success       bool
	Error         error
	Path          string
	OriginalLines int
	NewLines      int
	Diff          string // Represents outcome description
}
