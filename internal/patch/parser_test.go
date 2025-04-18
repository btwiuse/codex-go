package patch

import (
	"strings"
	"testing"
)

func TestTextToPatch(t *testing.T) {
	// Define a basic patch
	patchText := `*** Begin Patch
*** Update File: testfile.txt
 Line 1
 Line 2
-Line 3
+Line 3 modified
 Line 4
*** End Patch`

	// Create a mock file system
	mockFiles := map[string]string{
		"testfile.txt": "Line 1\nLine 2\nLine 3\nLine 4",
	}

	// Parse the patch
	patch, fuzz, err := TextToPatch(patchText, mockFiles)
	if err != nil {
		t.Fatalf("Failed to parse patch: %v", err)
	}

	// Check if the patch was parsed correctly
	if len(patch.Actions) != 1 {
		t.Errorf("Expected 1 action, got %d", len(patch.Actions))
	}

	// Check the action
	action, ok := patch.Actions["testfile.txt"]
	if !ok {
		t.Fatalf("Action for testfile.txt not found")
	}

	if action.Type != ActionUpdate {
		t.Errorf("Expected action type %s, got %s", ActionUpdate, action.Type)
	}

	if len(action.Chunks) != 1 {
		t.Fatalf("Expected 1 chunk, got %d", len(action.Chunks))
	}

	// Check the chunk
	chunk := action.Chunks[0]
	if chunk.OrigIndex != 2 { // 0-indexed
		t.Errorf("Expected original index 2, got %d", chunk.OrigIndex)
	}

	if len(chunk.DelLines) != 1 || chunk.DelLines[0] != "Line 3" {
		t.Errorf("Deleted lines not correct: %v", chunk.DelLines)
	}

	if len(chunk.InsLines) != 1 || chunk.InsLines[0] != "Line 3 modified" {
		t.Errorf("Inserted lines not correct: %v", chunk.InsLines)
	}

	// Check fuzz level
	if fuzz != 0 {
		t.Errorf("Expected fuzz level 0, got %d", fuzz)
	}
}

func TestUpdateFileWithChunks(t *testing.T) {
	// Original file content
	text := "Line 1\nLine 2\nLine 3\nLine 4"

	// Create a patch action
	action := PatchAction{
		Type: ActionUpdate,
		Chunks: []Chunk{
			{
				OrigIndex: 2, // 0-indexed, Line 3
				DelLines:  []string{"Line 3"},
				InsLines:  []string{"Line 3 modified"},
			},
		},
	}

	// Apply the chunks
	result, err := UpdateFileWithChunks(text, action, "testfile.txt")
	if err != nil {
		t.Fatalf("Failed to update file with chunks: %v", err)
	}

	// Expected result
	expected := "Line 1\nLine 2\nLine 3 modified\nLine 4"

	// Check if the result is correct
	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

func TestParseSimplePatch(t *testing.T) {
	// Define a simple patch
	patchText := `*** Begin Patch
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

	// Parse the patch
	operations, err := ParseSimplePatch(patchText)
	if err != nil {
		t.Fatalf("Failed to parse simple patch: %v", err)
	}

	// Check if the operations were parsed correctly
	if len(operations) != 3 {
		t.Errorf("Expected 3 operations, got %d", len(operations))
	}

	// Check the add operation
	if operations[0].Type != "add" || operations[0].Path != "newfile.txt" {
		t.Errorf("Add operation not parsed correctly: %s, %s", operations[0].Type, operations[0].Path)
	}
	expectedContent := "Line 1\nLine 2"
	if operations[0].Content != expectedContent {
		t.Errorf("Add operation content not correct:\nExpected: %q\nGot: %q", expectedContent, operations[0].Content)
	}

	// Check the update operation
	if operations[1].Type != "update" || operations[1].Path != "existingfile.txt" {
		t.Errorf("Update operation not parsed correctly: %s, %s", operations[1].Type, operations[1].Path)
	}
	if len(operations[1].Context) != 2 || operations[1].Context[0] != "Context line 1" || operations[1].Context[1] != "Context line 2" {
		t.Errorf("Update operation context not correct: %v", operations[1].Context)
	}
	if len(operations[1].DelLines) != 1 || operations[1].DelLines[0] != "Old line" {
		t.Errorf("Update operation deleted lines not correct: %v", operations[1].DelLines)
	}
	if len(operations[1].AddLines) != 1 || operations[1].AddLines[0] != "New line" {
		t.Errorf("Update operation added lines not correct: %v", operations[1].AddLines)
	}

	// Check the delete operation
	if operations[2].Type != "delete" || operations[2].Path != "oldfile.txt" {
		t.Errorf("Delete operation not parsed correctly: %s, %s", operations[2].Type, operations[2].Path)
	}
}

func TestConvertToCustomPatchFormat(t *testing.T) {
	// Create a slice of PatchOperation
	operations := []*PatchOperation{
		{
			Type:    "add",
			Path:    "newfile.txt",
			Content: "Line 1\nLine 2",
		},
		{
			Type:     "update",
			Path:     "existingfile.txt",
			Context:  []string{"Context line 1", "Context line 2"},
			DelLines: []string{"Old line"},
			AddLines: []string{"New line"},
		},
		{
			Type: "delete",
			Path: "oldfile.txt",
		},
	}

	// Convert to custom patch format
	patchText := ConvertToCustomPatchFormat(operations)

	// Make sure it contains the expected markers
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
		if !strings.Contains(patchText, marker) {
			t.Errorf("Expected patch to contain %q, but it doesn't", marker)
		}
	}
}
