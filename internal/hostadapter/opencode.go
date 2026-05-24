package hostadapter

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/google/uuid"
)

// OpenCodeStreamAdapter parses OpenCode SSE events (from EventObservable)
// and maps them to canonical events.Event values. This adapter is a pure
// parser — orchestrator owns emission via EventObservable wiring.
type OpenCodeStreamAdapter struct {
	task        string
	runtimeType string
	sessionID   string
}

// openCodeSSEEvent is the generic shape of an OpenCode SSE event payload.
type openCodeSSEEvent struct {
	Type string          `json:"type"`
	Info json.RawMessage `json:"info,omitempty"`
}

// ocSessionStatusInfo is the info payload for session.status events.
type ocSessionStatusInfo struct {
	Session struct {
		ID string `json:"id"`
	} `json:"session"`
	Status struct {
		Type string `json:"type"` // "idle", "busy"
	} `json:"status"`
}

// ocMessagePartInfo is the info payload for message.part.updated events.
type ocMessagePartInfo struct {
	Part struct {
		Type  string `json:"type"` // "text", "tool"
		Text  string `json:"text,omitempty"`
		Tool  string `json:"tool,omitempty"`
		State *struct {
			Status string `json:"status,omitempty"` // "running", "completed", "error"
		} `json:"state,omitempty"`
	} `json:"part"`
}

// ocSessionErrorInfo is the info payload for session.error events.
type ocSessionErrorInfo struct {
	Error string `json:"error"`
}

// ParseLine parses a raw SSE event payload (JSON bytes from EventObservable)
// ParseLine processes SSE event data and returns wire-level detail events
// plus an optional fatal error signal.
func (a *OpenCodeStreamAdapter) ParseLine(data []byte) StreamParseResult {
	if len(data) == 0 {
		return StreamParseResult{}
	}

	var sse openCodeSSEEvent
	if err := json.Unmarshal(data, &sse); err != nil {
		return StreamParseResult{}
	}

	switch sse.Type {
	case "session.status":
		return a.parseSessionStatus(sse.Info)

	case "message.part.updated":
		return StreamParseResult{DetailEvents: a.parseMessagePart(sse.Info)}

	case "session.error":
		return a.parseSessionError(sse.Info)

	case "message.updated":
		return StreamParseResult{}

	default:
		return StreamParseResult{}
	}
}

func (a *OpenCodeStreamAdapter) parseSessionStatus(info json.RawMessage) StreamParseResult {
	var status ocSessionStatusInfo
	if err := json.Unmarshal(info, &status); err != nil {
		return StreamParseResult{}
	}

	if status.Session.ID != "" {
		a.sessionID = status.Session.ID
	}

	switch status.Status.Type {
	case "busy":
		return StreamParseResult{}
	case "idle":
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.completed", "info", map[string]string{
			"session_id": a.sessionID,
		})}}
	default:
		return StreamParseResult{}
	}
}

func (a *OpenCodeStreamAdapter) parseMessagePart(info json.RawMessage) []events.Event {
	var mp ocMessagePartInfo
	if err := json.Unmarshal(info, &mp); err != nil {
		return nil
	}

	switch mp.Part.Type {
	case "text":
		attrs := map[string]string{}
		if mp.Part.Text != "" {
			preview := mp.Part.Text
			if len(preview) > 256 {
				preview = preview[:256]
			}
			attrs["text_preview"] = preview
			attrs["text_bytes"] = strconv.Itoa(len(mp.Part.Text))
		}
		return []events.Event{a.newEvent("message.delta", "debug", attrs)}

	case "tool":
		toolName := mp.Part.Tool
		if mp.Part.State != nil {
			switch mp.Part.State.Status {
			case "running":
				return []events.Event{a.newEvent("tool.started", "info", map[string]string{
					"tool_name": toolName,
				})}
			case "completed", "error":
				return []events.Event{a.newEvent("tool.completed", "info", map[string]string{
					"tool_name": toolName,
					"status":    mp.Part.State.Status,
				})}
			}
		}
		return nil

	default:
		return nil
	}
}

func (a *OpenCodeStreamAdapter) parseSessionError(info json.RawMessage) StreamParseResult {
	var errInfo ocSessionErrorInfo
	if err := json.Unmarshal(info, &errInfo); err != nil {
		return StreamParseResult{FatalError: &StreamError{Message: "unknown session error"}}
	}
	return StreamParseResult{FatalError: &StreamError{Message: errInfo.Error}}
}

// ParseBatchResult returns wire-level detail events only.
// Lifecycle events (completed/failed) are owned by the agentlifecycle.Writer.
func (a *OpenCodeStreamAdapter) ParseBatchResult(stdout []byte, exitErr error) ParsedResult {
	return ParsedResult{}
}

func (a *OpenCodeStreamAdapter) newEvent(action, severity string, attrs map[string]string) events.Event {
	if attrs == nil {
		attrs = make(map[string]string)
	}
	attrs["runtime"] = a.runtimeType
	return events.Event{
		ID:        uuid.NewString(),
		Time:      time.Now(),
		Component: "host_adapter",
		Category:  "agent_activity",
		Resource:  "task:" + a.task,
		Action:    action,
		Severity:  severity,
		Task:      a.task,
		Attrs:     attrs,
	}
}
