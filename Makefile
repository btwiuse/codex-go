.PHONY: build test lint clean install run help

# Build settings
BINARY_NAME=codex
GO=go
BUILD_DIR=bin
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.GitCommit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

help:
	@echo "Codex-Go Makefile"
	@echo "Available targets:"
	@echo "  build       - Build the binary"
	@echo "  test        - Run tests"
	@echo "  lint        - Run linters"
	@echo "  clean       - Clean build artifacts"
	@echo "  install     - Install the binary"
	@echo "  run         - Run the application (with prompt if provided)"
	@echo "  help        - Show this help message"

build:
	@echo "Building Codex-Go..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codex

test:
	@echo "Running tests..."
	$(GO) test -v ./...

test-unit:
	@echo "Running unit tests only..."
	$(GO) test -v -tags=unit ./...

test-integration:
	@echo "Running integration tests..."
	$(GO) test -v -tags=integration ./...

test-coverage:
	@echo "Generating test coverage report..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html

lint:
	@echo "Running linters..."
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; $(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; }
	golangci-lint run

clean:
	@echo "Cleaning up..."
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

install: build
	@echo "Installing Codex-Go..."
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

run:
	@echo "Running Codex-Go..."
	@if [ -z "$(PROMPT)" ]; then \
		$(GO) run cmd/codex/main.go; \
	else \
		$(GO) run cmd/codex/main.go "$(PROMPT)"; \
	fi

# Default target
default: build 
