// Package hostadapter provides per-host wire format parsers that translate
// host-specific output (Claude stream-json, Codex JSONL) into canonical
// events.Event values. Adapters are pure parsers with no bus dependency —
// the caller is responsible for emitting returned events.
package hostadapter

import (
	"github.com/darkquasar/fracta/internal/events"
)

// ParsedResult is the structured return from ParseBatchResult.
// Lifecycle events (started/completed/failed/stopped) are NOT included —
// those are owned by the agentlifecycle.Writer. Only wire-level detail
// events (message.completed, tool.started, etc.) appear in DetailEvents.
type ParsedResult struct {
	DetailEvents []events.Event
}

// StreamParseResult is the structured return from ParseLine.
// Lifecycle events are NOT included — adapters signal fatal errors via
// FatalError, and callers route to the lifecycle writer.
type StreamParseResult struct {
	DetailEvents []events.Event
	FatalError   *StreamError
}

// StreamError signals a fatal stream error detected by the adapter.
// Callers should call Writer.MarkFailed when this is non-nil.
type StreamError struct {
	Message string
	Code    string
}

// HostStreamAdapter translates host-specific wire events into canonical
// events.Event values. Each host implementation provides its own adapter.
// Adapters are stateful (they track session IDs, current tool, etc.).
type HostStreamAdapter interface {
	// ParseLine parses a single line/event of host output and returns
	// wire-level detail events plus an optional fatal error signal.
	ParseLine(line []byte) StreamParseResult

	// ParseBatchResult parses complete batch stdout and returns wire-level
	// detail events only. Lifecycle events are NOT returned — those are
	// owned by the agentlifecycle.Writer.
	ParseBatchResult(stdout []byte, exitErr error) ParsedResult
}

// NewStreamAdapter returns the appropriate adapter for the given runtime type.
// Unknown runtime types get a NoopAdapter that produces no events.
func NewStreamAdapter(runtimeType, task string) HostStreamAdapter {
	switch runtimeType {
	case "claude":
		return &ClaudeStreamAdapter{task: task, runtimeType: runtimeType}
	case "codex":
		return &CodexBatchAdapter{task: task, runtimeType: runtimeType}
	case "opencode":
		return &OpenCodeStreamAdapter{task: task, runtimeType: runtimeType}
	default:
		return &NoopAdapter{}
	}
}

// NoopAdapter is returned for unknown host types. It produces no events.
type NoopAdapter struct{}

func (*NoopAdapter) ParseLine([]byte) StreamParseResult          { return StreamParseResult{} }
func (*NoopAdapter) ParseBatchResult([]byte, error) ParsedResult { return ParsedResult{} }
