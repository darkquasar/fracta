package hostadapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Stream (JSON-RPC) adapter tests ---

func TestCodexStreamAdapter_ThreadStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "stream-agent", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"thread-rpc-1"}}}`)

	// lifecycle.started no longer emitted by adapter — owned by writer.
	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)

	// Verify thread ID still tracked.
	assert.Equal(t, "thread-rpc-1", a.threadID)
}

func TestCodexStreamAdapter_AgentMessageDelta(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"t1","itemId":"i1","delta":"Hello world"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "message.delta", e.Action)
	assert.Equal(t, "debug", e.Severity)
	assert.Equal(t, "Hello world", e.Attrs["text_preview"])
	assert.Equal(t, "11", e.Attrs["text_bytes"])
}

func TestCodexStreamAdapter_AgentMessageDelta_LongText(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	longDelta := make([]byte, 500)
	for i := range longDelta {
		longDelta[i] = 'x'
	}
	line := []byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"t1","itemId":"i1","delta":"` + string(longDelta) + `"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	// Preview should be truncated to 256 chars.
	assert.Len(t, result.DetailEvents[0].Attrs["text_preview"], 256)
	assert.Equal(t, "500", result.DetailEvents[0].Attrs["text_bytes"])
}

func TestCodexStreamAdapter_CommandOutputDelta(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/commandExecution/outputDelta","params":{"threadId":"t1","itemId":"i1","delta":"PASS\nok  \tpkg\t0.5s"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "command.output", e.Action)
	assert.Equal(t, "debug", e.Severity)
	assert.Contains(t, e.Attrs["text_preview"], "PASS")
}

func TestCodexStreamAdapter_ItemStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/started","params":{"threadId":"t1","item":{"id":"item-5","type":"command_execution"}}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "item.started", e.Action)
	assert.Equal(t, "debug", e.Severity)
	assert.Equal(t, "item-5", e.Attrs["item_id"])
	assert.Equal(t, "command_execution", e.Attrs["item_type"])
}

func TestCodexStreamAdapter_ItemCompleted_AgentMessage(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"t1","item":{"id":"item-6","type":"agent_message"}}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "message.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "item-6", e.Attrs["item_id"])
}

func TestCodexStreamAdapter_ItemCompleted_CommandExecution(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"t1","item":{"id":"item-7","type":"command_execution"}}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "command.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
}

func TestCodexStreamAdapter_ItemCompleted_UnknownType(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"t1","item":{"id":"item-8","type":"unknown_thing"}}}`)

	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
}

func TestCodexStreamAdapter_TurnStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"t1","turnId":"turn-1"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	assert.Equal(t, "turn.started", result.DetailEvents[0].Action)
	assert.Equal(t, "debug", result.DetailEvents[0].Severity)
}

func TestCodexStreamAdapter_TurnCompleted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"t1","turnId":"turn-1","tokenUsage":{"inputTokens":1200,"cachedInputTokens":100,"outputTokens":600}}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	assert.Equal(t, "info", e.Severity)
	assert.Equal(t, "1200", e.Attrs["input_tokens"])
	assert.Equal(t, "600", e.Attrs["output_tokens"])
	assert.Equal(t, "100", e.Attrs["cached_tokens"])
}

func TestCodexStreamAdapter_TurnCompleted_NoUsage(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"t1","turnId":"turn-1"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	_, has := e.Attrs["input_tokens"]
	assert.False(t, has, "no usage means no token attrs")
}

func TestCodexStreamAdapter_Error_NonRetryable(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"error","params":{"threadId":"t1","error":{"message":"rate limit exceeded","additionalDetails":"try again later"},"willRetry":false}}`)

	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
	require.NotNil(t, result.FatalError)
	assert.Equal(t, "rate limit exceeded", result.FatalError.Message)
}

func TestCodexStreamAdapter_Error_Retryable(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"error","params":{"threadId":"t1","error":{"message":"transient failure"},"willRetry":true}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "stream.retrying", e.Action)
	assert.Equal(t, "warn", e.Severity)
	assert.Equal(t, "transient failure", e.Attrs["error"])
}

func TestCodexStreamAdapter_UnknownMethod(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"jsonrpc":"2.0","method":"thread/status/changed","params":{"threadId":"t1"}}`)

	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
}

// --- Regression: batch JSONL still works with unified adapter ---

func TestCodexUnifiedAdapter_BatchRegression_ThreadStarted(t *testing.T) {
	a := &CodexBatchAdapter{task: "batch-agent", runtimeType: "codex"}
	line := []byte(`{"type":"thread.started","thread_id":"thread-batch-1"}`)

	// lifecycle.started no longer emitted — owned by writer.
	result := a.ParseLine(line)
	assert.Empty(t, result.DetailEvents)
	assert.Equal(t, "thread-batch-1", a.threadID)
}

func TestCodexUnifiedAdapter_BatchRegression_ItemCompleted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"item.completed","item":{"id":"item-1","type":"agent_message","text":"batch output"}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	assert.Equal(t, "message.completed", result.DetailEvents[0].Action)
	assert.Equal(t, "batch output", result.DetailEvents[0].Attrs["text_preview"])
}

func TestCodexUnifiedAdapter_BatchRegression_TurnCompleted(t *testing.T) {
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":50,"output_tokens":200}}`)

	result := a.ParseLine(line)
	require.Len(t, result.DetailEvents, 1)

	e := result.DetailEvents[0]
	assert.Equal(t, "turn.completed", e.Action)
	assert.Equal(t, "500", e.Attrs["input_tokens"])
}

func TestCodexUnifiedAdapter_MixedWireFormat(t *testing.T) {
	// Verify that batch and stream lines can be interspersed
	// (this shouldn't happen in practice, but verifies the per-line detection).
	a := &CodexBatchAdapter{task: "test", runtimeType: "codex"}

	// Batch line — no lifecycle event, but threadID tracked.
	result1 := a.ParseLine([]byte(`{"type":"thread.started","thread_id":"batch-t"}`))
	assert.Empty(t, result1.DetailEvents)
	assert.Equal(t, "batch-t", a.threadID)

	// Stream line — no lifecycle event, but threadID updated.
	result2 := a.ParseLine([]byte(`{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":"stream-t"}}}`))
	assert.Empty(t, result2.DetailEvents)
	assert.Equal(t, "stream-t", a.threadID)
}
