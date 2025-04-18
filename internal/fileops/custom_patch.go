package fileops

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
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
)

// ParseCustomPatch parses a patch in our custom format
// It now separates full file updates from hunks more explicitly during parsing.
func ParseCustomPatch(patchText string) ([]CustomPatchOperation, error) {
	var operations []CustomPatchOperation
	var currentOp *CustomPatchOperation
	var inPatch bool
	var inContent bool
	var contentLines []string

	scanner := bufio.NewScanner(strings.NewReader(patchText))
	lineNum := 0

	resetCurrentOp := func() {
		if currentOp != nil {
			// If it was a full file add/update, finalize it
			if inContent && !currentOp.IsHunk {
				currentOp.Content = strings.Join(contentLines, "\n")
				operations = append(operations, *currentOp)
			}
			// If it was a hunk and has content, finalize it
			if currentOp.IsHunk && (len(currentOp.AddLines) > 0 || len(currentOp.DelLines) > 0) {
				// Context is required for a valid hunk operation
				if len(currentOp.Context) > 0 {
					operations = append(operations, *currentOp)
				} else {
					// Or log a warning, maybe return error? Hunk without context is ambiguous.
				}
			}
		}
		currentOp = nil
		inContent = false
		contentLines = []string{}
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if patchStartRegex.MatchString(line) {
			if inPatch {
				return nil, fmt.Errorf("line %d: nested Begin Patch markers not allowed", lineNum)
			}
			inPatch = true
			continue
		}

		if endRegex.MatchString(line) {
			if !inPatch {
				return nil, fmt.Errorf("line %d: End Patch marker without Begin Patch", lineNum)
			}
			resetCurrentOp() // Finalize any pending operation before ending
			inPatch = false
			break // End of patch processing
		}

		if !inPatch {
			continue // Ignore lines outside patch markers
		}

		// Check for operation markers
		if updateMatch := updateFileRegex.FindStringSubmatch(line); updateMatch != nil {
			resetCurrentOp() // Finalize previous op
			currentOp = &CustomPatchOperation{
				Type: "update",
				Path: updateMatch[1],
			}
			inContent = true // Assume full file content unless hunk markers (+/-) appear
			continue
		}

		if addMatch := addFileRegex.FindStringSubmatch(line); addMatch != nil {
			resetCurrentOp()
			currentOp = &CustomPatchOperation{
				Type: "add",
				Path: addMatch[1],
			}
			inContent = true
			continue
		}

		if delMatch := deleteFileRegex.FindStringSubmatch(line); delMatch != nil {
			resetCurrentOp()
			// Delete is a complete operation on its own
			operations = append(operations, CustomPatchOperation{
				Type: "delete",
				Path: delMatch[1],
			})
			continue
		}

		// If we are inside an operation's content section
		if inContent && currentOp != nil {
			// Check for hunk markers (+, -, space)
			if addMatch := addLineRegex.FindStringSubmatch(line); addMatch != nil {
				if currentOp.Type == "add" {
					// For Add File, '+' is part of the content itself
					contentLines = append(contentLines, line)
				} else { // Must be an update operation
					// If this is the first hunk marker, switch from full file to hunk mode
					if !currentOp.IsHunk {
						currentOp.IsHunk = true
						// Discard any previously collected lines as they were assumed to be full content
						contentLines = []string{}
					}
					currentOp.AddLines = append(currentOp.AddLines, addMatch[1])
				}
				continue
			}

			if delMatch := delLineRegex.FindStringSubmatch(line); delMatch != nil {
				if currentOp.Type == "add" {
					// Cannot have '-' lines in an Add File operation
					return nil, fmt.Errorf("line %d: unexpected '-' marker in Add File section for %s", lineNum, currentOp.Path)
				}
				// Must be an update operation
				if !currentOp.IsHunk {
					currentOp.IsHunk = true
					contentLines = []string{}
				}
				currentOp.DelLines = append(currentOp.DelLines, delMatch[1])
				continue
			}

			if contextMatch := contextLineRegex.FindStringSubmatch(line); contextMatch != nil && strings.HasPrefix(line, " ") {
				if currentOp.Type == "add" {
					// For Add File, context lines are part of the content
					contentLines = append(contentLines, line)
				} else if currentOp.IsHunk {
					// If we already started Add/Del lines, a context line signifies the end of the current hunk
					if len(currentOp.AddLines) > 0 || len(currentOp.DelLines) > 0 {
						if len(currentOp.Context) > 0 {
							operations = append(operations, *currentOp)
						} else {
							// Hunk ended without preceding context? Invalid.
							return nil, fmt.Errorf("line %d: hunk add/delete lines must be preceded by context lines for %s", lineNum, currentOp.Path)
						}
						// Start a new hunk operation for the same file
						path := currentOp.Path // Save path before creating new op
						currentOp = &CustomPatchOperation{
							Type:   "update",
							Path:   path,
							IsHunk: true,
						}
					}
					// Add the context line (remove leading space)
					currentOp.Context = append(currentOp.Context, contextMatch[1])
				} else {
					// If it's not an 'add' and not yet a hunk, it's full file content
					contentLines = append(contentLines, line)
				}
				continue
			}

			// If it's not a marker (+, -, space) and not a comment
			if !currentOp.IsHunk && !existingCodeRegex.MatchString(line) {
				contentLines = append(contentLines, line)
			} else if currentOp.IsHunk {
				// Lines inside a hunk that don't start with +, -, or space are usually invalid
				// unless they are the special comment, which we can ignore here.
				if !existingCodeRegex.MatchString(line) {
					return nil, fmt.Errorf("line %d: unexpected line format inside hunk for %s: %s", lineNum, currentOp.Path, line)
				}
			}
		}
	}

	if scanner.Err() != nil {
		return nil, fmt.Errorf("error scanning patch text: %w", scanner.Err())
	}

	// Finalize any pending operation after loop finishes (if not ended by End Patch)
	if inPatch {
		resetCurrentOp()
		// If we were still in a patch but the loop ended, it implies missing End Patch marker
		return nil, errors.New("patch ended abruptly without End Patch marker")
	}

	return operations, nil
}

