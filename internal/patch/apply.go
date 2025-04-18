package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateFileWithChunks applies chunks of changes to a file
func UpdateFileWithChunks(text string, action PatchAction, path string) (string, error) {
	lines := strings.Split(text, "\n")
	destLines := make([]string, 0, len(lines))
	origIndex := 0

	// Sort chunks by OrigIndex in descending order to apply from bottom to top
	// This avoids messing up line numbers as we make changes
	chunks := action.Chunks

	// Apply each chunk
	for _, chunk := range chunks {
		// Add unchanged lines from origIndex up to the chunk
		if chunk.OrigIndex > origIndex {
			destLines = append(destLines, lines[origIndex:chunk.OrigIndex]...)
		}

		// Add the inserted lines
		destLines = append(destLines, chunk.InsLines...)

		// Skip the deleted lines
		origIndex = chunk.OrigIndex + len(chunk.DelLines)
	}

	// Add any remaining lines from the original file
	if origIndex < len(lines) {
		destLines = append(destLines, lines[origIndex:]...)
	}

	return strings.Join(destLines, "\n"), nil
}

// PatchToCommit converts a Patch to a Commit
func PatchToCommit(patch Patch, orig map[string]string) Commit {
	commit := Commit{
		Changes: make(map[string]FileChange),
	}

	for pathKey, action := range patch.Actions {
		switch action.Type {
		case ActionDelete:
			commit.Changes[pathKey] = FileChange{
				Type:       ActionDelete,
				OldContent: orig[pathKey],
			}
		case ActionAdd:
			commit.Changes[pathKey] = FileChange{
				Type:       ActionAdd,
				NewContent: action.NewFile,
			}
		case ActionUpdate:
			newContent, _ := UpdateFileWithChunks(orig[pathKey], action, pathKey)
			commit.Changes[pathKey] = FileChange{
				Type:       ActionUpdate,
				OldContent: orig[pathKey],
				NewContent: newContent,
				MovePath:   action.MovePath,
			}
		}
	}

	return commit
}

// LoadFiles loads the content of multiple files
func LoadFiles(paths []string) (map[string]string, error) {
	orig := make(map[string]string)

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, &DiffError{Message: fmt.Sprintf("File not found: %s", p)}
		}
		orig[p] = string(data)
	}

	return orig, nil
}

// LineStats tracks line changes in a file
type LineStats struct {
	Original int
	New      int
	Added    int
}

// LegacyPatchResult represents the result of applying a legacy patch operation
type LegacyPatchResult struct {
	FilePath      string
	OperationType string
	Success       bool
	Error         error
	Message       string
	LineStats     LineStats
}

