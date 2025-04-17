# Testing Guide for Codex-Go

This document provides instructions on how to run and write tests for the Codex-Go project.

## Running Tests

### Prerequisites

To run the tests, you need:

1. Go 1.18 or higher installed
2. OpenAI API key (for integration tests)

### Running All Tests

```bash
# From the project root
go test ./...
```

### Running Specific Tests

```bash
# Run specific test package
go test ./tests

# Run specific test file
go test ./tests/agent_test.go

# Run specific test function
go test -run TestOpenAIAgent ./tests
```

### Test Tags

Some tests are tagged to control when they run:

```bash
# Run only unit tests (no external dependencies)
go test -tags=unit ./...

# Run integration tests (requires API keys)
go test -tags=integration ./...
```

## Writing Tests

### Test Types

#### Unit Tests

Unit tests should be self-contained and not rely on external services. They should be fast and reliable.

Example:

```go
func TestConfig(t *testing.T) {
    // Test configuration loading
    cfg, err := config.Load()
    assert.NoError(t, err)
    assert.NotNil(t, cfg)
}
```

#### Integration Tests

Integration tests can use external services like the OpenAI API. They should be skipped if the necessary credentials are not available.

Example:

```go
func TestOpenAIAgent(t *testing.T) {
    // Skip if no API key is provided
    apiKey := os.Getenv("OPENAI_API_KEY")
    if apiKey == "" {
        t.Skip("Skipping test: OPENAI_API_KEY not set")
    }
    
    // Test implementation...
}
```

### Mocking

For tests that require external services but need to run without credentials, use mocks:

1. Define interfaces for the dependencies
2. Create mock implementations of those interfaces
3. Use the mocks in tests

Example:

```go
type MockAgent struct {
    agent.Agent // Embed the interface
}

func (m *MockAgent) SendMessage(ctx context.Context, messages []agent.Message, handler agent.ResponseHandler) error {
    // Mock implementation
    handler(agent.ResponseItem{
        Type: "message",
        Message: &agent.Message{
            Role: "assistant",
            Content: "Mock response",
        },
    })
    return nil
}
```

## Test Coverage

To generate a test coverage report:

```bash
# Generate coverage profile
go test -coverprofile=coverage.out ./...

# View coverage in terminal
go tool cover -func=coverage.out

# Generate HTML coverage report
go tool cover -html=coverage.out -o coverage.html
```

## Continuous Integration

The CI pipeline runs tests on each pull request. Tests are run on multiple platforms:

- Linux
- macOS
- Windows

To ensure your tests pass in CI:

1. Don't depend on specific environment variables being set
2. Skip integration tests if required credentials are missing
3. Use path separators that work cross-platform
4. Be mindful of resource usage and timeouts

## Troubleshooting

### Common Issues

1. **API Rate Limits**: If you hit OpenAI API rate limits, add delays between tests or reduce the number of API calls.

2. **Test Timeouts**: Some tests may take longer than the default timeout. You can increase the timeout:

   ```bash
   go test -timeout 5m ./...
   ```

3. **Environment Variables**: Make sure all required environment variables are set:

   ```bash
   export OPENAI_API_KEY="your-api-key"
   go test ./...
   ``` 