// ApplyCustomPatch applies a sequence of custom patch operations to the filesystem.
// It returns a slice of results, one for each operation attempt.
func ApplyCustomPatch(operations []CustomPatchOperation) ([]*CustomPatchResult, error) {
	var results []*CustomPatchResult
	fileContentsCache := make(map[string][]string) // Cache file content for multi-hunk updates
	failedHunks := make(map[string]bool)           // Track if a hunk failed for a specific file

	for _, op := range operations {
		var result *CustomPatchResult
		var err error

		// If a previous hunk failed for this file, skip subsequent hunks for the same file
		if op.IsHunk && failedHunks[op.Path] {
			result = &CustomPatchResult{
				Success:   false,
				Error:     fmt.Errorf("previous hunk failed for file %s, skipping this hunk", op.Path),
				Path:      op.Path,
				Operation: "update_hunk",
			}
			results = append(results, result)
			continue
		}

		switch op.Type {
		case "add":
			result, err = applyAddFile(op)
			// Clear cache if file is added, forcing reread if later updated
			delete(fileContentsCache, op.Path)
		case "delete":
			result, err = applyDeleteFile(op)
			// Clear cache as file is gone
			delete(fileContentsCache, op.Path)
		case "update":
			if op.IsHunk {
				result, err = applySingleHunk(op, fileContentsCache)
				if err != nil || !result.Success {
					failedHunks[op.Path] = true // Mark file as failed for subsequent hunks
				}
			} else {
				result, err = applyUpdateFile(op)
				// Clear cache as file content is now known
				delete(fileContentsCache, op.Path)
			}
		default:
			err = fmt.Errorf("unknown operation type: %s", op.Type)
			result = &CustomPatchResult{Success: false, Error: err, Path: op.Path, Operation: op.Type}
		}

		// If the specific apply function returned an error, ensure the result reflects it
		if err != nil {
			if result == nil { // Should not happen if functions follow contract
				result = &CustomPatchResult{Success: false, Error: err, Path: op.Path}
			} else {
				result.Success = false
				result.Error = err // Overwrite error if apply func returned one
			}
		}

		results = append(results, result)
	}

	// Overall error check is less critical now as individual results hold status
	// But we could return an error if *any* operation failed
	for _, r := range results {
		if !r.Success {
			// Maybe return the first error encountered? Or a summary error?
			// For now, let the caller inspect the results slice.
			// return results, r.Error // Example: return first error
		}
	}

	return results, nil
}

