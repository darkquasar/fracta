package events

import (
	"testing"
	"time"
)

func TestSSEHub_SubscribeAndBroadcast(t *testing.T) {
	hub := NewSSEHub(0)

	ch := hub.Subscribe("agent-1")
	defer hub.Unsubscribe("agent-1", ch)

	e := makeEvent("e1")
	hub.Broadcast("agent-1", e)

	select {
	case got := <-ch:
		if got.ID != "e1" {
			t.Errorf("got ID = %q, want %q", got.ID, "e1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestSSEHub_GlobalSubscriber(t *testing.T) {
	hub := NewSSEHub(0)

	// Subscribe globally (empty task).
	ch := hub.Subscribe("")
	defer hub.Unsubscribe("", ch)

	// Broadcast to a specific task — global should receive it.
	e := makeEvent("e1")
	hub.Broadcast("agent-1", e)

	select {
	case got := <-ch:
		if got.ID != "e1" {
			t.Errorf("got ID = %q, want %q", got.ID, "e1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("global subscriber didn't receive event")
	}
}

func TestSSEHub_TaskSubscriberDoesNotReceiveOtherTasks(t *testing.T) {
	hub := NewSSEHub(0)

	ch := hub.Subscribe("agent-1")
	defer hub.Unsubscribe("agent-1", ch)

	// Broadcast to a different task.
	hub.Broadcast("agent-2", makeEvent("e1"))

	select {
	case got := <-ch:
		t.Errorf("should not receive event for other task, got %q", got.ID)
	case <-time.After(50 * time.Millisecond):
		// Expected — no event received.
	}
}

func TestSSEHub_MultipleSubscribers(t *testing.T) {
	hub := NewSSEHub(0)

	ch1 := hub.Subscribe("agent-1")
	ch2 := hub.Subscribe("agent-1")
	defer hub.Unsubscribe("agent-1", ch1)
	defer hub.Unsubscribe("agent-1", ch2)

	hub.Broadcast("agent-1", makeEvent("e1"))

	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.ID != "e1" {
				t.Errorf("subscriber %d: got ID = %q", i, got.ID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("subscriber %d: timed out", i)
		}
	}
}

func TestSSEHub_DropOnFull(t *testing.T) {
	hub := NewSSEHub(2) // buffer of 2

	ch := hub.Subscribe("agent-1")
	defer hub.Unsubscribe("agent-1", ch)

	// Fill the buffer.
	hub.Broadcast("agent-1", makeEvent("e1"))
	hub.Broadcast("agent-1", makeEvent("e2"))
	// This should be dropped (non-blocking).
	hub.Broadcast("agent-1", makeEvent("e3"))

	// Drain and verify we get e1, e2 but not e3.
	got1 := <-ch
	got2 := <-ch
	if got1.ID != "e1" || got2.ID != "e2" {
		t.Errorf("got %q, %q; want e1, e2", got1.ID, got2.ID)
	}

	select {
	case extra := <-ch:
		t.Errorf("should not have more events, got %q", extra.ID)
	default:
		// Expected.
	}
}

func TestSSEHub_UnsubscribeCleanup(t *testing.T) {
	hub := NewSSEHub(0)

	ch := hub.Subscribe("agent-1")
	if hub.SubscriberCount("agent-1") != 1 {
		t.Fatal("expected 1 subscriber")
	}

	hub.Unsubscribe("agent-1", ch)

	if hub.SubscriberCount("agent-1") != 0 {
		t.Error("expected 0 subscribers after unsubscribe")
	}

	// Verify channel is closed.
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after unsubscribe")
	}
}

func TestSSEHub_UnsubscribeGlobal(t *testing.T) {
	hub := NewSSEHub(0)

	ch := hub.Subscribe("")
	if hub.SubscriberCount("") != 1 {
		t.Fatal("expected 1 global subscriber")
	}

	hub.Unsubscribe("", ch)

	if hub.SubscriberCount("") != 0 {
		t.Error("expected 0 global subscribers after unsubscribe")
	}
}

func TestSSEHub_TotalSubscribers(t *testing.T) {
	hub := NewSSEHub(0)

	ch1 := hub.Subscribe("agent-1")
	ch2 := hub.Subscribe("agent-2")
	ch3 := hub.Subscribe("") // global
	defer hub.Unsubscribe("agent-1", ch1)
	defer hub.Unsubscribe("agent-2", ch2)
	defer hub.Unsubscribe("", ch3)

	if hub.TotalSubscribers() != 3 {
		t.Errorf("TotalSubscribers = %d, want 3", hub.TotalSubscribers())
	}
}

func TestSSEHub_BroadcastNoSubscribers(t *testing.T) {
	hub := NewSSEHub(0)
	// Should not panic.
	hub.Broadcast("agent-1", makeEvent("e1"))
}

func TestSSEHub_GlobalReceivesAllTasks(t *testing.T) {
	hub := NewSSEHub(0)

	ch := hub.Subscribe("")
	defer hub.Unsubscribe("", ch)

	hub.Broadcast("agent-1", makeEvent("e1"))
	hub.Broadcast("agent-2", makeEvent("e2"))

	got1 := <-ch
	got2 := <-ch
	if got1.ID != "e1" || got2.ID != "e2" {
		t.Errorf("global got %q, %q; want e1, e2", got1.ID, got2.ID)
	}
}
