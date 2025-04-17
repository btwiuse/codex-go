package tests

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/epuerta/codex-go/internal/agent"
	"github.com/epuerta/codex-go/internal/config"
	"github.com/epuerta/codex-go/internal/logging"
)

func TestOpenAIAgent(t *testing.T) {
	// Skip if no API key is provided
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping test: OPENAI_API_KEY not set")
	}

	// Create a config
	cfg := &config.Config{
		APIKey:       apiKey,
		Model:        "gpt-3.5-turbo",
		APITimeout:   30,
		ApprovalMode: config.Suggest,
	}

	// Create a nil logger for testing
	logger := logging.NewNilLogger()

	// Create an OpenAI agent
	openaiAgent, err := agent.NewOpenAIAgent(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create OpenAI agent: %v", err)
	}

	// Test sending a message
	messages := []agent.Message{
		{
			Role:    "system",
			Content: "You are a helpful assistant. Respond with a short greeting.",
		},
		{
			Role:    "user",
			Content: "Hello!",
		},
	}

	// Set up a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a channel to collect response items
	respChan := make(chan agent.ResponseItem)
	var responses []agent.ResponseItem

	// Send the message in a goroutine
	go func() {
		defer close(respChan)
		// Convert our handler to accept string JSON
		jsonHandler := func(jsonStr string) {
			var item agent.ResponseItem
			if err := json.Unmarshal([]byte(jsonStr), &item); err != nil {
				t.Errorf("Error unmarshalling response item: %v", err)
				return
			}
			respChan <- item
		}
		_, err := openaiAgent.SendMessage(ctx, messages, jsonHandler)
		if err != nil {
			t.Errorf("Error sending message: %v", err)
		}
	}()

	// Collect responses from the channel
	for item := range respChan {
		responses = append(responses, item)
	}

	// Check that we got at least one response
	if len(responses) == 0 {
		t.Errorf("No responses received")
	}

	// Check for message content in the responses
	hasMessage := false
	for _, resp := range responses {
		if resp.Type == "message" && resp.Message != nil {
			hasMessage = true
			break
		}
	}

	if !hasMessage {
		t.Errorf("No message in responses")
	}
}

func TestAgentMock(t *testing.T) {
	// TODO: Implement a mock agent test that doesn't require an API key
	// This is a placeholder for future mock testing
	t.Skip("Mock agent test not implemented yet")
}
