package fileops

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
)

// CustomPatchOperation represents an operation in our custom patch format
type CustomPatchOperation struct {
	Type     string   // "update", "add", "delete"
	Path     string   // Path to the file
	Content  string   // Content for the file (add, full update)
	IsHunk   bool     // Whether this is a partial update (hunk) or full file update
	Context  []string // Context lines for fuzzy matching (hunk)
	AddLines []string // Lines to add (hunk)
	DelLines []string // Lines to delete (hunk)
}

// CustomPatchResult represents the result of applying one custom patch operation
type CustomPatchResult struct {
	Success       bool
	Error         error
	Path          string
	OriginalLines int    // Line count of the file *before* this specific operation
	NewLines      int    // Line count of the file *after* this specific operation (if successful)
	Operation     string // "add", "delete", "update", "update_hunk"
	// TODO: Maybe add HunkIndex later if needed for UI clarity
}

// Regex patterns for parsing
var (
	patchStartRegex   = regexp.MustCompile(`^\*\*\* Begin Patch\s*$`) // Renamed from beginRegex
	updateFileRegex   = regexp.MustCompile(`^\*\*\* Update File:\s+(.+)\s*$`)
	addFileRegex      = regexp.MustCompile(`^\*\*\* Add File:\s+(.+)\s*$`)
	deleteFileRegex   = regexp.MustCompile(`^\*\*\* Delete File:\s+(.+)\s*$`)
	endRegex          = regexp.MustCompile(`^\*\*\* End Patch\s*$`)              // Renamed from patchEndRegex
	addLineRegex      = regexp.MustCompile(`^\+\s*(.*)$`)                        // Add line within a hunk
	delLineRegex      = regexp.MustCompile(`^-\s*(.*)$`)                         // Delete line within a hunk
	contextLineRegex  = regexp.MustCompile(`^\s*(.*)$`)                          // Context line within a hunk (leading space)
	existingCodeRegex = regexp.MustCompile(`^\/\/ \.\.\. existing code \.\.\.$`) // Added for clarity in format examples
	fileMarkerRegex   = regexp.MustCompile(`^\*\*\* FILE:\s+(.+)\s*$`)
)

// ParseCustomPatch parses a patch in our custom format OR the simplified Agent format
// It now separates full file updates from hunks more explicitly during parsing.
func ParseCustomPatch(patchText string) ([]CustomPatchOperation, error) {
	var operations []CustomPatchOperation
	// var currentOp *CustomPatchOperation // Removed as unused
	// var inPatch bool // Not strictly needed for the simple format, but keep for potential future mixed format use
	var currentFile string
	var addLines []string
	var delLines []string

	scanner := bufio.NewScanner(strings.NewReader(patchText))
	lineNum := 0

	finalizeOperation := func() {
		if currentFile != "" && (len(addLines) > 0 || len(delLines) > 0) {
			// Create a single 'update' operation with additions and deletions
			// For simplicity, we treat this as a single non-hunk update for now.
			// A more sophisticated approach might create multiple hunk operations.
			operations = append(operations, CustomPatchOperation{
				Type:     "update", // Treat combined ADD/DEL as update
				Path:     currentFile,
				IsHunk:   true, // Mark as hunk because it's partial changes
				AddLines: addLines,
				DelLines: delLines,
				// Context is omitted in this simple format
			})
			addLines = []string{}
			delLines = []string{}
			currentFile = "" // Reset for the next potential file
		}
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check for FILE marker first
		if fileMatch := fileMarkerRegex.FindStringSubmatch(line); fileMatch != nil {
			finalizeOperation() // Finalize any operation for the previous file
			currentFile = fileMatch[1]
			continue
		}

		// Skip lines if no file context is set yet
		if currentFile == "" {
			continue
		}

		// Handle simplified ADD: prefix
		if strings.HasPrefix(line, "ADD:") {
			content := strings.TrimSpace(strings.TrimPrefix(line, "ADD:"))
			addLines = append(addLines, content)
			continue
		}

		// Handle simplified DEL: prefix
		if strings.HasPrefix(line, "DEL:") {
			content := strings.TrimSpace(strings.TrimPrefix(line, "DEL:"))
			delLines = append(delLines, content)
			continue
		}

		// Ignore other lines (like comments // EDIT:, // END_EDIT) in the simple format
	}

	finalizeOperation() // Finalize any remaining operation

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning patch text: %w", err)
	}

	// // Original complex parser logic commented out for now
	// /*
	// var operations []CustomPatchOperation
	// var currentOp *CustomPatchOperation
	// var inPatch bool
	// var inContent bool
	// var contentLines []string
	// ... (rest of the original complex parser code) ...
	// */

	return operations, nil
}

