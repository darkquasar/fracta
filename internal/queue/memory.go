package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
)

// MemoryQueue is an in-process MissionQueue backed by a buffered channel.
// It persists agent records via the Store on Enqueue.
type MemoryQueue struct {
	ch       chan *Mission
	mu       sync.Mutex
	missions map[int64]*Mission
	nextID   atomic.Int64
	store    state.Store
	closed   chan struct{}
}

// NewMemoryQueue creates a MemoryQueue with a buffered channel of the given size.
// store is used to persist agent records on Enqueue (uniform contract).
func NewMemoryQueue(store state.Store, bufferSize int) *MemoryQueue {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	q := &MemoryQueue{
		ch:       make(chan *Mission, bufferSize),
		missions: make(map[int64]*Mission),
		store:    store,
		closed:   make(chan struct{}),
	}
	return q
}

// Enqueue adds a mission to the queue and persists the agent record via Store.
func (q *MemoryQueue) Enqueue(ctx context.Context, m *Mission, agent *model.AgentEntry) error {
	id := q.nextID.Add(1)
	m.ID = id
	m.Status = StatusPending
	m.CreatedAt = time.Now()

	// Set agent's MissionID before persisting.
	agent.MissionID = id

	// Persist agent via Store.WithLock (uniform contract — caller never persists agent separately).
	if err := q.store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, *agent)
		return nil
	}); err != nil {
		return fmt.Errorf("memory queue: persist agent: %w", err)
	}

	q.mu.Lock()
	q.missions[id] = m
	q.mu.Unlock()

	select {
	case q.ch <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dequeue blocks until a mission is available, claims it, and returns it.
func (q *MemoryQueue) Dequeue(ctx context.Context) (*Mission, error) {
	for {
		select {
		case m := <-q.ch:
			q.mu.Lock()
			stored, ok := q.missions[m.ID]
			if !ok || stored.Status != StatusPending {
				// Mission was cancelled while in channel — skip.
				q.mu.Unlock()
				continue
			}
			now := time.Now()
			stored.Status = StatusClaimed
			stored.ClaimedAt = &now
			q.mu.Unlock()
			return stored, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.closed:
			return nil, fmt.Errorf("queue closed")
		}
	}
}

// Ack marks a claimed mission as completed.
func (q *MemoryQueue) Ack(_ context.Context, missionID int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.missions[missionID]
	if !ok {
		return ErrNotFound
	}
	m.Status = StatusCompleted
	return nil
}

// Fail marks a claimed mission as failed.
func (q *MemoryQueue) Fail(_ context.Context, missionID int64, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.missions[missionID]
	if !ok {
		return ErrNotFound
	}
	m.Status = StatusFailed
	m.Error = reason
	return nil
}

// Len returns the number of pending missions.
func (q *MemoryQueue) Len(_ context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, m := range q.missions {
		if m.Status == StatusPending {
			count++
		}
	}
	return count, nil
}

// Status returns the current status of a mission.
func (q *MemoryQueue) Status(_ context.Context, missionID int64) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.missions[missionID]
	if !ok {
		return "", ErrNotFound
	}
	return m.Status, nil
}

// Cancel removes a pending mission or marks a claimed mission as cancelled.
func (q *MemoryQueue) Cancel(_ context.Context, missionID int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.missions[missionID]
	if !ok {
		return ErrNotFound
	}
	switch m.Status {
	case StatusPending:
		// Mark as cancelled — Dequeue will skip it.
		m.Status = StatusCancelled
		return nil
	case StatusClaimed:
		// Worker is executing — set cancelled so worker detects via Status poll.
		m.Status = StatusCancelled
		return nil
	default:
		// Already terminal.
		return ErrNotFound
	}
}

// Close releases queue resources.
func (q *MemoryQueue) Close() error {
	select {
	case <-q.closed:
		// Already closed.
	default:
		close(q.closed)
	}
	return nil
}
