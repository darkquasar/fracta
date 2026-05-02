package orchestrator

import (
	"sync"

	"github.com/darkquasar/fracta/internal/host"
)

// ProcessRegistry maps task names to live StreamSessions. Thread-safe.
type ProcessRegistry struct {
	mu      sync.RWMutex
	handles map[string]host.StreamSession
}

// NewProcessRegistry creates an empty registry.
func NewProcessRegistry() *ProcessRegistry {
	return &ProcessRegistry{
		handles: make(map[string]host.StreamSession),
	}
}

// Register associates a task name with a StreamSession.
func (r *ProcessRegistry) Register(task string, h host.StreamSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handles[task] = h
}

// Get returns the StreamSession for a task, or nil if not found.
func (r *ProcessRegistry) Get(task string) host.StreamSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handles[task]
}

// Remove deletes a task's StreamSession from the registry.
func (r *ProcessRegistry) Remove(task string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handles, task)
}

// CloseAll closes all registered StreamSessions and empties the registry.
func (r *ProcessRegistry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for task, h := range r.handles {
		h.Close()
		delete(r.handles, task)
	}
}
