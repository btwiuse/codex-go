package logging

// Logger defines the interface for logging messages.
type Logger interface {
	// Log formats and writes a log message.
	Log(format string, args ...interface{})
	// IsEnabled returns true if the logger is active (e.g., debug mode is on).
	IsEnabled() bool
	// Close cleans up any resources used by the logger (e.g., closes file handles).
	Close() error
}
