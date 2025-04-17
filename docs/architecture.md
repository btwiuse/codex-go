# Codex Architecture Overview

## Current TypeScript Architecture

Based on the analysis of the codex project, here's an overview of its architecture:

### Core Components

1. **CLI Interface**
   - Entry point: `cli.tsx` - handles CLI commands, arguments, and options
   - Validation of API keys and configuration
   - Multiple modes: interactive, quiet, full-context

2. **Agent Loop**
   - Core class: `AgentLoop` in `agent-loop.ts`
   - Manages interactions with the OpenAI API
   - Processes function calls from the model
   - Handles streaming responses and cancellation

3. **Command Execution**
   - Sandbox mechanism for executing commands safely
   - Platform-specific implementation (macOS Seatbelt, container for Linux)
   - Command approval workflow

4. **File Modification**
   - Ability to read, write, and modify files
   - Uses patches for file modifications
   - Versioning and approval workflow

5. **UI Components**
   - Terminal-based UI using Ink/React
   - Chat interface and message history
   - Interactive command approval
   - Various overlays (help, model selection, history)

6. **Configuration**
   - User configuration in `~/.codex/`
   - Project-specific instructions in `codex.md`
   - Model selection and approval policy settings

### Data Flow

1. User inputs a prompt via CLI
2. CLI validates and sets up the environment
3. The agent loop sends the prompt to OpenAI API
4. The model responds with text or function calls
5. Function calls are parsed and executed (if approved)
6. Results are displayed to the user
7. The process continues until completion or user cancellation

## Go Implementation Architecture

### Core Components

1. **CLI Interface**
   - Using Cobra for CLI commands and flags
   - Similar command structure to the TypeScript version
   - Flags for model selection, approval policy, etc.

2. **Agent System**
   - Go equivalent of `AgentLoop`
   - Interface for multiple AI providers (OpenAI, Anthropic, etc.)
   - Streaming response handling using Go channels

3. **Command Execution**
   - Secure sandboxing using Go's `exec` package with restrictions
   - Platform-specific isolation (similar to the TypeScript version)
   - Command approval workflow

4. **File Operations**
   - File reading, writing, and modification
   - Diff generation and application
   - Path resolution and security checks

5. **TUI (Terminal User Interface)**
   - Using Charm Bubble Tea for terminal UI
   - Stateful components and event handling
   - Interactive command approval
   - Help and configuration screens

6. **Configuration**
   - Similar directory structure (`~/.codex/`)
   - YAML configuration parsing
   - Environment variable handling

### Proposed Package Structure

```
codex-go/
├── cmd/
│   └── codex/
│       └── main.go           # CLI entry point
├── internal/
│   ├── agent/                # Agent implementation
│   │   ├── openai.go         # OpenAI implementation
│   │   ├── anthropic.go      # Anthropic implementation
│   │   └── interface.go      # Common agent interface
│   ├── config/               # Configuration handling
│   │   ├── config.go
│   │   └── loader.go
│   ├── sandbox/              # Command execution sandbox
│   │   ├── sandbox.go
│   │   ├── macos.go
│   │   └── linux.go
│   ├── ui/                   # Terminal UI components
│   │   ├── chat.go
│   │   ├── approval.go
│   │   └── help.go
│   ├── fileops/              # File operations
│   │   ├── diff.go
│   │   └── patch.go
│   └── utils/                # Common utilities
│       └── session.go
├── pkg/                      # Public packages for library use
│   ├── agent/                # Public agent API
│   ├── config/               # Public configuration API
│   └── sandbox/              # Public sandbox API
└── tests/                    # Test suite
```

### Key Differences and Considerations

1. **Language-Specific Patterns**
   - Go's error handling vs TypeScript's promises/async-await
   - Go's struct-based design vs TypeScript's class-based approach
   - Go's strong typing and lack of generics (in older versions)

2. **Libraries and Dependencies**
   - Cobra CLI instead of meow
   - Bubble Tea instead of Ink/React
   - Go-specific OpenAI client

3. **Performance Considerations**
   - Go's compile-time checking and performance benefits
   - Concurrency using goroutines and channels
   - Memory management differences

4. **Testing Approach**
   - Table-driven tests common in Go
   - Mocking the OpenAI API for unit tests
   - End-to-end test strategy

### Implementation Priorities

1. Create the CLI framework with Cobra
2. Implement the configuration system
3. Create the agent interface and OpenAI implementation
4. Build the sandbox for secure command execution
5. Implement file operations
6. Develop the TUI with Bubble Tea
7. Add comprehensive tests
8. Implement additional providers (Anthropic, etc.) 