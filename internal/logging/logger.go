package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a new slog.Logger based on the given level and format.
// Supported levels: debug, info, warn, error.
// Supported formats: text, json.
func NewLogger(level, format string) *slog.Logger {
	return NewLoggerWithWriter(level, format, os.Stderr)
}

// NewLoggerWithWriter creates a new slog.Logger that writes to the given writer.
func NewLoggerWithWriter(level, format string, w io.Writer) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
