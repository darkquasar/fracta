package hostadapter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeStreamAdapter_SystemInit(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "research", runtimeType: "claude"}
	line := []byte(`{"type":"system","subtype":"init","session_id":"sess-abc123"}`)

	// lifecycle.started no longer emitted by adapter — owned by writer.
	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)

	// Verify session ID is still tracked.
	assert.Equal(t, "sess-abc123", a.sessionID)
}

func TestClaudeStreamAdapter_SystemNonInit(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	line := []byte(`{"type":"system","subtype":"other"}`)

	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_AssistantTextOnly(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "message.delta", e.Action)
	assert.Equal(t, "debug", e.Severity)
	assert.Equal(t, "11", e.Attrs["text_bytes"]) // len("Hello world")
}

func TestClaudeStreamAdapter_AssistantToolUse(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "tool.started", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "Read", e.Attrs["tool_name"])

	// Verify currentTool is tracked.
	assert.Equal(t, "Read", a.currentTool)
}

func TestClaudeStreamAdapter_AssistantTextAndToolUse(t *testing.T) {
	// Single assistant event with both text and tool_use → two events.
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","name":"Bash"}]}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 2)

	// message.delta comes first (text).
	assert.Equal(t, "message.delta", result.DetailEvents[0].Action)
	assert.Equal(t, "22", result.DetailEvents[0].Attrs["text_bytes"])

	// tool.started comes second.
	assert.Equal(t, "tool.started", result.DetailEvents[1].Action)
	assert.Equal(t, "Bash", result.DetailEvents[1].Attrs["tool_name"])
}

func TestClaudeStreamAdapter_ToolResult(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude", currentTool: "Read"}
	line := []byte(`{"type":"tool_result","message":{"content":"file contents here that are quite long"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "tool.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "Read", e.Attrs["tool_name"])
	assert.NotEmpty(t, e.Attrs["result_bytes"])
}

func TestClaudeStreamAdapter_ResultSuccess(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude", sessionID: "sess-1"}
	line := []byte(`{"type":"result","session_id":"sess-1","result":"Task completed.","is_error":false}`)

	result := a.ParseLine(line)
	// Stream result is a turn delimiter: message.completed + turn.completed (not lifecycle.completed).
	require.Len(t, result.DetailEvents, 2)

	assert.Equal(t, "message.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "Task completed.", result.DetailEvents[0].Attrs["text_preview"])

	assert.Equal(t, "turn.completed", result.DetailEvents[1].Action)
	assert.Equal(t, "info", result.DetailEvents[1].Severity)
	assert.Equal(t, "sess-1", result.DetailEvents[1].Attrs["session_id"])
}

func TestClaudeStreamAdapter_ResultError(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude", sessionID: "sess-1"}
	line := []byte(`{"type":"result","session_id":"sess-1","result":"Something went wrong","is_error":true}`)

	result := a.ParseLine(line)
	// Stream error result is still a turn delimiter, not lifecycle terminal.
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "true", e.Attrs["is_error"])
	assert.Equal(t, "Something went wrong", e.Attrs["error"])
}

func TestClaudeStreamAdapter_ErrorEvent(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	line := []byte(`{"type":"error","result":"connection lost"}`)

	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
	require.NotNil(t, result.FatalError)
	assert.Equal(t, "connection lost", result.FatalError.Message)
}

func TestClaudeStreamAdapter_InvalidJSON(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	result := a.ParseLine([]byte(`not json at all`))
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_EmptyLine(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	result := a.ParseLine([]byte{})
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_UnknownType(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	result := a.ParseLine([]byte(`{"type":"unknown_event_type"}`))
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_SessionIDPersists(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}

	// Init sets session ID.
	a.ParseLine([]byte(`{"type":"system","subtype":"init","session_id":"sess-xyz"}`))
	assert.Equal(t, "sess-xyz", a.sessionID)

	// Subsequent assistant event without session_id still has the adapter's sessionID.
	result := a.ParseLine([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`))
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "claude", result.DetailEvents[0].Attrs["runtime"])
}

func TestClaudeStreamAdapter_ParseBatchResult_Success(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	stdout := []byte(`{"session_id":"sess-batch-1","result":"Done.","is_error":false}`)

	result := a.ParseBatchResult(stdout, nil)
	// Only wire-level detail events; no lifecycle events.
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "message.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "Done.", result.DetailEvents[0].Attrs["text_preview"])
}

func TestClaudeStreamAdapter_ParseBatchResult_Error(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	stdout := []byte(`{"session_id":"sess-batch-2","result":"Out of memory","is_error":true}`)

	result := a.ParseBatchResult(stdout, nil)
	// No lifecycle events from adapter — writer owns lifecycle.failed.
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_ParseBatchResult_ProcessError(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}
	stdout := []byte(`not valid json`)

	result := a.ParseBatchResult(stdout, errors.New("exit status 1"))
	// Parse failure — no detail events, no lifecycle events.
	assert.Empty(t, result.DetailEvents)
}

func TestClaudeStreamAdapter_MultipleToolCorrelation(t *testing.T) {
	a := &ClaudeStreamAdapter{task: "test", runtimeType: "claude"}

	// Tool use: Read.
	a.ParseLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`))
	assert.Equal(t, "Read", a.currentTool)

	// Tool result should carry "Read".
	result := a.ParseLine([]byte(`{"type":"tool_result","message":{"content":"data"}}`))
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "Read", result.DetailEvents[0].Attrs["tool_name"])

	// Next tool use: Bash.
	a.ParseLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`))
	assert.Equal(t, "Bash", a.currentTool)

	// Tool result should now carry "Bash".
	result = a.ParseLine([]byte(`{"type":"tool_result","message":{"content":"output"}}`))
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "Bash", result.DetailEvents[0].Attrs["tool_name"])
}