// ApplyCustomPatch applies a sequence of custom patch operations to the filesystem.
// It returns a slice of results, one for each operation attempt.
func ApplyCustomPatch(operations []CustomPatchOperation) ([]*CustomPatchResult, error) {
	var results []*CustomPatchResult
	fileContentsCache := make(map[string][]string) // Cache file content for multi-hunk updates
	failedHunks := make(map[string]bool)           // Track if a hunk failed for a specific file

	for i, op := range operations { // Use index for logging/debugging if needed
		var result *CustomPatchResult
		var err error
		opDescription := fmt.Sprintf("Operation %d: %s %s", i+1, strings.ToUpper(op.Type), op.Path)
		if op.IsHunk {
			opDescription += " (Hunk)"
		}

		// If a previous hunk failed for this file, skip subsequent hunks for the same file
		// Note: With the simplified parser, we usually have only one 'hunk' per file now.
		if op.IsHunk && failedHunks[op.Path] {
			result = &CustomPatchResult{
				Operation: op.Type, // Be specific: "update_hunk" or "update"
				Path:      op.Path,
				Success:   false,
				Error:     fmt.Errorf("previous operation failed for file %s, skipping this one", op.Path),
			}
			results = append(results, result)
			log.Printf("%s - SKIPPED: %v", opDescription, result.Error)
			continue
		}

		switch op.Type {
		// Removed case "add" as it's unreachable with the current parser
		// case "add":
		// 	result, err = applyAddFile(op)
		// Removed case "delete" as it's unreachable with the current parser
		// case "delete":
		// 	result, err = applyDeleteFile(op)
		case "update":
			if op.IsHunk {
				// This is the path taken by the simplified ADD:/DEL: parser
				result, err = applySingleHunk(op, fileContentsCache) // Pass cache
				if result != nil {
					result.Operation = "update_hunk" // Be specific
				}
			} // Removed else block for full file update as it's unreachable
			// else {
			// 	result, err = applyUpdateFile(op)
			// 	if result != nil {
			// 		result.Operation = "update_full"
			// 	}
			// }
		default:
			err = fmt.Errorf("unknown or unreachable patch operation type: %s", op.Type)
			result = &CustomPatchResult{
				Operation: "unknown",
				Path:      op.Path,
				Success:   false,
				Error:     err,
			}
		}

		// Ensure result is never nil even if apply functions misbehave
		if result == nil {
			result = &CustomPatchResult{
				Operation: op.Type,
				Path:      op.Path,
				Success:   false,
				Error:     fmt.Errorf("internal error: apply function returned nil result for %s", op.Type),
			}
			if err != nil { // Combine errors if possible
				result.Error = fmt.Errorf("%w; %w", result.Error, err)
			}
		} else if err != nil && result.Error == nil {
			// If the apply function returned an error but didn't set it in the result
			result.Error = err
			result.Success = false
		}

		results = append(results, result)

		if !result.Success {
			log.Printf("%s - FAILED: %v", opDescription, result.Error)
			if op.IsHunk {
				failedHunks[op.Path] = true // Mark file as failed for subsequent hunks
			}
		} else {
			log.Printf("%s - SUCCESS: Original Lines: %d, New Lines: %d", opDescription, result.OriginalLines, result.NewLines)
			// Invalidate cache for this file if it was modified successfully
			// applySingleHunk should update the cache internally if successful
			// delete(fileContentsCache, op.Path) // Or let applySingleHunk manage it
		}
	}

	// Check for overall errors (e.g., permission issues not tied to a specific operation)
	// This simplistic loop doesn't introduce overall errors, but a more complex apply might.
	// For now, we just return the collected results.
	return results, nil // Return nil error, individual errors are in results
}

// applyAddFile creates a new file with the specified content.
// ... existing code ...
// applyDeleteFile deletes the specified file.
// ... existing code ...
// applyUpdateFile replaces the entire content of a file.
// ... existing code ...

