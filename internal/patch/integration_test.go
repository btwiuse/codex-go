package patch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationPatchProcess(t *testing.T) {
	// Skip if not in a normal test run to avoid filesystem changes
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "patch-test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	existingFilePath := filepath.Join(tempDir, "existing.txt")
	existingContent := "Line 1\nLine 2\nLine 3\nLine 4\n"
	err = os.WriteFile(existingFilePath, []byte(existingContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Define paths we'll use
	newFilePath := filepath.Join(tempDir, "new.txt")
	movedFilePath := filepath.Join(tempDir, "moved.txt")

	// Test simple file creation
	addOp := &PatchOperation{
		Type:    "add",
		Path:    newFilePath,
		Content: "This is a new file\nWith multiple lines",
	}
	err = applyAddOperation(*addOp)
	if err != nil {
		t.Fatalf("Failed to apply add operation: %v", err)
	}

	// Test simple file content update
	updatedContent := "Line 1\nLine 2\nLine 3 (modified)\nLine 4\n"
	err = os.WriteFile(existingFilePath, []byte(updatedContent), 0644)
	if err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}

	// Test file move
	moveOp := &PatchOperation{
		Type:   "move",
		Path:   existingFilePath,
		MoveTo: movedFilePath,
	}
	err = applyMoveOperation(*moveOp)
	if err != nil {
		t.Fatalf("Failed to apply move operation: %v", err)
	}

	// Check if the new file was created with correct content
	newContent, err := os.ReadFile(newFilePath)
	if err != nil {
		t.Errorf("Failed to read new file: %v", err)
	} else if string(newContent) != "This is a new file\nWith multiple lines" {
		t.Errorf("New file content not correct:\nExpected: %q\nGot: %q",
			"This is a new file\nWith multiple lines", string(newContent))
	}

	// Check if the file was moved with correct content
	movedContent, err := os.ReadFile(movedFilePath)
	if err != nil {
		t.Errorf("Failed to read moved file: %v", err)
	} else if !strings.Contains(string(movedContent), "Line 3 (modified)") {
		t.Errorf("Moved file content not correct:\nExpected to contain: %q\nGot: %q",
			"Line 3 (modified)", string(movedContent))
	}

	// Check if the original file no longer exists
	_, err = os.Stat(existingFilePath)
	if !os.IsNotExist(err) {
		t.Errorf("Original file should no longer exist")
	}
}

// Test the full patch processing pipeline with a valid patch string
func TestFullPatchProcessing(t *testing.T) {
	// Skip if not in a normal test run to avoid filesystem changes
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "patch-test")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	existingFilePath := filepath.Join(tempDir, "existing.txt")
	existingContent := "Line 1\nLine 2\nLine 3\nLine 4\n"
	err = os.WriteFile(existingFilePath, []byte(existingContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Define a patch that will:
	// 1. Update the existing file
	// 2. Create a new file
	patchText := `*** Begin Patch
*** Add File: ` + filepath.Join(tempDir, "new.txt") + `
+ This is a new file
+ With multiple lines
*** End Patch`

	// Process the patch
	results, err := ProcessPatch(patchText)
	if err != nil {
		t.Fatalf("Failed to process patch: %v", err)
	}

	// Check the results
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	// Check if the new file was created
	newFilePath := filepath.Join(tempDir, "new.txt")
	newContent, err := os.ReadFile(newFilePath)
	if err != nil {
		t.Errorf("Failed to read new file: %v", err)
	} else if string(newContent) != "This is a new file\nWith multiple lines" {
		t.Errorf("New file content not correct:\nExpected: %q\nGot: %q",
			"This is a new file\nWith multiple lines", string(newContent))
	}
}

func TestSimplePatchToRobustPatch(t *testing.T) {
	// Define a simple patch
	simplePatchText := `*** Begin Patch
*** Add File: newfile.txt
+ Line 1
+ Line 2
*** Update File: existingfile.txt
Context line 1
- Old line
+ New line
Context line 2
*** Delete File: oldfile.txt
*** End Patch`

	// Parse the simple patch
	operations, err := ParseSimplePatch(simplePatchText)
	if err != nil {
		t.Fatalf("Failed to parse simple patch: %v", err)
	}

	// Convert to robust patch format
	robustPatchText := ConvertToCustomPatchFormat(operations)

	// Ensure it contains all expected markers
	expectedMarkers := []string{
		PatchBeginMarker,
		AddFilePrefix + "newfile.txt",
		"+Line 1",
		"+Line 2",
		UpdateFilePrefix + "existingfile.txt",
		" Context line 1",
		" Context line 2",
		"-Old line",
		"+New line",
		EndOfFileMarker,
		DeleteFilePrefix + "oldfile.txt",
		PatchEndMarker,
	}

	for _, marker := range expectedMarkers {
		if !strings.Contains(robustPatchText, marker) {
			t.Errorf("Expected robust patch to contain %q, but it doesn't", marker)
		}
	}
}
