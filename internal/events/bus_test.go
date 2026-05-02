package events

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// recordingSink captures events for test assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *recordingSink) Handle(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *recordingSink) String() string { return "recordingSink" }

func (s *recordingSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Event, len(s.events))
	copy(cp, s.events)
	return cp
}

// failingSink always returns an error.
type failingSink struct{}

func (failingSink) Handle(context.Context, Event) error {
	return errors.New("sink exploded")
}
func (failingSink) String() string { return "failingSink" }

func TestNoopBus_DoesNotPanic(t *testing.T) {
	var bus NoopBus
	bus.Emit(context.Background(), Info("test", "noop"))
}

func TestFanoutBus_EmitsToAllSinks(t *testing.T) {
	s1 := &recordingSink{}
	s2 := &recordingSink{}
	bus := NewFanoutBus(s1, s2)

	e := Info("orchestrator", "create")
	e.Task = "agent-1"
	bus.Emit(context.Background(), e)

	if got := len(s1.Events()); got != 1 {
		t.Errorf("s1 got %d events, want 1", got)
	}
	if got := len(s2.Events()); got != 1 {
		t.Errorf("s2 got %d events, want 1", got)
	}
	if s1.Events()[0].Task != "agent-1" {
		t.Errorf("s1 event task = %q, want agent-1", s1.Events()[0].Task)
	}
}

func TestFanoutBus_SinkFailureDoesNotBreakCaller(t *testing.T) {
	s1 := &recordingSink{}
	bad := failingSink{}
	s2 := &recordingSink{}
	bus := NewFanoutBus(s1, bad, s2)

	e := Info("orchestrator", "create")
	bus.Emit(context.Background(), e)

	// Both recording sinks should still receive the event.
	if got := len(s1.Events()); got != 1 {
		t.Errorf("s1 got %d events, want 1", got)
	}
	if got := len(s2.Events()); got != 1 {
		t.Errorf("s2 got %d events, want 1", got)
	}
}

func TestFanoutBus_EmptyBus(t *testing.T) {
	bus := NewFanoutBus()
	// Should not panic with zero sinks.
	bus.Emit(context.Background(), Info("test", "empty"))
}
