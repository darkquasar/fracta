// Package fractalog provides structured logging initialization and helpers.
// All logging goes through log/slog — this package configures the global handler
// and provides a Component helper for tagged loggers.
package fractalog

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Init configures the global slog handler with JSON output to stderr.
// The log level is controlled by FRACTA_LOG_LEVEL (debug, info, warn, error).
// Call this once at startup (e.g., in cmd/root.go init()).
func Init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: resolveLevel(""),
	})))
}

// AttachFile adds a log file alongside stderr. After this call, all slog
// output goes to both stderr and the file. The path is resolved via
// filepath.Abs (absolute paths used as-is, relative paths resolved from CWD).
// If level is non-empty it overrides FRACTA_LOG_LEVEL for both destinations.
func AttachFile(path, level string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(abs); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	w := io.MultiWriter(os.Stderr, f)
	slog.SetDefault(slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: resolveLevel(level),
	})))
	return nil
}

// Component returns a logger tagged with the given component name.
func Component(name string) *slog.Logger {
	return slog.Default().With("component", name)
}

// resolveLevel returns the slog level from the given string, falling back to
// FRACTA_LOG_LEVEL env, then defaulting to info.
func resolveLevel(level string) slog.Level {
	s := level
	if s == "" {
		s = os.Getenv("FRACTA_LOG_LEVEL")
	}
	switch strings.ToLower(s) {
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
