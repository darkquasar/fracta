package hostadapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/google/uuid"
)

// CodexBatchAdapter parses Codex output and maps events to canonical
// events.Event values. Supports both batch JSONL (codex exec --json) and
// stream JSON-RPC (codex app-server) wire formats. The wire format is
// detected per-line: JSON-RPC notifications have a "method" field while
// batch JSONL has a "type" field.
type CodexBatchAdapter struct {
	task        string
	runtimeType string
	threadID    string // from thread.started or thread/started
}

// codexEvent mirrors the Codex JSONL wire format (batch mode).
type codexEvent struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id,omitempty"`
	Item     *codexItem  `json:"item,omitempty"`
	Usage    *codexUsage `json:"usage,omitempty"`
}

type codexItem struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Text             string            `json:"text,omitempty"`
	Command          string            `json:"command,omitempty"`
	AggregatedOutput string            `json:"aggregated_output,omitempty"`
	ExitCode         *int              `json:"exit_code,omitempty"`
	Status           string            `json:"status,omitempty"`
	Changes          []codexFileChange `json:"changes,omitempty"`
}

type codexFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type codexUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// codexJSONRPCNotification mirrors the app-server JSON-RPC wire format (stream mode).
type codexJSONRPCNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// codexStreamThreadStarted is the params for a thread/started JSON-RPC notification.
type codexStreamThreadStarted struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// codexStreamDelta is the params for item/agentMessage/delta.
type codexStreamDelta struct {
	ThreadID string `json:"threadId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

// codexStreamCmdDelta is the params for item/commandExecution/outputDelta.
type codexStreamCmdDelta struct {
	ThreadID string `json:"threadId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

// codexStreamItemStarted is the params for item/started.
type codexStreamItemStarted struct {
	ThreadID string `json:"threadId"`
	Item     struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"item"`
}

// codexStreamItemCompleted is the params for item/completed.
type codexStreamItemCompleted struct {
	ThreadID string `json:"threadId"`
	Item     struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"item"`
}

// codexStreamError is the params for error JSON-RPC notifications.
type codexStreamError struct {
	ThreadID string `json:"threadId"`
	Error    struct {
		Message           string `json:"message"`
		AdditionalDetails string `json:"additionalDetails"`
	} `json:"error"`
	WillRetry bool `json:"willRetry"`
}

// codexStreamTurnCompleted is the params for turn/completed.
type codexStreamTurnCompleted struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage *struct {
		InputTokens       int `json:"inputTokens"`
		CachedInputTokens int `json:"cachedInputTokens"`
		OutputTokens      int `json:"outputTokens"`
	} `json:"tokenUsage"`
}

