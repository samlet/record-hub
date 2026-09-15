// Package logging creates the process-wide structured logger.
package logging

import (
	"io"
	"log/slog"
)

// New creates a JSON logger suitable for both local and deployed processes.
func New(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}
