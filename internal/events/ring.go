package events

import (
	"sync"
	"time"
)

// DefaultRingSize is the default number of events per agent ring buffer.
const DefaultRingSize = 100

// AgentRing is a fixed-size circular buffer of events for a single agent.
type AgentRing struct {
	events []Event
	head   int // next write position
	count  int // number of valid entries (0..cap)
	mu     sync.RWMutex
}

// NewAgentRing creates a ring buffer with the given capacity.
func NewAgentRing(size int) *AgentRing {
	if size <= 0 {
		size = DefaultRingSize
	}
	return &AgentRing{
		events: make([]Event, size),
	}
}

// Append adds an event to the ring, overwriting the oldest if full.
func (r *AgentRing) Append(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events[r.head] = e
	r.head = (r.head + 1) % len(r.events)
	if r.count < len(r.events) {
		r.count++
	}
}

// Recent returns the last n events in chronological order (oldest first).
// If n > count, returns all available events.
func (r *AgentRing) Recent(n int) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n <= 0 || r.count == 0 {
		return nil
	}
	if n > r.count {
		n = r.count
	}

	result := make([]Event, n)
	// Start index: head points to next write, so the most recent is at head-1.
	// The oldest of the last n is at head-n.
	cap := len(r.events)
	start := (r.head - n + cap) % cap
	for i := 0; i < n; i++ {
		result[i] = r.events[(start+i)%cap]
	}
	return result
}

// Since returns all events after the given event ID, in chronological order.
// Returns nil if the event ID is not found in the ring (evicted).
// Returns an empty slice if the event ID is the most recent event.
func (r *AgentRing) Since(eventID string) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil
	}

	// Find the position of eventID in the ring.
	cap := len(r.events)
	oldest := (r.head - r.count + cap) % cap

	foundIdx := -1
	for i := 0; i < r.count; i++ {
		idx := (oldest + i) % cap
		if r.events[idx].ID == eventID {
			foundIdx = i
			break
		}
	}

	if foundIdx < 0 {
		// Event not found — it has been evicted or never existed.
		return nil
	}

	// Return everything after foundIdx.
	remaining := r.count - foundIdx - 1
	if remaining <= 0 {
		return []Event{}
	}

	result := make([]Event, remaining)
	for i := 0; i < remaining; i++ {
		idx := (oldest + foundIdx + 1 + i) % cap
		result[i] = r.events[idx]
	}
	return result
}

// Len returns the number of events in the ring.
func (r *AgentRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// EventStore holds ring buffers for all agents. Thread-safe.
type EventStore struct {
	rings    map[string]*AgentRing
	mu       sync.RWMutex
	ringSize int
	ttl      time.Duration

	// terminalTimes tracks when an agent entered terminal status (for TTL cleanup).
	terminalTimes map[string]time.Time
}

// NewEventStore creates an EventStore with the given ring size per agent.
// If ringSize is 0, DefaultRingSize is used. If ttl is 0, 30 minutes is used.
func NewEventStore(ringSize int, ttl time.Duration) *EventStore {
	if ringSize <= 0 {
		ringSize = DefaultRingSize
	}
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	return &EventStore{
		rings:         make(map[string]*AgentRing),
		ringSize:      ringSize,
		ttl:           ttl,
		terminalTimes: make(map[string]time.Time),
	}
}

// Append adds an event to the ring for the given task. Creates the ring if needed.
func (s *EventStore) Append(task string, e Event) {
	s.mu.Lock()
	ring, ok := s.rings[task]
	if !ok {
		ring = NewAgentRing(s.ringSize)
		s.rings[task] = ring
	}
	s.mu.Unlock()

	ring.Append(e)
}

// Recent returns the last n events for an agent in chronological order.
func (s *EventStore) Recent(task string, n int) []Event {
	s.mu.RLock()
	ring, ok := s.rings[task]
	s.mu.RUnlock()

	if !ok {
		return nil
	}
	return ring.Recent(n)
}

// Since returns all events after the given event ID for an agent.
// Returns nil if the ring doesn't exist or the event ID has been evicted.
func (s *EventStore) Since(task string, eventID string) []Event {
	s.mu.RLock()
	ring, ok := s.rings[task]
	s.mu.RUnlock()

	if !ok {
		return nil
	}
	return ring.Since(eventID)
}

// Remove deletes the ring buffer for a given task.
func (s *EventStore) Remove(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rings, task)
	delete(s.terminalTimes, task)
}

// MarkTerminal records that an agent entered terminal status at the given time.
func (s *EventStore) MarkTerminal(task string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminalTimes[task] = at
}

// Cleanup removes rings for agents that have been terminal for longer than TTL.
func (s *EventStore) Cleanup(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for task, termAt := range s.terminalTimes {
		if now.Sub(termAt) > s.ttl {
			delete(s.rings, task)
			delete(s.terminalTimes, task)
			removed++
		}
	}
	return removed
}

// RingSize returns the configured per-agent ring buffer size.
func (s *EventStore) RingSize() int {
	return s.ringSize
}

// Len returns the number of active ring buffers.
func (s *EventStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rings)
}
