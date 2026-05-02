package events

import (
	"context"
)

// RingBufferSink implements Sink. It appends events to the EventStore ring
// buffer and broadcasts to SSE subscribers via the SSEHub.
type RingBufferSink struct {
	store *EventStore
	hub   *SSEHub
}

// NewRingBufferSink creates a RingBufferSink that appends to the given store
// and broadcasts to the given hub. hub may be nil if SSE is not needed.
func NewRingBufferSink(store *EventStore, hub *SSEHub) *RingBufferSink {
	return &RingBufferSink{
		store: store,
		hub:   hub,
	}
}

// Handle appends the event to the ring buffer and broadcasts to SSE subscribers.
func (s *RingBufferSink) Handle(_ context.Context, e Event) error {
	if e.Task == "" {
		return nil
	}

	s.store.Append(e.Task, e)

	// Track terminal status for ring buffer TTL cleanup.
	if e.Action == "lifecycle.completed" || e.Action == "lifecycle.failed" || e.Action == "lifecycle.stopped" {
		s.store.MarkTerminal(e.Task, e.Time)
	}

	if s.hub != nil {
		s.hub.Broadcast(e.Task, e)
	}

	return nil
}

// String returns the sink name for logging.
func (s *RingBufferSink) String() string { return "RingBufferSink" }
