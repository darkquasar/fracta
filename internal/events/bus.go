package events

import (
	"context"
	"log/slog"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// Bus is the interface through which fracta components emit events.
// Emit is fire-and-forget; sink failures must not break core flows.
type Bus interface {
	Emit(ctx context.Context, e Event)
}

// Sink processes a single event. Implementations decide how to handle it
// (log, persist, forward to Kubernetes Events, etc.).
type Sink interface {
	Handle(ctx context.Context, e Event) error
}

// NoopBus discards all events. Safe default for tests and legacy paths.
type NoopBus struct{}

// Emit does nothing.
func (NoopBus) Emit(context.Context, Event) {}

// FanoutBus emits events to all registered sinks synchronously.
// Sink failures are logged but never returned to the caller.
type FanoutBus struct {
	sinks []Sink
	log   *slog.Logger
}

// NewFanoutBus creates a FanoutBus that fans out to the given sinks.
func NewFanoutBus(sinks ...Sink) *FanoutBus {
	return &FanoutBus{
		sinks: sinks,
		log:   fractalog.Component("events_bus"),
	}
}

// AddSink appends a sink to the bus after construction. This allows
// late-binding sinks that depend on components built after the bus
// (e.g., K8sEventSink depends on a recorder from the K8s backend).
func (b *FanoutBus) AddSink(s Sink) {
	b.sinks = append(b.sinks, s)
}

// Emit sends the event to every registered sink. Failures are logged
// but never propagated to the caller.
func (b *FanoutBus) Emit(ctx context.Context, e Event) {
	for _, s := range b.sinks {
		if err := s.Handle(ctx, e); err != nil {
			b.log.Warn("sink failed", "error", err)
		}
	}
}

// sinkName returns a human-readable name for a sink, using the Stringer
// interface if available, otherwise the type name.
func sinkName(s Sink) string {
	if n, ok := s.(interface{ String() string }); ok {
		return n.String()
	}
	return "unknown"
}
