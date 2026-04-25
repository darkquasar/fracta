package hostadapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeStreamAdapter_SessionStatusBusy(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"session.status","info":{"session":{"id":"ses_123"},"status":{"type":"busy"}}}`)

	// lifecycle.started no longer emitted — owned by writer.
	result := adapter.ParseLine(data)
	assert.Empty(t, result.DetailEvents)
	assert.Equal(t, "ses_123", adapter.sessionID)
}

func TestOpenCodeStreamAdapter_SessionStatusIdle(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"session.status","info":{"session":{"id":"ses_456"},"status":{"type":"idle"}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "turn.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "ses_456", result.DetailEvents[0].Attrs["session_id"])
}

func TestOpenCodeStreamAdapter_MessagePartText(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"message.part.updated","info":{"part":{"type":"text","text":"Hello world"}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "message.delta", result.DetailEvents[0].Action)
	assert.Equal(t, "Hello world", result.DetailEvents[0].Attrs["text_preview"])
	assert.Equal(t, "11", result.DetailEvents[0].Attrs["text_bytes"])
	assert.Equal(t, "debug", result.DetailEvents[0].Severity)
}

func TestOpenCodeStreamAdapter_MessagePartTextTruncated(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}

	// Long text that should be truncated in preview.
	longText := make([]byte, 300)
	for i := range longText {
		longText[i] = 'a'
	}
	data := []byte(`{"type":"message.part.updated","info":{"part":{"type":"text","text":"` + string(longText) + `"}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "message.delta", result.DetailEvents[0].Action)
	assert.LessOrEqual(t, len(result.DetailEvents[0].Attrs["text_preview"]), 256)
	assert.Equal(t, "300", result.DetailEvents[0].Attrs["text_bytes"])
}

func TestOpenCodeStreamAdapter_ToolStarted(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"message.part.updated","info":{"part":{"type":"tool","tool":"bash","state":{"status":"running"}}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "tool.started", result.DetailEvents[0].Action)
	assert.Equal(t, "bash", result.DetailEvents[0].Attrs["tool_name"])
}

func TestOpenCodeStreamAdapter_ToolCompleted(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"message.part.updated","info":{"part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "tool.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "bash", result.DetailEvents[0].Attrs["tool_name"])
	assert.Equal(t, "completed", result.DetailEvents[0].Attrs["status"])
}

func TestOpenCodeStreamAdapter_ToolError(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"message.part.updated","info":{"part":{"type":"tool","tool":"edit","state":{"status":"error"}}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	assert.Equal(t, "tool.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "error", result.DetailEvents[0].Attrs["status"])
}

func TestOpenCodeStreamAdapter_SessionError(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"session.error","info":{"error":"API rate limit exceeded"}}`)

	result := adapter.ParseLine(data)
	assert.Empty(t, result.DetailEvents)
	require.NotNil(t, result.FatalError)
	assert.Equal(t, "API rate limit exceeded", result.FatalError.Message)
}

func TestOpenCodeStreamAdapter_MessageUpdated(t *testing.T) {
	// message.updated is metadata-only — no canonical event needed.
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"message.updated","info":{"message":{"id":"msg_1","role":"assistant"}}}`)

	result := adapter.ParseLine(data)
	assert.Empty(t, result.DetailEvents)
}

func TestOpenCodeStreamAdapter_UnknownType(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"permission.asked","info":{"id":"perm_1"}}`)

	result := adapter.ParseLine(data)
	assert.Empty(t, result.DetailEvents)
}

func TestOpenCodeStreamAdapter_EmptyInput(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}

	r1 := adapter.ParseLine(nil)
	assert.Empty(t, r1.DetailEvents)
	assert.Nil(t, r1.FatalError)
	r2 := adapter.ParseLine([]byte{})
	assert.Empty(t, r2.DetailEvents)
	assert.Nil(t, r2.FatalError)
}

func TestOpenCodeStreamAdapter_InvalidJSON(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
	result := adapter.ParseLine([]byte("not json"))
	assert.Empty(t, result.DetailEvents)
}

func TestOpenCodeStreamAdapter_SessionIDTracking(t *testing.T) {
	adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}

	// Session ID should be tracked from status events (busy no longer emits events).
	data := []byte(`{"type":"session.status","info":{"session":{"id":"ses_track"},"status":{"type":"busy"}}}`)
	result := adapter.ParseLine(data)
	assert.Empty(t, result.DetailEvents)
	assert.Equal(t, "ses_track", adapter.sessionID)

	// Idle event should carry the session ID.
	data2 := []byte(`{"type":"session.status","info":{"session":{"id":"ses_track"},"status":{"type":"idle"}}}`)
	result2 := adapter.ParseLine(data2)
	require.Len(t, result2.DetailEvents, 1)
	assert.Equal(t, "ses_track", result2.DetailEvents[0].Attrs["session_id"])
}

func TestOpenCodeStreamAdapter_EventMetadata(t *testing.T) {
	// Use an event that still produces output (idle/turn.completed) for metadata checks.
	adapter := &OpenCodeStreamAdapter{task: "my-agent", runtimeType: "opencode"}
	data := []byte(`{"type":"session.status","info":{"session":{"id":"ses_meta"},"status":{"type":"idle"}}}`)

	result := adapter.ParseLine(data)
	require.Len(t, result.DetailEvents, 1)
	evt := result.DetailEvents[0]

	assert.Equal(t, "host_adapter", evt.Component)
	assert.Equal(t, "agent_activity", evt.Category)
	assert.Equal(t, "task:my-agent", evt.Resource)
	assert.Equal(t, "my-agent", evt.Task)
	assert.NotEmpty(t, evt.ID)
	assert.False(t, evt.Time.IsZero())
}

func TestOpenCodeStreamAdapter_ParseBatchResult(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode", sessionID: "ses_batch"}
		result := adapter.ParseBatchResult([]byte("some output"), nil)
		// No lifecycle events — writer owns those. No detail events for opencode batch.
		assert.Empty(t, result.DetailEvents)
	})

	t.Run("error", func(t *testing.T) {
		adapter := &OpenCodeStreamAdapter{task: "test-agent", runtimeType: "opencode"}
		result := adapter.ParseBatchResult(nil, assert.AnError)
		// No lifecycle events — writer owns lifecycle.failed.
		assert.Empty(t, result.DetailEvents)
	})
}

func TestNewStreamAdapter_OpenCode(t *testing.T) {
	adapter := NewStreamAdapter("opencode", "my-task")
	_, ok := adapter.(*OpenCodeStreamAdapter)
	assert.True(t, ok, "expected OpenCodeStreamAdapter for 'opencode' runtime type")
}
