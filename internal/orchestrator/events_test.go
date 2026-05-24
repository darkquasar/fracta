package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/model"
)

// captureBus records all emitted events for test assertions.
type captureBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (b *captureBus) Emit(_ context.Context, e events.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *captureBus) all() []events.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]events.Event, len(b.events))
	copy(out, b.events)
	return out
}

func newEventsTestOrchestrator(bus events.Bus) *Orchestrator {
	return &Orchestrator{
		Events: bus,
		Logger: fractalog.Component("orchestrator-test"),
	}
}

func TestEmit_StructuredEvent(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	o.emit(context.Background(), events.Info("orchestrator", "create"),
		func(e *events.Event) {
			e.Category = "agent"
			e.Task = "test-agent"
			e.Resource = "task:test-agent"
			e.Attrs = map[string]string{"model": "haiku"}
		})

	got := bus.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0]
	if e.Component != "orchestrator" {
		t.Errorf("component = %q, want orchestrator", e.Component)
	}
	if e.Action != "create" {
		t.Errorf("action = %q, want create", e.Action)
	}
	if e.Category != "agent" {
		t.Errorf("category = %q, want agent", e.Category)
	}
	if e.Task != "test-agent" {
		t.Errorf("task = %q, want test-agent", e.Task)
	}
	if e.Severity != "info" {
		t.Errorf("severity = %q, want info", e.Severity)
	}
	if e.Attrs["model"] != "haiku" {
		t.Errorf("attrs[model] = %q, want haiku", e.Attrs["model"])
	}
	if e.ID == "" {
		t.Error("event ID should not be empty")
	}
	// Legacy alias check.
	alias := events.LegacyAlias(e)
	if alias != "job_created" {
		t.Errorf("legacy alias = %q, want job_created", alias)
	}
}

func TestEmitAgentResult_Completed(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	o.emitAgentResult(context.Background(), "agent-1", model.StatusCompleted, "all done")

	got := bus.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0]
	if e.Component != "orchestrator" {
		t.Errorf("component = %q, want orchestrator", e.Component)
	}
	if e.Action != "complete" {
		t.Errorf("action = %q, want complete", e.Action)
	}
	if e.Category != "agent" {
		t.Errorf("category = %q, want agent", e.Category)
	}
	if e.Task != "agent-1" {
		t.Errorf("task = %q, want agent-1", e.Task)
	}
	if e.Outcome != "success" {
		t.Errorf("outcome = %q, want success", e.Outcome)
	}
	if e.Severity != "info" {
		t.Errorf("severity = %q, want info", e.Severity)
	}
	alias := events.LegacyAlias(e)
	if alias != "completed" {
		t.Errorf("legacy alias = %q, want completed", alias)
	}
}

func TestEmitAgentResult_Failed(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	o.emitAgentResult(context.Background(), "agent-2", model.StatusFailed, "timeout waiting for response")

	got := bus.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0]
	if e.Action != "fail" {
		t.Errorf("action = %q, want fail", e.Action)
	}
	if e.Outcome != "failure" {
		t.Errorf("outcome = %q, want failure", e.Outcome)
	}
	if e.Severity != "warn" {
		t.Errorf("severity = %q, want warn", e.Severity)
	}
	if e.Detail != "timeout waiting for response" {
		t.Errorf("detail = %q, want timeout message", e.Detail)
	}
	alias := events.LegacyAlias(e)
	if alias != "failed" {
		t.Errorf("legacy alias = %q, want failed", alias)
	}
}

func TestEmitAgentResult_FailedDetailTruncated(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	// Create output longer than 512 bytes.
	longOutput := make([]byte, 1024)
	for i := range longOutput {
		longOutput[i] = 'x'
	}
	o.emitAgentResult(context.Background(), "agent-3", model.StatusFailed, string(longOutput))

	got := bus.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if len(got[0].Detail) > 512 {
		t.Errorf("detail length = %d, want <= 512", len(got[0].Detail))
	}
}

func TestEmitAgentResult_RunningNoEvent(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	// Running status should not emit any event.
	o.emitAgentResult(context.Background(), "agent-4", model.StatusRunning, "")

	got := bus.all()
	if len(got) != 0 {
		t.Errorf("expected 0 events for Running status, got %d", len(got))
	}
}

func TestEmit_NilBusSafe(t *testing.T) {
	o := &Orchestrator{Events: nil}

	// Should not panic.
	o.emit(context.Background(), events.Info("orchestrator", "test"))
	o.emitAgentResult(context.Background(), "agent-x", model.StatusCompleted, "done")
}

func TestEmit_AuthSeedEvents(t *testing.T) {
	bus := &captureBus{}
	o := newEventsTestOrchestrator(bus)

	// Simulate auth seed success.
	o.emit(context.Background(), events.Info("orchestrator", "seed"),
		func(e *events.Event) {
			e.Category = "auth"
			e.Task = "agent-auth"
			e.Outcome = "success"
			e.Resource = "task:agent-auth"
		})

	// Simulate auth seed failure.
	o.emit(context.Background(), events.Warn("orchestrator", "seed", "command timed out"),
		func(e *events.Event) {
			e.Category = "auth"
			e.Task = "agent-auth"
			e.Outcome = "failure"
			e.Resource = "task:agent-auth"
		})

	got := bus.all()
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}

	// Success event.
	if got[0].Outcome != "success" {
		t.Errorf("first event outcome = %q, want success", got[0].Outcome)
	}
	if events.LegacyAlias(got[0]) != "auth_seed_ok" {
		t.Errorf("first event alias = %q, want auth_seed_ok", events.LegacyAlias(got[0]))
	}

	// Failure event.
	if got[1].Outcome != "failure" {
		t.Errorf("second event outcome = %q, want failure", got[1].Outcome)
	}
	if events.LegacyAlias(got[1]) != "auth_seed_failed" {
		t.Errorf("second event alias = %q, want auth_seed_failed", events.LegacyAlias(got[1]))
	}
}
