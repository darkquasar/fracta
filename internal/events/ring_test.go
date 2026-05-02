package events

import (
	"fmt"
	"testing"
	"time"
)

func makeEvent(id string) Event {
	return Event{ID: id, Time: time.Now()}
}

func TestAgentRing_AppendAndRecent(t *testing.T) {
	ring := NewAgentRing(5)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	ring.Append(makeEvent("e3"))

	got := ring.Recent(2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "e2" || got[1].ID != "e3" {
		t.Errorf("got IDs %q, %q; want e2, e3", got[0].ID, got[1].ID)
	}
}

func TestAgentRing_RecentAll(t *testing.T) {
	ring := NewAgentRing(5)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))

	got := ring.Recent(10) // ask for more than available
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "e1" || got[1].ID != "e2" {
		t.Errorf("got IDs %q, %q; want e1, e2", got[0].ID, got[1].ID)
	}
}

func TestAgentRing_RecentEmpty(t *testing.T) {
	ring := NewAgentRing(5)
	if got := ring.Recent(5); got != nil {
		t.Errorf("expected nil for empty ring, got %v", got)
	}
}

func TestAgentRing_WrapAround(t *testing.T) {
	ring := NewAgentRing(3) // capacity 3

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	ring.Append(makeEvent("e3"))
	ring.Append(makeEvent("e4")) // evicts e1
	ring.Append(makeEvent("e5")) // evicts e2

	got := ring.Recent(3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Should have e3, e4, e5 in order.
	expected := []string{"e3", "e4", "e5"}
	for i, want := range expected {
		if got[i].ID != want {
			t.Errorf("got[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

func TestAgentRing_Since(t *testing.T) {
	ring := NewAgentRing(5)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	ring.Append(makeEvent("e3"))
	ring.Append(makeEvent("e4"))

	// Since e2 → should return e3, e4.
	got := ring.Since("e2")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "e3" || got[1].ID != "e4" {
		t.Errorf("got %q, %q; want e3, e4", got[0].ID, got[1].ID)
	}
}

func TestAgentRing_SinceMostRecent(t *testing.T) {
	ring := NewAgentRing(5)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))

	// Since e2 (the most recent) → empty slice.
	got := ring.Since("e2")
	if got == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestAgentRing_SinceEvicted(t *testing.T) {
	ring := NewAgentRing(3)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	ring.Append(makeEvent("e3"))
	ring.Append(makeEvent("e4")) // evicts e1

	// Since e1 → nil (evicted).
	got := ring.Since("e1")
	if got != nil {
		t.Errorf("expected nil for evicted event, got %v", got)
	}
}

func TestAgentRing_SinceNotFound(t *testing.T) {
	ring := NewAgentRing(5)
	ring.Append(makeEvent("e1"))

	got := ring.Since("nonexistent")
	if got != nil {
		t.Errorf("expected nil for nonexistent event, got %v", got)
	}
}

func TestAgentRing_SinceWithWrapAround(t *testing.T) {
	ring := NewAgentRing(4)

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	ring.Append(makeEvent("e3"))
	ring.Append(makeEvent("e4"))
	ring.Append(makeEvent("e5")) // evicts e1, ring has e2, e3, e4, e5

	got := ring.Since("e3")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "e4" || got[1].ID != "e5" {
		t.Errorf("got %q, %q; want e4, e5", got[0].ID, got[1].ID)
	}
}

func TestAgentRing_Len(t *testing.T) {
	ring := NewAgentRing(3)
	if ring.Len() != 0 {
		t.Errorf("empty ring len = %d", ring.Len())
	}

	ring.Append(makeEvent("e1"))
	ring.Append(makeEvent("e2"))
	if ring.Len() != 2 {
		t.Errorf("len = %d, want 2", ring.Len())
	}

	ring.Append(makeEvent("e3"))
	ring.Append(makeEvent("e4")) // wraps
	if ring.Len() != 3 {
		t.Errorf("len after wrap = %d, want 3 (cap)", ring.Len())
	}
}

// --- EventStore tests ---

func TestEventStore_AppendAndRecent(t *testing.T) {
	store := NewEventStore(0, 0)

	store.Append("agent-1", makeEvent("e1"))
	store.Append("agent-1", makeEvent("e2"))
	store.Append("agent-2", makeEvent("e3"))

	got := store.Recent("agent-1", 5)
	if len(got) != 2 {
		t.Fatalf("agent-1 recent = %d, want 2", len(got))
	}

	got = store.Recent("agent-2", 5)
	if len(got) != 1 {
		t.Fatalf("agent-2 recent = %d, want 1", len(got))
	}
}

func TestEventStore_RecentMissingAgent(t *testing.T) {
	store := NewEventStore(0, 0)
	if got := store.Recent("nonexistent", 5); got != nil {
		t.Errorf("expected nil for missing agent, got %v", got)
	}
}

func TestEventStore_Since(t *testing.T) {
	store := NewEventStore(0, 0)

	store.Append("a", makeEvent("e1"))
	store.Append("a", makeEvent("e2"))
	store.Append("a", makeEvent("e3"))

	got := store.Since("a", "e1")
	if len(got) != 2 {
		t.Fatalf("since e1: len = %d, want 2", len(got))
	}
	if got[0].ID != "e2" || got[1].ID != "e3" {
		t.Errorf("got %q, %q; want e2, e3", got[0].ID, got[1].ID)
	}
}

func TestEventStore_SinceMissingAgent(t *testing.T) {
	store := NewEventStore(0, 0)
	if got := store.Since("nonexistent", "e1"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEventStore_Remove(t *testing.T) {
	store := NewEventStore(0, 0)

	store.Append("agent-1", makeEvent("e1"))
	store.Remove("agent-1")

	if got := store.Recent("agent-1", 5); got != nil {
		t.Errorf("expected nil after remove, got %v", got)
	}
	if store.Len() != 0 {
		t.Errorf("len = %d, want 0", store.Len())
	}
}

func TestEventStore_Cleanup(t *testing.T) {
	store := NewEventStore(0, 30*time.Minute)
	now := time.Now()

	store.Append("expired", makeEvent("e1"))
	store.MarkTerminal("expired", now.Add(-31*time.Minute))

	store.Append("recent", makeEvent("e2"))
	store.MarkTerminal("recent", now.Add(-5*time.Minute))

	store.Append("active", makeEvent("e3"))
	// not marked terminal

	removed := store.Cleanup(now)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if store.Recent("expired", 5) != nil {
		t.Error("expired ring should be removed")
	}
	if store.Recent("recent", 5) == nil {
		t.Error("recent ring should still exist")
	}
	if store.Recent("active", 5) == nil {
		t.Error("active ring should still exist")
	}
}

func TestEventStore_RingSizeConfigurable(t *testing.T) {
	store := NewEventStore(3, 0)

	for i := 0; i < 5; i++ {
		store.Append("a", makeEvent(fmt.Sprintf("e%d", i+1)))
	}

	got := store.Recent("a", 10)
	if len(got) != 3 {
		t.Errorf("len = %d, want 3 (ring size)", len(got))
	}
	// Should have e3, e4, e5.
	if got[0].ID != "e3" {
		t.Errorf("oldest = %q, want e3", got[0].ID)
	}
}
