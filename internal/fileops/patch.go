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
