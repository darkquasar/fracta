package events

import (
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

// AgentSnapshot is the in-memory per-agent state projected from events.
// It provides fast read access for fracta_list and fracta_peek without DB queries.
type AgentSnapshot struct {
	Task               string            `json:"task"`
	Status             model.AgentStatus `json:"status"`
	Backend            string            `json:"backend"`
	PodName            string            `json:"pod_name,omitempty"`
	StartedAt          time.Time         `json:"started_at"`
	LastHeartbeatAt    time.Time         `json:"last_heartbeat_at"`
	LastEventAt        time.Time         `json:"last_event_at"`
	CurrentPhase       string            `json:"current_phase"`
	CurrentTool        string            `json:"current_tool,omitempty"`
	LastMessageExcerpt string            `json:"last_message_excerpt,omitempty"`
	MissionID          int64             `json:"mission_id,omitempty"`
	ObjectiveID        string            `json:"objective_id,omitempty"`

	// terminalAt is set when the agent enters a terminal status (Completed, Failed, Stopped).
	// Used by TTL cleanup to garbage-collect stale snapshots.
	terminalAt time.Time
}

// IsTerminal returns true if the snapshot is in a terminal status.
func (s *AgentSnapshot) IsTerminal() bool {
	switch s.Status {
	case model.StatusCompleted, model.StatusFailed, model.StatusStopped:
		return true
	}
	return false
}

// SnapshotStore is a thread-safe in-memory map of agent snapshots.
// It supports update-by-function, get, list-all, remove, and TTL cleanup.
type SnapshotStore struct {
	snapshots map[string]*AgentSnapshot
	mu        sync.RWMutex
	ttl       time.Duration
}

// NewSnapshotStore creates a new SnapshotStore with the given TTL for terminal entries.
// If ttl is 0, a default of 30 minutes is used.
func NewSnapshotStore(ttl time.Duration) *SnapshotStore {
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	return &SnapshotStore{
		snapshots: make(map[string]*AgentSnapshot),
		ttl:       ttl,
	}
}

// Update applies fn to the snapshot for the given task. If no snapshot exists,
// a new one is created with the task field pre-set.
func (s *SnapshotStore) Update(task string, fn func(*AgentSnapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, ok := s.snapshots[task]
	if !ok {
		snap = &AgentSnapshot{Task: task}
		s.snapshots[task] = snap
	}
	fn(snap)
}

// UpdateIfExists applies fn only if a snapshot already exists for the task.
// Returns false if no snapshot was found (no new entry created).
func (s *SnapshotStore) UpdateIfExists(task string, fn func(*AgentSnapshot)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, ok := s.snapshots[task]
	if !ok {
		return false
	}
	fn(snap)
	return true
}

// Get returns a copy of the snapshot for the given task, or nil if not found.
func (s *SnapshotStore) Get(task string) *AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap, ok := s.snapshots[task]
	if !ok {
		return nil
	}
	// Return a copy to avoid data races on the caller side.
	cp := *snap
	return &cp
}

// All returns a copy of all snapshots.
func (s *SnapshotStore) All() map[string]AgentSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]AgentSnapshot, len(s.snapshots))
	for k, v := range s.snapshots {
		result[k] = *v
	}
	return result
}

// Remove deletes the snapshot for the given task.
func (s *SnapshotStore) Remove(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, task)
}

// Cleanup removes snapshots that have been in terminal status for longer than
// the configured TTL. Call periodically (e.g., from a background goroutine).
func (s *SnapshotStore) Cleanup(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for task, snap := range s.snapshots {
		if snap.IsTerminal() && !snap.terminalAt.IsZero() && now.Sub(snap.terminalAt) > s.ttl {
			delete(s.snapshots, task)
			removed++
		}
	}
	return removed
}

// Len returns the number of snapshots in the store.
func (s *SnapshotStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.snapshots)
}
