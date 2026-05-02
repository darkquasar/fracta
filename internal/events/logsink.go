package events

import (
	"context"
	"log/slog"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// LogSink writes one structured log line per event via fractalog.
type LogSink struct {
	log *slog.Logger
}

// NewLogSink creates a LogSink that logs events via fractalog.Component("events").
func NewLogSink() *LogSink {
	return &LogSink{log: fractalog.Component("events_bus")}
}

// Handle logs the event as a structured slog line.
func (s *LogSink) Handle(_ context.Context, e Event) error {
	attrs := []any{
		"event_id", e.ID,
		"source", e.Component,
		"action", e.Action,
		"severity", e.Severity,
	}
	if e.Category != "" {
		attrs = append(attrs, "category", e.Category)
	}
	if e.Resource != "" {
		attrs = append(attrs, "resource", e.Resource)
	}
	if e.Outcome != "" {
		attrs = append(attrs, "outcome", e.Outcome)
	}
	if e.Task != "" {
		attrs = append(attrs, "task", e.Task)
	}
	if e.ObjectiveID != "" {
		attrs = append(attrs, "objective_id", e.ObjectiveID)
	}
	if e.MissionID != 0 {
		attrs = append(attrs, "mission_id", e.MissionID)
	}
	if e.Detail != "" {
		attrs = append(attrs, "detail", e.Detail)
	}
	if len(e.Attrs) > 0 {
		attrs = append(attrs, "attrs", e.Attrs)
	}

	s.log.Info("event", attrs...)
	return nil
}

// String returns the sink name for logging.
func (s *LogSink) String() string { return "LogSink" }
