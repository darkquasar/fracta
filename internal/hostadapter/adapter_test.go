package hostadapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStreamAdapter_Claude(t *testing.T) {
	a := NewStreamAdapter("claude", "test-agent")
	_, ok := a.(*ClaudeStreamAdapter)
	require.True(t, ok, "expected ClaudeStreamAdapter for host type 'claude'")
}

func TestNewStreamAdapter_Codex(t *testing.T) {
	a := NewStreamAdapter("codex", "test-agent")
	_, ok := a.(*CodexBatchAdapter)
	require.True(t, ok, "expected CodexBatchAdapter for host type 'codex'")
}

func TestNewStreamAdapter_Unknown(t *testing.T) {
	a := NewStreamAdapter("unknown-host", "test-agent")
	_, ok := a.(*NoopAdapter)
	require.True(t, ok, "expected NoopAdapter for unknown host type")
}

func TestNoopAdapter_ParseLine(t *testing.T) {
	a := &NoopAdapter{}
	result := a.ParseLine([]byte(`{"type":"system","subtype":"init"}`))
	assert.Empty(t, result.DetailEvents)
	assert.Nil(t, result.FatalError)
}

func TestNoopAdapter_ParseBatchResult(t *testing.T) {
	a := &NoopAdapter{}
	result := a.ParseBatchResult([]byte(`{"session_id":"s1","result":"done"}`), nil)
	assert.Empty(t, result.DetailEvents)
}