func (a *CodexBatchAdapter) ParseLine(line []byte) StreamParseResult {
	if len(line) == 0 {
		return StreamParseResult{}
	}

	var probe struct {
		Method string `json:"method"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return StreamParseResult{}
	}
	if probe.Method != "" {
		return a.parseStreamLine(line, probe.Method)
	}

	// Batch JSONL path.
	var event codexEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return StreamParseResult{}
	}

	switch event.Type {
	case "thread.started":
		a.threadID = event.ThreadID
		return StreamParseResult{}

	case "turn.started":
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.started", "debug", nil)}}

	case "item.completed":
		return StreamParseResult{DetailEvents: a.parseItemCompleted(event.Item)}

	case "turn.completed":
		attrs := map[string]string{}
		if event.Usage != nil {
			attrs["input_tokens"] = strconv.Itoa(event.Usage.InputTokens)
			attrs["output_tokens"] = strconv.Itoa(event.Usage.OutputTokens)
			attrs["cached_tokens"] = strconv.Itoa(event.Usage.CachedInputTokens)
		}
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.completed", "info", attrs)}}

	default:
		return StreamParseResult{}
	}
}

// parseStreamLine handles JSON-RPC notifications from codex app-server.
func (a *CodexBatchAdapter) parseStreamLine(line []byte, method string) StreamParseResult {
	var notif codexJSONRPCNotification
	if err := json.Unmarshal(line, &notif); err != nil {
		return StreamParseResult{}
	}

	switch method {
	case "thread/started":
		var params codexStreamThreadStarted
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		a.threadID = params.Thread.ID
		return StreamParseResult{}

	case "item/agentMessage/delta":
		var params codexStreamDelta
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		preview := params.Delta
		if len(preview) > 256 {
			preview = preview[:256]
		}
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("message.delta", "debug", map[string]string{
			"text_preview": preview,
			"text_bytes":   strconv.Itoa(len(params.Delta)),
		})}}

	case "item/commandExecution/outputDelta":
		var params codexStreamCmdDelta
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		preview := params.Delta
		if len(preview) > 256 {
			preview = preview[:256]
		}
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("command.output", "debug", map[string]string{
			"text_preview": preview,
		})}}

	case "item/started":
		var params codexStreamItemStarted
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("item.started", "debug", map[string]string{
			"item_id":   params.Item.ID,
			"item_type": params.Item.Type,
		})}}

	case "item/completed":
		var params codexStreamItemCompleted
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		switch params.Item.Type {
		case "agent_message":
			return StreamParseResult{DetailEvents: []events.Event{a.newEvent("message.completed", "info", map[string]string{
				"item_id": params.Item.ID,
			})}}
		case "command_execution":
			return StreamParseResult{DetailEvents: []events.Event{a.newEvent("command.completed", "info", map[string]string{
				"item_id": params.Item.ID,
			})}}
		default:
			return StreamParseResult{}
		}

	case "turn/started":
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.started", "debug", nil)}}

	case "turn/completed":
		var params codexStreamTurnCompleted
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		attrs := map[string]string{}
		if params.TokenUsage != nil {
			attrs["input_tokens"] = strconv.Itoa(params.TokenUsage.InputTokens)
			attrs["output_tokens"] = strconv.Itoa(params.TokenUsage.OutputTokens)
			attrs["cached_tokens"] = strconv.Itoa(params.TokenUsage.CachedInputTokens)
		}
		return StreamParseResult{DetailEvents: []events.Event{a.newEvent("turn.completed", "info", attrs)}}

	case "error":
		var params codexStreamError
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return StreamParseResult{}
		}
		if params.WillRetry {
			return StreamParseResult{DetailEvents: []events.Event{a.newEvent("stream.retrying", "warn", map[string]string{
				"error": params.Error.Message,
			})}}
		}
		return StreamParseResult{FatalError: &StreamError{Message: params.Error.Message}}

	default:
		return StreamParseResult{}
	}
}

func (a *CodexBatchAdapter) parseItemCompleted(item *codexItem) []events.Event {
	if item == nil {
		return nil
	}

	switch item.Type {
	case "agent_message":
		preview := item.Text
		if len(preview) > 256 {
			preview = preview[:256]
		}
		return []events.Event{a.newEvent("message.completed", "info", map[string]string{
			"text_preview": preview,
		})}

	case "command_execution":
		attrs := map[string]string{
			"command": item.Command,
		}
		if item.ExitCode != nil {
			attrs["exit_code"] = strconv.Itoa(*item.ExitCode)
		}
		return []events.Event{a.newEvent("command.completed", "info", attrs)}

	case "file_change":
		var out []events.Event
		for _, change := range item.Changes {
			out = append(out, a.newEvent("file.changed", "info", map[string]string{
				"file_path":   change.Path,
				"change_kind": change.Kind,
			}))
		}
		return out

	default:
		return nil
	}
}

func (a *CodexBatchAdapter) ParseBatchResult(stdout []byte, exitErr error) ParsedResult {
	var result ParsedResult

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		parsed := a.ParseLine(line)
		result.DetailEvents = append(result.DetailEvents, parsed.DetailEvents...)
	}

	// Lifecycle events (completed/failed) are NOT emitted — owned by the writer.
	return result
}

func (a *CodexBatchAdapter) newEvent(action, severity string, attrs map[string]string) events.Event {
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
