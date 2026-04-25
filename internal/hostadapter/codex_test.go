package hostadapter

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexBatchAdapter_ThreadStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "codex-agent", runtimeType: "codex"}
	line := []byte(`{"type":"thread.started","thread_id":"thread-abc"}`)

	// lifecycle.started no longer emitted by adapter — owned by writer.
	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)

	// Verify thread ID is still tracked.
	assert.Equal(t, "thread-abc", a.threadID)
}

func TestCodexBatchAdapter_TurnStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"turn.started"}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	assert.Equal(t, "turn.started", result.DetailEvents[0].Action)
	assert.Equal(t, "debug", result.DetailEvents[0].Severity)
}

func TestCodexBatchAdapter_ItemCompleted_AgentMessage(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"I found the bug in line 42."}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "message.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "I found the bug in line 42.", e.Attrs["text_preview"])
}

func TestCodexBatchAdapter_ItemCompleted_AgentMessage_LongText(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	longText := strings.Repeat("x", 500)
	line := []byte(`{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"` + longText + `"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	// Preview should be truncated to 256 chars.
	assert.Len(t, result.DetailEvents[0].Attrs["text_preview"], 256)
}

func TestCodexBatchAdapter_ItemCompleted_CommandExecution(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"item.completed","item":{"id":"item-2","type":"command_execution","command":"go test ./...","exit_code":0,"aggregated_output":"PASS"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "command.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "go test ./...", e.Attrs["command"])
	assert.Equal(t, "0", e.Attrs["exit_code"])
}

func TestCodexBatchAdapter_ItemCompleted_CommandExecution_NoExitCode(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"item.completed","item":{"id":"item-2","type":"command_execution","command":"ls"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "command.completed", e.Action)
	assert.Equal(t, "ls", e.Attrs["command"])
	_, hasExitCode := e.Attrs["exit_code"]
	assert.False(t, hasExitCode, "exit_code should not be set when nil")
}

func TestCodexBatchAdapter_ItemCompleted_FileChange(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"item.completed","item":{"id":"item-3","type":"file_change","changes":[{"path":"main.go","kind":"modify"},{"path":"new.go","kind":"add"}]}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 2)

	assert.Equal(t, "file.changed", result.DetailEvents[0].Action)
	assert.Equal(t, "main.go", result.DetailEvents[0].Attrs["file_path"])
	assert.Equal(t, "modify", result.DetailEvents[0].Attrs["change_kind"])

	assert.Equal(t, "file.changed", result.DetailEvents[1].Action)
	assert.Equal(t, "new.go", result.DetailEvents[1].Attrs["file_path"])
	assert.Equal(t, "add", result.DetailEvents[1].Attrs["change_kind"])
}

func TestCodexBatchAdapter_TurnCompleted_WithUsage(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":1500,"cached_input_tokens":200,"output_tokens":800}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "1500", e.Attrs["input_tokens"])
	assert.Equal(t, "800", e.Attrs["output_tokens"])
	assert.Equal(t, "200", e.Attrs["cached_tokens"])
}

func TestCodexBatchAdapter_TurnCompleted_NoUsage(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"turn.completed"}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	// No usage fields set when usage is nil.
	_, has := e.Attrs["input_tokens"]
	assert.False(t, has)
}

func TestCodexBatchAdapter_UnknownType(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	result := a.ParseLine([]byte(`{"type":"unknown.event"}`))
	assert.Empty(t, result.DetailEvents)
}

func TestCodexBatchAdapter_InvalidJSON(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	result := a.ParseLine([]byte(`not json`))
	assert.Empty(t, result.DetailEvents)
}

func TestCodexBatchAdapter_EmptyLine(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	result := a.ParseLine([]byte{})
	assert.Empty(t, result.DetailEvents)
}

func TestCodexBatchAdapter_NilItem(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	result := a.ParseLine([]byte(`{"type":"item.completed"}`))
	assert.Empty(t, result.DetailEvents)
}

func TestCodexBatchAdapter_ParseBatchResult_Success(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	stdout := []byte(`{"type":"thread.started","thread_id":"t-1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"Done"}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":10,"output_tokens":50}}
`)

	result := a.ParseBatchResult(stdout, nil)

	// Only wire-level detail events; no lifecycle events.
	// turn.started, message.completed, turn.completed (lifecycle.started/completed filtered)
	require.Len(t, result.DetailEvents, 3)
	assert.Equal(t, "turn.started", result.DetailEvents[0].Action)
	assert.Equal(t, "message.completed", result.DetailEvents[1].Action)
	assert.Equal(t, "turn.completed", result.DetailEvents[2].Action)
}

func TestCodexBatchAdapter_ParseBatchResult_ProcessError(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	stdout := []byte(`{"type":"thread.started","thread_id":"t-2"}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"partial work"}}
`)

	result := a.ParseBatchResult(stdout, errors.New("exit status 1"))

	// message.completed only; lifecycle.failed owned by writer.
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "message.completed", result.DetailEvents[0].Action)
}

func TestCodexBatchAdapter_ParseBatchResult_NoThreadStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	stdout := []byte(`{"type":"turn.started"}
{"type":"turn.completed"}
`)

	result := a.ParseBatchResult(stdout, nil)

	// turn.started + turn.completed (no lifecycle events)
	require.Len(t, result.DetailEvents, 2)
	assert.Equal(t, "turn.started", result.DetailEvents[0].Action)
	assert.Equal(t, "turn.completed", result.DetailEvents[1].Action)
}

func TestCodexBatchAdapter_ParseBatchResult_EmptyOutput(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}

	result := a.ParseBatchResult([]byte{}, errors.New("no output"))

	// No detail events on empty output (lifecycle.failed owned by writer).
	assert.Empty(t, result.DetailEvents)
}

func TestCodexBatchAdapter_ParseBatchResult_MixedWithNonJSON(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	stdout := []byte(`some debug output
{"type":"thread.started","thread_id":"t-3"}
another debug line
{"type":"turn.completed"}
`)

	result := a.ParseBatchResult(stdout, nil)

	// turn.completed only (lifecycle.started filtered, lifecycle.completed not emitted)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "turn.completed", result.DetailEvents[0].Action)
}