// applyAddFile creates a new file with the given content
func applyAddFile(op CustomPatchOperation) (*CustomPatchResult, error) {
	result := &CustomPatchResult{Path: op.Path, Operation: "add"}

	// Make sure the directory exists
	dir := filepath.Dir(op.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result.Error = fmt.Errorf("failed to create directory %s: %w", dir, err)
		return result, result.Error
	}

	// Check if file already exists
	if _, err := os.Stat(op.Path); err == nil {
		result.Error = fmt.Errorf("cannot add file %s: file already exists", op.Path)
		return result, result.Error
	} else if !os.IsNotExist(err) {
		// Handle other stat errors
		result.Error = fmt.Errorf("failed to check file status %s: %w", op.Path, err)
		return result, result.Error
	}

	// Write the file
	if err := ioutil.WriteFile(op.Path, []byte(op.Content), 0644); err != nil {
		result.Error = fmt.Errorf("failed to write file %s: %w", op.Path, err)
		return result, result.Error
	}

	result.Success = true
	result.OriginalLines = 0
	result.NewLines = len(strings.Split(op.Content, "\n"))
	return result, nil
}

// applyDeleteFile deletes a file
func applyDeleteFile(op CustomPatchOperation) (*CustomPatchResult, error) {
	result := &CustomPatchResult{Path: op.Path, Operation: "delete"}

	// Check if file exists
	info, err := os.Stat(op.Path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Error = fmt.Errorf("cannot delete file %s: file does not exist", op.Path)
		} else {
			result.Error = fmt.Errorf("failed to stat file %s: %w", op.Path, err)
		}
		return result, result.Error
	}

	// Don't delete directories
	if info.IsDir() {
		result.Error = fmt.Errorf("cannot delete %s: is a directory", op.Path)
		return result, result.Error
	}

	// Get original line count
	content, readErr := ioutil.ReadFile(op.Path)
	if readErr != nil {
		// Proceed with deletion, but log/note the read error
		result.OriginalLines = -1 // Indicate unknown original size
	} else {
		result.OriginalLines = len(strings.Split(string(content), "\n"))
	}

	// Delete the file
	if err := os.Remove(op.Path); err != nil {
		result.Error = fmt.Errorf("failed to delete file %s: %w", op.Path, err)
		return result, result.Error
	}

	result.Success = true
	result.NewLines = 0
	if readErr != nil {
		// Add a note about the prior read error if desired
		// result.Error = fmt.Errorf("deleted file %s, but failed to read original content: %w", op.Path, readErr)
	}
	return result, nil
}

// applyUpdateFile replaces a file with new content (full update)
func applyUpdateFile(op CustomPatchOperation) (*CustomPatchResult, error) {
	result := &CustomPatchResult{Path: op.Path, Operation: "update"}

	// Check if file exists to get original line count
	originalContent, err := ioutil.ReadFile(op.Path)
	if err != nil {
		if os.IsNotExist(err) {
			// If the file doesn't exist, treat it like an add operation
			// Note: This deviates slightly from strict patch behavior but aligns with 'upsert'
			return applyAddFile(op) // Re-use add logic
		}
		result.Error = fmt.Errorf("failed to read existing file %s for update: %w", op.Path, err)
		return result, result.Error
	}
	result.OriginalLines = len(strings.Split(string(originalContent), "\n"))

	// Write the new content
	if err := ioutil.WriteFile(op.Path, []byte(op.Content), 0644); err != nil {
		result.Error = fmt.Errorf("failed to write updated file %s: %w", op.Path, err)
		return result, result.Error
	}

	result.Success = true
	result.NewLines = len(strings.Split(op.Content, "\n"))
	return result, nil
}

