package events

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
)

// memoryInserter records InsertEvent calls for testing.
type memoryInserter struct {
	mu     sync.Mutex
	params []InsertEventParams
}

func (m *memoryInserter) InsertEvent(_ context.Context, p InsertEventParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.params = append(m.params, p)
	return nil
}

func (m *memoryInserter) Params() []InsertEventParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]InsertEventParams, len(m.params))
	copy(cp, m.params)
	return cp
}

func TestStoreSink_WritesStructuredAndLegacy(t *testing.T) {
	ins := &memoryInserter{}
	sink := NewStoreSink(ins)

	e := Info("orchestrator", "create")
	e.Category = "agent"
	e.Task = "research-foo"
	e.Attrs = map[string]string{"model": "haiku", "runtime": "claude"}

	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	params := ins.Params()
	if len(params) != 1 {
		t.Fatalf("got %d inserts, want 1", len(params))
	}
	p := params[0]

	if p.Component != "orchestrator" {
		t.Errorf("Component = %q, want orchestrator", p.Component)
	}
	if p.Action != "create" {
		t.Errorf("Action = %q, want create", p.Action)
	}
	if p.Event != "job_created" {
		t.Errorf("Event (legacy alias) = %q, want job_created", p.Event)
	}
	if p.Task != "research-foo" {
		t.Errorf("Task = %q, want research-foo", p.Task)
	}
	if p.AttrsJSON == "" {
		t.Error("AttrsJSON should be non-empty for events with Attrs")
	}
}

func TestStoreSink_DoesNotLog(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldDefault)

	ins := &memoryInserter{}
	sink := NewStoreSink(ins)

	e := Info("orchestrator", "create")
	_ = sink.Handle(context.Background(), e)

	if buf.Len() > 0 {
		t.Errorf("StoreSink should not emit log lines, but got: %s", buf.String())
	}
}

func TestStoreSink_EmptyAttrs(t *testing.T) {
	ins := &memoryInserter{}
	sink := NewStoreSink(ins)

	e := Info("gateway", "status_change")
	_ = sink.Handle(context.Background(), e)

	params := ins.Params()
	if len(params) != 1 {
		t.Fatalf("got %d inserts, want 1", len(params))
	}
	if params[0].AttrsJSON != "" {
		t.Errorf("AttrsJSON should be empty for events without Attrs, got %q", params[0].AttrsJSON)
	}
}
