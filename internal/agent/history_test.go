package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewConversationHistory(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "history-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test options
	opts := HistoryOptions{
		MaxTokenCount: 1000,
		SessionID:     "test-session",
		HistoryPath:   tempDir,
		EnablePersist: true,
		SystemPrompt:  "Test system prompt",
	}

	// Create new conversation history
	history, err := NewConversationHistory(opts)
	if err != nil {
		t.Fatalf("Failed to create conversation history: %v", err)
	}

	// Check if the system prompt was added
	if len(history.Messages) != 1 {
		t.Fatalf("Expected 1 message (system prompt), got %d", len(history.Messages))
	}

	if history.Messages[0].Role != "system" || history.Messages[0].Content != opts.SystemPrompt {
		t.Errorf("System message not added correctly")
	}

	// Check other fields
	if history.MaxTokenCount != opts.MaxTokenCount {
		t.Errorf("Expected MaxTokenCount=%d, got %d", opts.MaxTokenCount, history.MaxTokenCount)
	}

	if history.CurrentSession != opts.SessionID {
		t.Errorf("Expected CurrentSession=%s, got %s", opts.SessionID, history.CurrentSession)
	}
}

func TestAddMessage(t *testing.T) {
	// Create a basic history
	history := &ConversationHistory{
		Messages:       []Message{},
		MaxTokenCount:  1000,
		CurrentSession: "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Add a message
	message := Message{
		Role:    "user",
		Content: "Hello, world!",
	}
	history.AddMessage(message)

	// Check if the message was added
	if len(history.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(history.Messages))
	}

	if history.Messages[0].Role != message.Role || history.Messages[0].Content != message.Content {
		t.Errorf("Message not added correctly")
	}

	// Add another message
	message2 := Message{
		Role:    "assistant",
		Content: "Hi there!",
	}
	history.AddMessage(message2)

	// Check if both messages are present
	if len(history.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(history.Messages))
	}

	if history.Messages[1].Role != message2.Role || history.Messages[1].Content != message2.Content {
		t.Errorf("Second message not added correctly")
	}
}

func TestPruneIfNeeded(t *testing.T) {
	// Create a history with a small token limit
	history := &ConversationHistory{
		Messages:       []Message{},
		MaxTokenCount:  20, // Very small limit to trigger pruning
		CurrentSession: "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Add a system message
	systemMsg := Message{
		Role:    "system",
		Content: "You are a helpful assistant.",
	}
	history.AddMessage(systemMsg)

	// Add several messages to exceed the token limit
	for i := 0; i < 5; i++ {
		userMsg := Message{
			Role:    "user",
			Content: "This is a test message that should exceed the token limit.",
		}
		assistantMsg := Message{
			Role:    "assistant",
			Content: "This is a response that should also contribute to exceeding the token limit.",
		}
		history.AddMessages([]Message{userMsg, assistantMsg})
	}

	// Check that pruning occurred
	if len(history.Messages) >= 12 { // 1 system + 10 messages = 11
		t.Errorf("Expected pruning to reduce message count, but got %d messages", len(history.Messages))
	}

	// Ensure system message is still present
	foundSystem := false
	for _, msg := range history.Messages {
		if msg.Role == "system" {
			foundSystem = true
			break
		}
	}
	if !foundSystem {
		t.Errorf("System message was removed during pruning")
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "history-save-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a session ID
	sessionID := "test-save-session"

	// Create test options
	opts := HistoryOptions{
		MaxTokenCount: 1000,
		SessionID:     sessionID,
		HistoryPath:   tempDir,
		EnablePersist: true,
	}

	// Create new conversation history
	history, err := NewConversationHistory(opts)
	if err != nil {
		t.Fatalf("Failed to create conversation history: %v", err)
	}

	// Add some messages
	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello, world!"},
		{Role: "assistant", Content: "Hi there! How can I help you today?"},
	}

	for _, msg := range messages {
		history.AddMessage(msg)
	}

	// Save the history
	if err := history.Save(tempDir); err != nil {
		t.Fatalf("Failed to save history: %v", err)
	}

	// Check if the file exists
	historyFile := filepath.Join(tempDir, sessionID+".json")
	if _, err := os.Stat(historyFile); os.IsNotExist(err) {
		t.Fatalf("History file %s was not created", historyFile)
	}

	// Create a new history instance with the same options to load the saved data
	loadedOpts := HistoryOptions{
		MaxTokenCount: 1000,
		SessionID:     sessionID,
		HistoryPath:   tempDir,
		EnablePersist: true,
	}

	loadedHistory, err := NewConversationHistory(loadedOpts)
	if err != nil {
		t.Fatalf("Failed to create history for loading: %v", err)
	}

	// Check if the messages were loaded correctly
	if len(loadedHistory.Messages) != len(messages) {
		t.Fatalf("Expected %d messages, got %d", len(messages), len(loadedHistory.Messages))
	}

	for i, msg := range loadedHistory.Messages {
		if msg.Role != messages[i].Role || msg.Content != messages[i].Content {
			t.Errorf("Message %d not loaded correctly", i)
		}
	}
}

func TestEstimateTokenCount(t *testing.T) {
	// Create a basic history
	history := &ConversationHistory{
		Messages:       []Message{},
		MaxTokenCount:  1000,
		CurrentSession: "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Add messages with known content length
	messages := []Message{
		{Role: "system", Content: "A message with 24 characters"},          // ~6 tokens + 4 overhead
		{Role: "user", Content: "A slightly longer message with 39 chars"}, // ~10 tokens + 4 overhead
		{Role: "assistant", Content: "A short reply"},                      // ~3 tokens + 4 overhead
	}

	for _, msg := range messages {
		history.AddMessage(msg)
	}

	// Estimate tokens
	tokenCount := history.EstimateTokenCount()

	// Check that the estimate is reasonable (this is approximate)
	// 6 + 4 + 10 + 4 + 3 + 4 = roughly 31 tokens
	expectedMinimum := 20 // Lower bound
	expectedMaximum := 40 // Upper bound

	if tokenCount < expectedMinimum || tokenCount > expectedMaximum {
		t.Errorf("Token count estimate %d outside expected range %d-%d",
			tokenCount, expectedMinimum, expectedMaximum)
	}
}

func TestClear(t *testing.T) {
	// Create a history with some messages
	history := &ConversationHistory{
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello, world!"},
			{Role: "assistant", Content: "Hi there! How can I help you today?"},
		},
		MaxTokenCount:  1000,
		CurrentSession: "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Clear the history
	history.Clear()

	// Check that all messages are removed
	if len(history.Messages) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(history.Messages))
	}
}
