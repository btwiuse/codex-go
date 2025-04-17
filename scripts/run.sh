#!/bin/bash

# Ensure the script directory exists
mkdir -p scripts

# Set the API key if provided as an argument
if [ ! -z "$1" ]; then
  export OPENAI_API_KEY="$1"
fi

# Check if OPENAI_API_KEY is set
if [ -z "$OPENAI_API_KEY" ]; then
  echo "Error: OPENAI_API_KEY environment variable is not set"
  echo "Please set your OpenAI API key: export OPENAI_API_KEY=your-api-key-here"
  echo "Or provide it as an argument: ./scripts/run.sh your-api-key-here"
  exit 1
fi

# Get the prompt if provided as the second argument
PROMPT=""
if [ ! -z "$2" ]; then
  PROMPT="$2"
fi

# Run the application
if [ -z "$PROMPT" ]; then
  go run cmd/codex/main.go
else
  go run cmd/codex/main.go "$PROMPT" 