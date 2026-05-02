package events

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRingBufferSink_AppendsToStore(t *testing.T) {
	store := NewEventStore(0, 0)
	hub := NewSSEHub(0)
	sink := NewRingBufferSink(store, hub)

	e := Event{ID: "e1", Task: "agent-1", Time: time.Now(), Action: "heartbeat"}
	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	got := store.Recent("agent-1", 5)
	if len(got) != 1 {
		t.Fatalf("store has %d events, want 1", len(got))
	}
	if got[0].ID != "e1" {
		t.Errorf("stored event ID = %q, want %q", got[0].ID, "e1")
	}
}

func TestRingBufferSink_BroadcastsToSSE(t *testing.T) {
	store := NewEventStore(0, 0)
	hub := NewSSEHub(0)
	sink := NewRingBufferSink(store, hub)

	ch := hub.Subscribe("agent-1")
	defer hub.Unsubscribe("agent-1", ch)

	e := Event{ID: "e1", Task: "agent-1", Time: time.Now(), Action: "tool.started"}
	sink.Handle(context.Background(), e)

	select {
	case got := <-ch:
		if got.ID != "e1" {
			t.Errorf("SSE event ID = %q, want %q", got.ID, "e1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SSE event")
	}
}

func TestRingBufferSink_GlobalSSEReceives(t *testing.T) {
	store := NewEventStore(0, 0)
	hub := NewSSEHub(0)
	sink := NewRingBufferSink(store, hub)

	ch := hub.Subscribe("") // global
	defer hub.Unsubscribe("", ch)

	e := Event{ID: "e1", Task: "agent-1", Time: time.Now(), Action: "heartbeat"}
	sink.Handle(context.Background(), e)

	select {
	case got := <-ch:
		if got.ID != "e1" {
			t.Errorf("global SSE event ID = %q, want %q", got.ID, "e1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("global subscriber timed out")
	}
}

func TestRingBufferSink_NilHub(t *testing.T) {
	store := NewEventStore(0, 0)
	sink := NewRingBufferSink(store, nil)

	e := Event{ID: "e1", Task: "agent-1", Time: time.Now(), Action: "heartbeat"}
	// Should not panic.
	if err := sink.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	got := store.Recent("agent-1", 5)
	if len(got) != 1 {
		t.Errorf("store has %d events, want 1", len(got))
	}
}

func TestRingBufferSink_IgnoresEmptyTask(t *testing.T) {
	store := NewEventStore(0, 0)
	hub := NewSSEHub(0)
	sink := NewRingBufferSink(store, hub)

	e := Event{ID: "e1", Time: time.Now(), Action: "heartbeat"}
	sink.Handle(context.Background(), e)

	if store.Len() != 0 {
		t.Errorf("store should be empty for events without task")
	}
}

func TestRingBufferSink_EndToEnd(t *testing.T) {
	store := NewEventStore(5, 0)
	hub := NewSSEHub(0)
	sink := NewRingBufferSink(store, hub)

	// Subscribe before events.
	taskCh := hub.Subscribe("agent-1")
	globalCh := hub.Subscribe("")
	defer hub.Unsubscribe("agent-1", taskCh)
	defer hub.Unsubscribe("", globalCh)

	// Emit multiple events.
	for i := 0; i < 3; i++ {
		e := Event{
			ID:     fmt.Sprintf("e%d", i+1),
			Task:   "agent-1",
			Time:   time.Now(),
			Action: "heartbeat",
		}
		sink.Handle(context.Background(), e)
	}

	// Verify ring buffer has all events.
	recent := store.Recent("agent-1", 10)
	if len(recent) != 3 {
		t.Fatalf("ring has %d events, want 3", len(recent))
	}

	// Verify SSE subscribers received all events.
	for i := 0; i < 3; i++ {
		select {
		case got := <-taskCh:
			if got.ID != fmt.Sprintf("e%d", i+1) {
				t.Errorf("task sub: got %q, want e%d", got.ID, i+1)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("task sub: timed out on event %d", i+1)
		}
	}
	for i := 0; i < 3; i++ {
		select {
		case got := <-globalCh:
			if got.ID != fmt.Sprintf("e%d", i+1) {
				t.Errorf("global sub: got %q, want e%d", got.ID, i+1)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("global sub: timed out on event %d", i+1)
		}
	}
}
