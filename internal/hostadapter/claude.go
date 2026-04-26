package hostadapter

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/google/uuid"
)

// ClaudeStreamAdapter parses Claude stream-json protocol events and maps them
// to canonical events.Event values.
type ClaudeStreamAdapter struct {
	task        string
	runtimeType    string
	sessionID   string // from system:init, attached to subsequent events
	currentTool string // last tool_use name seen, for tool_result correlation
}

// claudeWireEvent mirrors the internal wire format used by Claude stream-json.
type claudeWireEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	Message   json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

func (a *ClaudeStreamAdapter) ParseLine(line []byte) StreamParseResult {
	if len(line) == 0 {
		return StreamParseResult{}
	}

	var wire claudeWireEvent
	if err := json.Unmarshal(line, &wire); err != nil {
		return StreamParseResult{}
	}

	// Track session ID from any event that carries it.
	if wire.SessionID != "" {
		a.sessionID = wire.SessionID
	}

	switch wire.Type {
	case "system":
		// lifecycle.started is owned by the agentlifecycle.Writer.
		return StreamParseResult{}

	case "assistant":
		return StreamParseResult{DetailEvents: a.parseAssistant(wire.Message)}

	case "tool_result":
		return StreamParseResult{DetailEvents: a.parseToolResult(wire.Message)}

	case "result":
		// In stream mode, "result" is a turn delimiter — the session stays alive
		// for future sends. Map to turn.completed, NOT lifecycle.completed.
		if wire.IsError {
			return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.completed", "info", map[string]string{
				"session_id": a.sessionID,
				"error":      wire.Result,
				"is_error":   "true",
			})}}
		}
		var evts []events.Event
		if wire.Result != "" {
			preview := wire.Result
			if len(preview) > 256 {
				preview = preview[:256]
			}
			evts = append(evts, a.newEvent("message.completed", "info", map[string]string{
				"text_preview": preview,
			}))
		}
		evts = append(evts, a.newEvent("turn.completed", "info", map[string]string{
			"session_id": a.sessionID,
		}))
		return StreamParseResult{DetailEvents: evts}

	case "error":
		return StreamParseResult{FatalError: &StreamError{Message: wire.Result}}

	default:
		return StreamParseResult{}
	}
}

func (a *ClaudeStreamAdapter) parseAssistant(msg json.RawMessage) []events.Event {
	if len(msg) == 0 {
		return nil
	}

	var parsed claudeMessage
	if err := json.Unmarshal(msg, &parsed); err != nil {
		return nil
	}

	var out []events.Event
	var textBytes int
	var textPreview string

	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			textBytes += len(block.Text)
			if textPreview == "" && len(block.Text) > 0 {
				textPreview = block.Text
				if len(textPreview) > 256 {
					textPreview = textPreview[:256]
				}
			}
		case "tool_use":
			a.currentTool = block.Name
			out = append(out, a.newEvent("tool.started", "info", map[string]string{
				"tool_name": block.Name,
			}))
		}
	}

	if textBytes > 0 {
		attrs := map[string]string{
			"text_bytes": strconv.Itoa(textBytes),
		}
		if textPreview != "" {
			attrs["text_preview"] = textPreview
		}
		// Emit message.delta before tool.started for consistent ordering.
		delta := a.newEvent("message.delta", "debug", attrs)
		out = append([]events.Event{delta}, out...)
	}

	return out
}

func (a *ClaudeStreamAdapter) parseToolResult(msg json.RawMessage) []events.Event {
	attrs := map[string]string{
		"tool_name": a.currentTool,
	}
	if len(msg) > 0 {
		attrs["result_bytes"] = strconv.Itoa(len(msg))
	}
	return []events.Event{a.newEvent("tool.completed", "info", attrs)}
}

func (a *ClaudeStreamAdapter) ParseBatchResult(stdout []byte, exitErr error) ParsedResult {
	var result ParsedResult

	var resp struct {
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		IsError   bool   `json:"is_error"`
	}

	if err := json.Unmarshal(stdout, &resp); err != nil {
		// Parse failure — no detail events to return.
		return result
	}

	if resp.SessionID != "" {
		a.sessionID = resp.SessionID
	}

	// Only emit wire-level detail events; lifecycle events are owned by the writer.
	if !resp.IsError && exitErr == nil && resp.Result != "" {
		preview := resp.Result
		if len(preview) > 256 {
			preview = preview[:256]
		}
		result.DetailEvents = append(result.DetailEvents, a.newEvent("message.completed", "info", map[string]string{
			"text_preview": preview,
		}))
	}

	return result
}

func (a *ClaudeStreamAdapter) newEvent(action, severity string, attrs map[string]string) events.Event {
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
