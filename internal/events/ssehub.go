package events

import (
	"sync"
)

// DefaultSubscriberBuffer is the channel buffer size for SSE subscribers.
const DefaultSubscriberBuffer = 64

// SSEHub manages per-task and global subscribers for server-sent events.
// Broadcast is non-blocking: events are dropped for slow subscribers.
type SSEHub struct {
	// Per-task subscribers: map[task] -> set of channels.
	taskSubs map[string]map[chan Event]struct{}
	// Global subscribers (watching all agents).
	globalSubs map[chan Event]struct{}
	mu         sync.RWMutex
	bufSize    int
}

// NewSSEHub creates an SSEHub with the given subscriber buffer size.
// If bufSize is 0, DefaultSubscriberBuffer is used.
func NewSSEHub(bufSize int) *SSEHub {
	if bufSize <= 0 {
		bufSize = DefaultSubscriberBuffer
	}
	return &SSEHub{
		taskSubs:   make(map[string]map[chan Event]struct{}),
		globalSubs: make(map[chan Event]struct{}),
		bufSize:    bufSize,
	}
}

// Subscribe creates a new subscriber channel for the given task.
// If task is empty, subscribes to all agents (global subscriber).
// The caller must call Unsubscribe when done to avoid leaks.
func (h *SSEHub) Subscribe(task string) chan Event {
	ch := make(chan Event, h.bufSize)

	h.mu.Lock()
	defer h.mu.Unlock()

	if task == "" {
		h.globalSubs[ch] = struct{}{}
	} else {
		subs, ok := h.taskSubs[task]
		if !ok {
			subs = make(map[chan Event]struct{})
			h.taskSubs[task] = subs
		}
		subs[ch] = struct{}{}
	}

	return ch
}

// Unsubscribe removes a subscriber channel. The channel is closed.
// If task is empty, removes from global subscribers.
func (h *SSEHub) Unsubscribe(task string, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if task == "" {
		delete(h.globalSubs, ch)
	} else {
		if subs, ok := h.taskSubs[task]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.taskSubs, task)
			}
		}
	}

	close(ch)
}

// Broadcast sends an event to all subscribers for the given task and all
// global subscribers. Non-blocking: events are dropped for full channels.
func (h *SSEHub) Broadcast(task string, e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Send to task-specific subscribers.
	if subs, ok := h.taskSubs[task]; ok {
		for ch := range subs {
			select {
			case ch <- e:
			default:
				// Drop on full — subscriber can catch up via ?since=
			}
		}
	}

	// Send to global subscribers.
	for ch := range h.globalSubs {
		select {
		case ch <- e:
		default:
			// Drop on full.
		}
	}
}

// SubscriberCount returns the number of subscribers for a task.
// If task is empty, returns global subscriber count.
func (h *SSEHub) SubscriberCount(task string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if task == "" {
		return len(h.globalSubs)
	}
	return len(h.taskSubs[task])
}

// TotalSubscribers returns the total number of all subscribers (task + global).
func (h *SSEHub) TotalSubscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	total := len(h.globalSubs)
	for _, subs := range h.taskSubs {
		total += len(subs)
	}
	return total
}