// applySingleHunk attempts to apply a single set of changes (context, deletions, additions) to a file.
// It now uses and potentially updates the file content cache.
func applySingleHunk(op CustomPatchOperation, fileContentsCache map[string][]string) (*CustomPatchResult, error) {
	result := &CustomPatchResult{
		Operation: "update_hunk", // Default operation type
		Path:      op.Path,
		Success:   false, // Assume failure
	}

	// Get file content, using cache if available
	fileLines, ok := fileContentsCache[op.Path]
	if !ok {
		contentBytes, err := os.ReadFile(op.Path)
		if err != nil {
			if os.IsNotExist(err) {
				// If the simplified parser created a hunk for a non-existent file, it's an error
				result.Error = fmt.Errorf("file does not exist and cannot apply hunk: %w", err)
				return result, result.Error // Return early
			}
			result.Error = fmt.Errorf("failed to read file: %w", err)
			return result, result.Error // Return early
		}
		fileLines = strings.Split(string(contentBytes), "\n")
		fileContentsCache[op.Path] = fileLines // Cache the read content
	}
	originalLinesCount := len(fileLines)
	result.OriginalLines = originalLinesCount

	// --- Simplified Application Logic for ADD:/DEL: (No Context Matching) ---
	// This section assumes the simple format where DelLines should be removed
	// and AddLines should be appended. This is a basic interpretation.
	// A better approach might insert them relative to deletions or context (if available)
	newFileLines := append(fileLines, op.AddLines...)

	// --- End Simplified Logic ---

	// // --- Original Hunk Logic (Commented Out) ---
	// /*
	// // Find the starting position of the hunk using fuzzy matching on context lines
	// matchIndex := -1
	// if len(op.Context) > 0 {
	// 	matchIndex = findFuzzyMatch(fileLines, op.Context, op.DelLines)
	// } else if len(op.DelLines) > 0 {
	//     // Attempt direct match on delete lines if no context provided
	//     matchIndex = findFuzzyMatch(fileLines, op.DelLines, nil) // Use DelLines as context
	// }

	// if matchIndex == -1 && (len(op.Context) > 0 || len(op.DelLines) > 0) {
	// 	result.Error = errors.New("failed to find matching context/deletion lines in file")
	// 	return result, result.Error // Return early
	// }

	// // Verify that the lines immediately following the context match the DelLines
	// if len(op.DelLines) > 0 {
	//     expectedDelEndIndex := matchIndex + len(op.Context) + len(op.DelLines)
	//     if expectedDelEndIndex > len(fileLines) {
	//         result.Error = fmt.Errorf("deletion block extends beyond end of file (expected end %d, file lines %d)", expectedDelEndIndex, len(fileLines))
	//         return result, result.Error
	//     }
	//     actualDelLines := fileLines[matchIndex+len(op.Context) : expectedDelEndIndex]

	// 	// Use flexible matching for deletion verification
	// 	if !blocksMatchTrimSpace(op.DelLines, actualDelLines) { // Allow whitespace differences
	// 		result.Error = fmt.Errorf("deletion lines do not match file content at offset %d. Expected '%v', got '%v'",
	//             matchIndex+len(op.Context), op.DelLines, actualDelLines)
	// 		return result, result.Error
	// 	}
	// }

	// // Construct the new file content
	// var newFileLines []string
	// // Add lines before the hunk
	// newFileLines = append(newFileLines, fileLines[:matchIndex+len(op.Context)]...)
	// // Add the new lines (AddLines)
	// newFileLines = append(newFileLines, op.AddLines...)
	// // Add lines after the deleted section
	// deleteEndIndex := matchIndex + len(op.Context) + len(op.DelLines)
	// if deleteEndIndex < len(fileLines) {
	// 	newFileLines = append(newFileLines, fileLines[deleteEndIndex:]...)
	// }
	// */
	// // --- End Original Hunk Logic ---

	// Write the modified content back to the file
	newContent := strings.Join(newFileLines, "\n")
	// Use WriteFile for atomic write operations
	// Ensure correct permissions, e.g., 0644 or derive from original
	// Get original file permissions
	info, statErr := os.Stat(op.Path)
	perms := os.FileMode(0644) // Default permissions
	if statErr == nil {
		perms = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		// Handle stat errors other than NotExist if necessary
		log.Printf("Warning: Could not stat original file %s to get permissions: %v. Using default 0644.", op.Path, statErr)
	}

	err := os.WriteFile(op.Path, []byte(newContent), perms)
	if err != nil {
		result.Error = fmt.Errorf("failed to write updated file content: %w", err)
		return result, result.Error // Return failure
	}

	// Update cache with the new content
	fileContentsCache[op.Path] = newFileLines

	result.Success = true
	result.NewLines = len(newFileLines)
	log.Printf("Successfully applied hunk to %s. Original lines: %d, New lines: %d", op.Path, originalLinesCount, result.NewLines)
	return result, nil // Success
}

// findFuzzyMatch tries to find the starting line index of a block (context or delLines)
// ... existing code ...
// blocksMatchExact checks if two slices of strings are identical.
// ... existing code ...
// blocksMatchTrimSuffixSpace checks if two slices of strings match after trimming trailing spaces.
// ... existing code ...
// blocksMatchTrimSpace checks if two slices of strings match after trimming leading/trailing spaces.
// ... existing code ...