// LegacyApplyCommit applies a commit to the filesystem (legacy version)
func LegacyApplyCommit(commit Commit) ([]*LegacyPatchResult, error) {
	results := make([]*LegacyPatchResult, 0, len(commit.Changes))

	for path, change := range commit.Changes {
		result := &LegacyPatchResult{
			FilePath:      path,
			OperationType: string(change.Type),
			Success:       true,
			LineStats:     LineStats{},
		}

		switch change.Type {
		case ActionDelete:
			// Check if file exists
			if _, err := os.Stat(path); err != nil {
				result.Success = false
				result.Error = err
				result.Message = fmt.Sprintf("Failed to delete %s: %v", path, err)
			} else {
				// Remove the file
				if err := os.Remove(path); err != nil {
					result.Success = false
					result.Error = err
					result.Message = fmt.Sprintf("Failed to delete %s: %v", path, err)
				} else {
					result.Message = fmt.Sprintf("Deleted file: %s", path)
				}
			}

		case ActionAdd:
			// Create directories if needed
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0755); err != nil {
				result.Success = false
				result.Error = err
				result.Message = fmt.Sprintf("Failed to create directory for %s: %v", path, err)
				results = append(results, result)
				continue
			}

			// Write the file
			if err := os.WriteFile(path, []byte(change.NewContent), 0644); err != nil {
				result.Success = false
				result.Error = err
				result.Message = fmt.Sprintf("Failed to write to %s: %v", path, err)
			} else {
				// Count the lines
				numLines := len(strings.Split(change.NewContent, "\n"))
				result.LineStats.New = numLines
				result.LineStats.Added = numLines
				result.Message = fmt.Sprintf("Added file: %s (%d lines)", path, numLines)
			}

		case ActionUpdate:
			targetPath := path
			if change.MovePath != "" {
				targetPath = change.MovePath

				// Create directories for the target path if needed
				dir := filepath.Dir(targetPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					result.Success = false
					result.Error = err
					result.Message = fmt.Sprintf("Failed to create directory for %s: %v", targetPath, err)
					results = append(results, result)
					continue
				}
			}

			// Count original and new lines
			originalLines := 0
			if change.OldContent != "" {
				originalLines = len(strings.Split(change.OldContent, "\n"))
			}
			newLines := 0
			if change.NewContent != "" {
				newLines = len(strings.Split(change.NewContent, "\n"))
			}

			// Write the new content
			if err := os.WriteFile(targetPath, []byte(change.NewContent), 0644); err != nil {
				result.Success = false
				result.Error = err
				result.Message = fmt.Sprintf("Failed to write to %s: %v", targetPath, err)
			} else {
				result.LineStats.Original = originalLines
				result.LineStats.New = newLines
				result.LineStats.Added = newLines - originalLines

				if change.MovePath != "" {
					// Remove the original file if moving
					if err := os.Remove(path); err != nil {
						// Just log the error, don't fail the entire operation
						result.Message = fmt.Sprintf("Updated and moved file: %s -> %s (%d -> %d lines), but failed to delete original: %v",
							path, targetPath, originalLines, newLines, err)
					} else {
						result.Message = fmt.Sprintf("Updated and moved file: %s -> %s (%d -> %d lines)",
							path, targetPath, originalLines, newLines)
					}
				} else {
					result.Message = fmt.Sprintf("Updated file: %s (%d -> %d lines)",
						path, originalLines, newLines)
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// IdentifyFilesNeeded extracts the list of files that need to be loaded from a patch
func IdentifyFilesNeeded(text string) []string {
	lines := strings.Split(text, "\n")
	result := make(map[string]bool)

	for _, line := range lines {
		if strings.HasPrefix(line, UpdateFilePrefix) {
			filePath := strings.TrimPrefix(line, UpdateFilePrefix)
			result[filePath] = true
		} else if strings.HasPrefix(line, DeleteFilePrefix) {
			filePath := strings.TrimPrefix(line, DeleteFilePrefix)
			result[filePath] = true
		}
	}

	// Convert to slice
	paths := make([]string, 0, len(result))
	for path := range result {
		paths = append(paths, path)
	}

	return paths
}

// LegacyProcessPatch is the high-level function to process a patch using the legacy format
func LegacyProcessPatch(patchText string) ([]*LegacyPatchResult, error) {
	// Validate basics
	if !strings.HasPrefix(patchText, PatchBeginMarker) {
		return nil, &DiffError{Message: "Patch must start with *** Begin Patch"}
	}

	// Identify files needed
	paths := IdentifyFilesNeeded(patchText)

	// Load the files
	orig, err := LoadFiles(paths)
	if err != nil {
		return nil, err
	}

	// Convert text to patch
	patch, _, err := TextToPatch(patchText, orig)
	if err != nil {
		return nil, err
	}

	// Convert patch to commit
	commit := PatchToCommit(patch, orig)

	// Apply the commit
	return LegacyApplyCommit(commit)
}

// ProcessPatch processes a patch string and applies all operations
func ProcessPatch(patchText string) ([]PatchResult, error) {
	operations, err := ParseSimplePatch(patchText)
	if err != nil {
		return nil, err
	}

	results := make([]PatchResult, 0, len(operations))
	for _, op := range operations {
		result := PatchResult{
			FilePath:      op.Path,
			OperationType: op.Type,
			Success:       false,
		}

		var err error
		switch op.Type {
		case "add":
			err = applyAddOperation(*op)
		case "update":
			err = applyUpdateOperation(*op)
		case "delete":
			err = applyDeleteOperation(*op)
		case "move":
			err = applyMoveOperation(*op)
		default:
			err = fmt.Errorf("unknown operation type: %s", op.Type)
		}

		result.Error = err
		result.Success = err == nil
		results = append(results, result)
	}

	return results, nil
}

// applyAddOperation applies an add operation by creating a new file
func applyAddOperation(op PatchOperation) error {
	// Create the directory structure if it doesn't exist
	dir := filepath.Dir(op.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directories for %s: %w", op.Path, err)
	}

	// Create the file
	if err := os.WriteFile(op.Path, []byte(op.Content), 0644); err != nil {
		return fmt.Errorf("failed to create file %s: %w", op.Path, err)
	}

	return nil
}

// applyUpdateOperation applies an update operation to an existing file
func applyUpdateOperation(op PatchOperation) error {
	// Check if the file exists
	_, err := os.Stat(op.Path)
	if err != nil {
		return fmt.Errorf("cannot update file %s: %w", op.Path, err)
	}

	// Read the current file content
	content, err := os.ReadFile(op.Path)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", op.Path, err)
	}

	// Apply the changes
	newContent, err := applyChanges(string(content), op)
	if err != nil {
		return fmt.Errorf("failed to apply changes to %s: %w", op.Path, err)
	}

	// Write the updated content
	if err := os.WriteFile(op.Path, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated content to %s: %w", op.Path, err)
	}

	return nil
}

// applyDeleteOperation deletes a file
func applyDeleteOperation(op PatchOperation) error {
	// Check if the file exists
	_, err := os.Stat(op.Path)
	if err != nil {
		return fmt.Errorf("cannot delete file %s: %w", op.Path, err)
	}

	// Delete the file
	if err := os.Remove(op.Path); err != nil {
		return fmt.Errorf("failed to delete file %s: %w", op.Path, err)
	}

	return nil
}

// applyMoveOperation moves a file to a new location
func applyMoveOperation(op PatchOperation) error {
	// Check if the source file exists
	_, err := os.Stat(op.Path)
	if err != nil {
		return fmt.Errorf("cannot move file %s: %w", op.Path, err)
	}

	// If there's no MoveTo path, it's an error
	if op.MoveTo == "" {
		return fmt.Errorf("move operation requires a destination path")
	}

	// Create the directory structure for the destination if it doesn't exist
	destDir := filepath.Dir(op.MoveTo)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directories for %s: %w", op.MoveTo, err)
	}

	// If we need to update content as well as move
	if len(op.DelLines) > 0 || len(op.AddLines) > 0 {
		// Read the current file content
		content, err := os.ReadFile(op.Path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", op.Path, err)
		}

		// Apply the changes
		newContent, err := applyChanges(string(content), op)
		if err != nil {
			return fmt.Errorf("failed to apply changes to %s: %w", op.Path, err)
		}

		// Write the content to the new location
		if err := os.WriteFile(op.MoveTo, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("failed to write content to %s: %w", op.MoveTo, err)
		}
	} else {
		// Simple move without content changes
		if err := os.Rename(op.Path, op.MoveTo); err != nil {
			return fmt.Errorf("failed to move file from %s to %s: %w", op.Path, op.MoveTo, err)
		}
	}

	// Delete the original file if it still exists
	if op.MoveTo != op.Path {
		_, err := os.Stat(op.Path)
		if err == nil {
			if err := os.Remove(op.Path); err != nil {
				return fmt.Errorf("failed to delete original file %s after move: %w", op.Path, err)
			}
		}
	}

	return nil
}

// applyChanges applies the changes specified in the operation to the content
func applyChanges(content string, op PatchOperation) (string, error) {
	// If there are no deletes or adds, return the original content
	if len(op.DelLines) == 0 && len(op.AddLines) == 0 {
		return content, nil
	}

	// Simple content replacement if Context is empty
	if len(op.Context) == 0 {
		if len(op.DelLines) > 0 {
			return strings.Join(op.AddLines, "\n"), nil
		}
		return content + "\n" + strings.Join(op.AddLines, "\n"), nil
	}

	lines := strings.Split(content, "\n")

	// Use fuzzy matching to find the context
	contextIdx := findContextInContent(lines, op.Context)
	if contextIdx == -1 {
		return "", fmt.Errorf("context not found in content")
	}

	// Apply the changes
	newLines := make([]string, 0, len(lines)+len(op.AddLines)-len(op.DelLines))
	newLines = append(newLines, lines[:contextIdx]...)

	// Skip deleted lines
	delCount := len(op.DelLines)

	// Add context lines before deletion
	contextBeforeDel := 0
	for i, line := range op.Context {
		if i < contextIdx+len(op.Context) && i < len(lines) && lines[i] == line {
			contextBeforeDel++
		} else {
			break
		}
	}

	newLines = append(newLines, lines[contextIdx:contextIdx+contextBeforeDel]...)

	// Add new lines
	newLines = append(newLines, op.AddLines...)

	// Add remaining lines
	if contextIdx+contextBeforeDel+delCount < len(lines) {
		newLines = append(newLines, lines[contextIdx+contextBeforeDel+delCount:]...)
	}

	return strings.Join(newLines, "\n"), nil
}

// findContextInContent finds the index where the context begins in the content
func findContextInContent(lines []string, context []string) int {
	if len(context) == 0 {
		return 0
	}

	for i := 0; i <= len(lines)-len(context); i++ {
		matches := true
		for j, contextLine := range context {
			if i+j >= len(lines) || lines[i+j] != contextLine {
				matches = false
				break
			}
		}
		if matches {
			return i
		}
	}

	// Try fuzzy matching if exact match fails
	bestMatchScore := 0
	bestMatchIndex := -1

	for i := 0; i <= len(lines)-len(context); i++ {
		score := 0
		for j, contextLine := range context {
			if i+j < len(lines) {
				// Calculate similarity score (simple approach)
				if lines[i+j] == contextLine {
					score += 3 // Exact match is worth more
				} else if strings.Contains(lines[i+j], contextLine) || strings.Contains(contextLine, lines[i+j]) {
					score += 1 // Partial match
				}
			}
		}
		if score > bestMatchScore {
			bestMatchScore = score
			bestMatchIndex = i
		}
	}

	// Return best match if it's good enough
	if bestMatchScore >= len(context) {
		return bestMatchIndex
	}

	return -1
}