// applySingleHunk applies one specific hunk operation to a file.
// It reads the file (using cache if available), applies the hunk, and writes back.
func applySingleHunk(op CustomPatchOperation, fileContentsCache map[string][]string) (*CustomPatchResult, error) {
	result := &CustomPatchResult{Path: op.Path, Operation: "update_hunk"}

	// Read current file content (or use cache)
	fileLines, ok := fileContentsCache[op.Path]
	if !ok {
		contentBytes, err := ioutil.ReadFile(op.Path)
		if err != nil {
			if os.IsNotExist(err) {
				result.Error = fmt.Errorf("cannot apply hunk to %s: file does not exist", op.Path)
			} else {
				result.Error = fmt.Errorf("failed to read file %s for hunk: %w", op.Path, err)
			}
			return result, result.Error
		}
		fileLines = strings.Split(string(contentBytes), "\n")
	}
	result.OriginalLines = len(fileLines)

	// Find where the hunk should be applied
	matchPos := findFuzzyMatch(fileLines, op.Context, op.DelLines)
	if matchPos == -1 {
		result.Error = fmt.Errorf("failed to locate hunk context in file %s", op.Path)
		return result, result.Error
	}

	// Construct the new content lines after applying this hunk
	var newFileLines []string
	newFileLines = append(newFileLines, fileLines[:matchPos]...)

	// If we have additions, add them
	if len(op.AddLines) > 0 {
		newFileLines = append(newFileLines, op.AddLines...)
	}

	// Calculate how many original lines this hunk replaces (context + deletions)
	// Note: The block to match in findFuzzyMatch includes context AND deletions.
	skipLines := len(op.Context) + len(op.DelLines)

	// Add the remaining lines from the original file
	if matchPos+skipLines <= len(fileLines) { // Ensure we don't index out of bounds
		newFileLines = append(newFileLines, fileLines[matchPos+skipLines:]...)
	} else if matchPos < len(fileLines) {
		// If the match was at the very end, there might be lines before matchPos + skipLines
		// but the skip goes past the end. This case might need review depending on desired EOF handling.
		// Currently, it correctly appends nothing more.
	}

	// Write the modified content back to the file
	newContent := strings.Join(newFileLines, "\n")
	if err := ioutil.WriteFile(op.Path, []byte(newContent), 0644); err != nil {
		result.Error = fmt.Errorf("failed to write updated file %s after hunk: %w", op.Path, err)
		// Update cache with the *original* lines since write failed
		fileContentsCache[op.Path] = fileLines
		return result, result.Error
	}

	// Update cache with the *new* lines since write succeeded
	fileContentsCache[op.Path] = newFileLines

	result.Success = true
	result.NewLines = len(newFileLines)
	return result, nil
}

// findFuzzyMatch uses fuzzy matching to find the position in the file
// that best matches the context and deleted lines, mimicking the TS logic.
func findFuzzyMatch(fileLines []string, context, delLines []string) int {
	// Combine context and delete lines to form the block we need to match
	blockToMatch := append(context, delLines...)
	if len(blockToMatch) == 0 {
		// If only additions are present, match position is based purely on context lines
		blockToMatch = context
		if len(blockToMatch) == 0 {
			return -1 // Cannot match an empty hunk context
		}
	}

	// Try matching at each possible starting position
	for i := 0; i <= len(fileLines)-len(blockToMatch); i++ {
		fileBlock := fileLines[i : i+len(blockToMatch)]

		// 1. Exact match
		if blocksMatchExact(fileBlock, blockToMatch) {
			return i
		}

		// 2. Match ignoring trailing whitespace
		if blocksMatchTrimSuffixSpace(fileBlock, blockToMatch) {
			return i
		}

		// 3. Match ignoring all leading/trailing whitespace
		if blocksMatchTrimSpace(fileBlock, blockToMatch) {
			return i
		}
	}

	// TODO: Consider adding EOF specific logic if needed, like in the TS version.
	// The TS version has a special check if the context is expected at EOF.

	return -1 // No match found
}

// blocksMatchExact checks if two slices of strings match exactly.
func blocksMatchExact(block1, block2 []string) bool {
	if len(block1) != len(block2) {
		return false
	}
	for j := range block1 {
		if block1[j] != block2[j] {
			return false
		}
	}
	return true
}

// blocksMatchTrimSuffixSpace checks if two slices of strings match after removing trailing spaces.
func blocksMatchTrimSuffixSpace(block1, block2 []string) bool {
	if len(block1) != len(block2) {
		return false
	}
	for j := range block1 {
		if strings.TrimRight(block1[j], " \t") != strings.TrimRight(block2[j], " \t") {
			return false
		}
	}
	return true
}

// blocksMatchTrimSpace checks if two slices of strings match after trimming all whitespace.
func blocksMatchTrimSpace(block1, block2 []string) bool {
	if len(block1) != len(block2) {
		return false
	}
	for j := range block1 {
		if strings.TrimSpace(block1[j]) != strings.TrimSpace(block2[j]) {
			return false
		}
	}
	return true
}
