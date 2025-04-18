# Codex-Go

An experimental AI coding assistant that runs in your terminal, modeled after the concepts of [openai/codex](https://github.com/openai/codex).

**Status:** This project is in an early, experimental phase and under active development. Expect breaking changes and potential instability.

**Note:** This project is part of CloudshipAI's mission to help make OSS tools for AI developers and engineers. [Learn more about CloudshipAI](https://cloudshipai.com). This is a tool we plan to incorporate into our platform and want to make it available to the community.

## Overview

Codex-Go aims to provide a terminal-based coding assistant implemented in Go, with the following long-term goals:

1.  **Core Functionality (Go Library):** A robust Go library for interacting with LLMs (like OpenAI's models) for code generation, explanation, and modification tasks.
2.  **In-Terminal Agent:** A user-friendly terminal application (powered by Bubble Tea) allowing developers to chat with the agent, request code changes, execute commands, and get help with their codebase.
3.  **MCP Server:** Integration as a [Mission Control Platform (MCP)](https://example.com/mcp-explanation) server component, enabling its capabilities to be used within broader operational workflows (details TBD).
4.  **Library Parity:** Achieve functional parity with the original `openai/codex` reference where applicable, focusing on file operations and command execution within a secure sandbox.

Currently, the primary focus is on the **In-Terminal Agent**.

## Features (Current & Planned)

-   Interact with an AI coding assistant directly in your terminal.
-   Ask questions about your code.
-   Request code generation or modification.
-   Safely execute shell commands proposed by the AI (with user approval).
-   Apply file patches proposed by the AI (with user approval).
-   Context-aware assistance using project documentation (`codex.md`).
-   Configurable safety levels (approval modes).

## Installation

### 1. From Release (Recommended)

Pre-built binaries for Linux, macOS, and Windows are available on the [GitHub Releases](https://github.com/epuerta9/codex-go/releases) page.

1.  Go to the [Latest Release](https://github.com/epuerta9/codex-go/releases/latest).
2.  Download the appropriate archive (`.tar.gz` or `.zip`) for your operating system and architecture.
3.  Extract the `codex-go` binary from the archive.
4.  (Optional but recommended) Move the `codex-go` binary to a directory included in your system's `PATH` (e.g., `/usr/local/bin`, `~/bin`).

    ```bash
    # Example for Linux/macOS:
    mv codex-go /usr/local/bin/
    chmod +x /usr/local/bin/codex-go
    ```

### 2. Building from Source

#### Prerequisites

-   Go 1.21 or higher ([Installation Guide](https://go.dev/doc/install))
-   Git

#### Steps

```bash
# Clone the repository
git clone https://github.com/epuerta9/codex-go.git
cd codex-go

# Build the binary (output will be named 'codex-go' in the current directory)
go build -o codex-go ./cmd/codex

# (Optional) Install to your Go bin path
go install ./cmd/codex

# (Optional) Or move the built binary to your preferred location
# mv codex-go /usr/local/bin/
```

## Configuration

1.  **OpenAI API Key:**
    Codex-Go requires an OpenAI API key. Set it as an environment variable:
    ```bash
    export OPENAI_API_KEY="your-api-key-here"
    ```
    Add this line to your shell configuration file (e.g., `.bashrc`, `.zshrc`, `.profile`) for persistence.

2.  **(Optional) Configuration File (`~/.codex/config.yaml`):**
    You can customize default behavior:
    ```yaml
    # Example ~/.codex/config.yaml
    model: gpt-4o-mini # Default model
    approval_mode: suggest # Default approval mode (suggest, auto-edit, full-auto)
    # log_file: ~/.codex/codex-go.log # Uncomment to enable file logging
    # log_level: debug # Log level (debug, info, warn, error)
    # disable_project_doc: false # Set to true to ignore codex.md files
    ```

3.  **(Optional) Custom Instructions (`~/.codex/instructions.md`):**
    Provide persistent custom instructions to the AI agent by creating this file.
    ```markdown
    # Example ~/.codex/instructions.md
    Always format Go code using gofmt.
    Keep responses concise.
    ```

4.  **(Optional) Project Context (`codex.md`):**
    Place `codex.md` files in your project for context:
    -   `codex.md` at the repository root (found via `.git` directory).
    -   `codex.md` in the current working directory.
    Both will be included if found (unless disabled via config or flag).

## Usage

### Interactive Mode

Start the application without arguments:

```bash
codex-go
```

Chat with the assistant. Press `Enter` to send your message.

**Keybindings:**

-   `Enter`: Send message.
-   `Ctrl+T`: Toggle message timestamps.
-   `Ctrl+S`: Toggle system/debug messages.
-   `/clear`: Clear the current conversation history.
-   `/help`: Show command help.
-   `Ctrl+C` or `Esc` or `q` (when input empty): Quit.

### Direct Prompt Mode (Quiet)

Execute a single prompt non-interactively:

```bash
codex-go -q "Refactor this Go function to improve readability: [paste code here]"
```
The response will be printed directly to standard output.

### Flags

-   `--model`, `-m`: Specify the model (e.g., `gpt-4o`, `gpt-4o-mini`).
-   `--approval-mode`, `-a`: Set approval mode (`suggest`, `auto-edit`, `full-auto`).
-   `--quiet`, `-q`: Use non-interactive mode (requires a prompt).
-   `--no-project-doc`: Don't include `codex.md` files.
-   `--project-doc <path>`: Include an additional specific markdown file as context.
-   `--config <path>`: Specify a path to a config file (overrides default `~/.codex/config.yaml`).
-   `--instructions <path>`: Specify a path to an instructions file (overrides default `~/.codex/instructions.md`).
-   `--log-file <path>`: Specify a log file path.
-   `--log-level <level>`: Set log level (`debug`, `info`, `warn`, `error`).

## Security & Approval Modes

Control the agent's autonomy with `--approval-mode`:

| Mode          | Allows without asking                | Requires approval                       |
|---------------|--------------------------------------|-----------------------------------------|
| **suggest**   | Read files, List directories         | File writes/patches, Command execution  |
| **auto-edit** | Read files, Apply file patches       | Command execution                       |
| **full-auto** | Read files, Apply patches, Execute commands | ---                                     |

**Note:** `full-auto` mode can execute *any* command the AI suggests without confirmation. Use with extreme caution.

Commands are executed within a sandbox environment (using platform features like `sandbox-exec` on macOS where possible) to limit potential harm, but caution is always advised.

## Development

(See [CONTRIBUTING.md](CONTRIBUTING.md) - *if you create one*)

### Running Tests

```bash
go test ./...
```

### Using the Makefile

```bash
make build
make test
make run PROMPT="Explain Go interfaces"
make help
```

## License

Apache-2.0
