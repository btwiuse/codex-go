# Codex-Go

A Go port of the OpenAI Codex CLI - a lightweight coding agent that runs in your terminal.
4/18/2025 : WIP This project is under heavy development. Working on releasing a working build shortly with an updated read me

- making it MCP server
- Library
- parity with original CODEX 

## Overview

Codex-Go is a terminal-based coding assistant that can:

- Answer questions about code
- Write and execute code for you
- Make changes to your codebase
- Explain concepts and provide solutions

It's implemented in Go with a focus on performance, security, and extensibility.

## Installation

### Prerequisites

- Go 1.18 or higher
- OpenAI API Key

### Building from source

```bash
# Clone the repository
git clone https://github.com/epuerta9/codex-go.git
cd codex-go

# Build the binary
go build -o codex ./cmd/codex

# Make executable
chmod +x codex

# Move to a location in your PATH (optional)
sudo mv codex /usr/local/bin/
```

## Usage

### Set your OpenAI API key

Make sure your `OPENAI_API_KEY` environment variable is set:

```bash
export OPENAI_API_KEY="your-api-key-here"
```

You can add this to your `.bashrc` or `.zshrc` file for persistence.

### Running Codex-Go

There are two primary ways to run the application:

1.  **Interactive Mode:**
    Start the application without any arguments to enter the interactive chat mode:
    ```bash
    codex
    ```
    In this mode, you can have a back-and-forth conversation with the AI. Type your message and press Enter.

2.  **Direct Prompt Mode (Quiet Mode):**
    Provide a prompt directly using the `-q` or `--quiet` flag:
    ```bash
    codex -q "Write a Go function to parse JSON"
    ```
    The AI will process the prompt and print the final response to standard output. This is useful for quick tasks or scripting.

### Interactive Mode Keybindings

While in interactive mode, you can use the following keybindings:

-   `Enter`: Send the message currently typed in the input box.
-   `Ctrl+T`: Toggle the display of timestamps for each message.
-   `Ctrl+S`: Toggle the display of system messages (like initial prompts or debug messages).
-   `Ctrl+X`: Clear the current conversation history. This will start a fresh context for the AI.
-   `Ctrl+C` or `Esc`: Quit the application.

### Flags

- `--model, -m`: Specify the model to use (default: "gpt-4o").
- `--approval-mode, -a`: Set the approval mode: "suggest", "auto-edit", or "full-auto".
- `--quiet, -q`: Use non-interactive mode (requires a prompt).
- `--image, -i`: Include image file(s) as input (not fully implemented yet).
- `--no-project-doc`: Don't include the repository's codex.md file.
- `--project-doc`: Include an additional markdown file as context.
- `--full-stdout`: Don't truncate command outputs.

## Security & Approval Modes

Codex-Go lets you decide how much autonomy the agent receives through the `--approval-mode` flag:

| Mode                      | What the agent may do without asking            | Still requires approval                                         |
| ------------------------- | ----------------------------------------------- | --------------------------------------------------------------- |
| **Suggest** <br>(default) | • Read any file in the repo                     | • **All** file writes/patches <br>• **All** shell/Bash commands |
| **Auto Edit**             | • Read **and** apply‑patch writes to files      | • **All** shell/Bash commands                                   |
| **Full Auto**             | • Read/write files <br>• Execute shell commands | –                                                               |

In all modes, shell commands are run with restricted environments for safety, and different sandboxing methods are used depending on your platform:

- **macOS**: Uses `sandbox-exec` to restrict file access and network connectivity.
- **Linux**: Uses environment restrictions and directory isolation.
- **Other platforms**: Uses basic sandboxing with environment and directory restrictions.

## Configuration

Codex-Go looks for configuration files in `~/.codex/`:

- `config.yaml`: Basic configuration (e.g., default model, approval mode).
  ```yaml
  # Example ~/.codex/config.yaml
  model: gpt-4o
  approval_mode: suggest
  ```
- `instructions.md`: Custom instructions prepended to the system prompt for the AI.
  ```markdown
  # Example ~/.codex/instructions.md
  Always format code blocks using markdown.
  Be concise.
  ```

Codex-Go also supports project-specific documentation through `codex.md` files:

1. `codex.md` at repository root - General project context
2. `codex.md` in current directory - Local directory-specific context

## Development

### Running tests

```bash
# Run all tests
go test ./...

# Run specific tests
go test ./internal/agent/... 
```

### Using the Makefile

The project includes a Makefile for common tasks:

```bash
# Build the binary
make build

# Run tests
make test

# Run linters (if configured)
# make lint 

# Clean build artifacts
make clean

# Run the application (interactive)
make run

# Run the application (quiet mode with prompt)
make run PROMPT="Explain Go interfaces"

# Show help
make help
```

### Project structure

- `cmd/codex`: Command-line interface (main, root command, app model).
- `internal/agent`: AI agent implementation (OpenAI client, history management).
- `internal/config`: Configuration loading and handling.
- `internal/ui`: Terminal UI components (Bubble Tea chat model, approval UI).
- `internal/sandbox`: Secure command execution (platform-specific sandboxing).
- `internal/fileops`: File operations.

## License

Apache-2.0 License 

from the new codex
