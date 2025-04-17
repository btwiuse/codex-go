package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileLogger implements the Logger interface, writing logs asynchronously to a file.
type FileLogger struct {
	logChan chan string
	file    *os.File
	waiter  sync.WaitGroup
	mu      sync.Mutex // Protects file handle during close
}

// NewFileLogger creates a new logger that writes to the specified file path.
// It creates the directory if it doesn't exist.
func NewFileLogger(filePath string) (*FileLogger, error) {
	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", dir, err)
	}

	// Open the file for appending, create if it doesn't exist
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", filePath, err)
	}

	logger := &FileLogger{
		logChan: make(chan string, 100), // Buffered channel
		file:    f,
	}

	// Start the background writer goroutine
	logger.waiter.Add(1)
	go logger.writer()

	return logger, nil
}

// writer runs in a background goroutine, reading from logChan and writing to the file.
func (l *FileLogger) writer() {
	defer l.waiter.Done()
	for msg := range l.logChan {
		l.mu.Lock()
		if l.file != nil { // Check if file is still open
			_, _ = l.file.WriteString(msg) // Ignore write errors for now
		}
		l.mu.Unlock()
	}
	// Channel closed, flush any remaining writes if necessary (though buffered channel helps)
}

// Log formats the message and sends it to the log channel.
func (l *FileLogger) Log(format string, args ...interface{}) {
	// Format the message with a timestamp
	now := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	msg := fmt.Sprintf("[%s] %s\n", now, fmt.Sprintf(format, args...))

	// Send to the channel (non-blocking if buffer is full, potentially dropping logs)
	// A select with a default could handle buffer full, but simple send is often ok.
	select {
	case l.logChan <- msg:
	default:
		// Log channel buffer is full, message dropped. Consider logging this drop to stderr?
		// fmt.Fprintf(os.Stderr, "Warning: Log channel buffer full, message dropped.\n")
	}
}

// IsEnabled returns true for FileLogger.
func (l *FileLogger) IsEnabled() bool {
	return true
}

// Close signals the writer goroutine to exit and closes the log file.
func (l *FileLogger) Close() error {
	// Signal the writer to stop by closing the channel
	close(l.logChan)

	// Wait for the writer goroutine to finish processing remaining messages
	l.waiter.Wait()

	// Safely close the file
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil // Prevent further writes
		return err
	}
	return nil
}

// Ensure FileLogger implements the Logger interface.
var _ Logger = (*FileLogger)(nil)
