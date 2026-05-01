package admission

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
)

// TestAdmissionControllerLoop tests the admission controller's core flow
// at the store/controller layer: create objective → enqueue root → submit
// proposal → tick → verify materialization.
// This is NOT an MCP handler test — see mcpserver/objective_integration_test.go
// for the full agent-facing contract test (T36).
func TestAdmissionControllerLoop(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatalf("sqlitestore.New: %v", err)
	}
	defer store.Close()

	objStore := objective.NewSQLiteStore(store.DB())
	propStore := proposal.NewSQLiteStore(store.DB())
	memQueue := queue.NewMemoryQueue(store, 100)
	reader := newMockMissionReader()

	ac := New(
		propStore, objStore, reader, memQueue, store, store.Mailbox(),
		WithInterval(50*time.Millisecond),
	)

	// Create objective.
	obj := &objective.Objective{
		ID: "ctrl-test", Description: "Controller loop test",
		MaxMissions: 10, MaxDepth: 3, MaxBranching: 5,
		MaxRuntime: 1 * time.Hour, CreatedBy: "test",
	}
	if err := objStore.Create(ctx, obj); err != nil {
		t.Fatalf("create objective: %v", err)
	}

	// Enqueue root mission.
	rootPayload, _ := json.Marshal(queue.MissionPayload{
		Task: "root", RuntimeType: "claude", ObjectiveID: "ctrl-test",
	})
	root := &queue.Mission{
		AgentTask: "root", Payload: rootPayload, ObjectiveID: "ctrl-test",
	}
	if err := memQueue.Enqueue(ctx, root, &model.AgentEntry{
		Task: "root", RuntimeType: "claude", Status: model.StatusQueued, Mode: "queued",
	}); err != nil {
		t.Fatalf("enqueue root: %v", err)
	}
	reader.add(root)
	objStore.IncrementMissionCount(ctx, "ctrl-test")

	// Dequeue root so it doesn't pollute the child check.
	dequeued, _ := memQueue.Dequeue(ctx)
	memQueue.Ack(ctx, dequeued.ID)

	// Submit proposal.
	prop := &proposal.MissionProposal{
		ObjectiveID: "ctrl-test", ParentMission: root.ID, ProposedBy: "root",
		Task: "investigate host-X", DedupeKey: "investigate:host=X",
		Rationale: "Found suspicious activity",
	}
	if err := propStore.Submit(ctx, prop); err != nil {
		t.Fatalf("submit proposal: %v", err)
	}

	// Tick — admission evaluates and materializes.
	ac.tick(ctx)

	// Verify child was materialized: queue should have exactly 1 pending mission.
	qLen, _ := memQueue.Len(ctx)
	if qLen != 1 {
		t.Fatalf("queue len = %d after admission tick, want 1 (child mission)", qLen)
	}

	// Dequeue and verify it's the child.
	child, _ := memQueue.Dequeue(ctx)
	if child.ObjectiveID != "ctrl-test" {
		t.Errorf("child objective = %q, want ctrl-test", child.ObjectiveID)
	}
	if child.Depth != 1 {
		t.Errorf("child depth = %d, want 1", child.Depth)
	}
	if child.DedupeKey != "investigate:host=X" {
		t.Errorf("child dedupe = %q", child.DedupeKey)
	}

	// Verify objective counters.
	objAfter, _ := objStore.Get(ctx, "ctrl-test")
	if objAfter.MissionCount < 2 {
		t.Errorf("mission_count = %d, want >= 2", objAfter.MissionCount)
	}
}

// TestCircuitBreakerFreezesObjective verifies the circuit breaker trips
// when fanout exceeds findings.
func TestCircuitBreakerFreezesObjective(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "test-cb.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		t.Fatalf("sqlitestore.New: %v", err)
	}
	defer store.Close()

	objStore := objective.NewSQLiteStore(store.DB())
	propStore := proposal.NewSQLiteStore(store.DB())
	reader := newMockMissionReader()
	memQueue := queue.NewMemoryQueue(store, 100)

	policy := DefaultPolicy()
	policy.CircuitBreakerRatio = 2

	ac := New(
		propStore, objStore, reader, memQueue, store, store.Mailbox(),
		WithInterval(50*time.Millisecond),
		WithPolicy(policy),
	)

	obj := &objective.Objective{
		ID: "cb-test", Description: "Circuit breaker test",
		MaxMissions: 50, MaxDepth: 5, MaxBranching: 10, MaxRuntime: 1 * time.Hour,
	}
	objStore.Create(ctx, obj)

	// 5 missions, 0 findings → ratio exceeded.
	for i := 0; i < 5; i++ {
		objStore.IncrementMissionCount(ctx, "cb-test")
	}

	rootPayload, _ := json.Marshal(queue.MissionPayload{
		Task: "root", RuntimeType: "claude", ObjectiveID: "cb-test",
	})
	root := &queue.Mission{
		ID: 1, AgentTask: "root", ObjectiveID: "cb-test",
		Payload: rootPayload, Status: queue.StatusCompleted,
	}
	reader.add(root)

	prop := &proposal.MissionProposal{
		ObjectiveID: "cb-test", ParentMission: 1, ProposedBy: "root",
		Task: "speculative", DedupeKey: "speculate:X", Rationale: "Curious",
		Evidence: json.RawMessage(`{"alert":"ALERT-123"}`),
	}
	propStore.Submit(ctx, prop)

	ac.tick(ctx)

	objAfter, _ := objStore.Get(ctx, "cb-test")
	if objAfter.Status != objective.StatusFrozen {
		t.Errorf("status = %q, want frozen", objAfter.Status)
	}
}
