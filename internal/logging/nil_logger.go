package logging

// NilLogger is a logger implementation that performs no operations.
type NilLogger struct{}

// NewNilLogger creates a new no-op logger.
func NewNilLogger() *NilLogger {
	return &NilLogger{}
}

// Log does nothing.
func (l *NilLogger) Log(format string, args ...interface{}) {}

// IsEnabled always returns false.
func (l *NilLogger) IsEnabled() bool {
	return false
}

// Close does nothing and returns nil error.
func (l *NilLogger) Close() error {
	return nil
}

// Ensure NilLogger implements the Logger interface.
var _ Logger = (*NilLogger)(nil)
