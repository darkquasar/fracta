package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
)

func testStore(t *testing.T) *sqlitestore.SQLiteStore {
	t.Helper()
	s, err := sqlitestore.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testPayload(t *testing.T) json.RawMessage {
	t.Helper()
	p := MissionPayload{Task: "test-task", RuntimeType: "claude"}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMemoryQueue_EnqueueDequeue(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	payload := testPayload(t)

	mission := &Mission{AgentTask: "agent-1", Payload: payload, Priority: 0}
	agent := &model.AgentEntry{
		Task:   "agent-1",
		Status: model.StatusQueued,
		Mode:   "queued",
	}

	if err := q.Enqueue(ctx, mission, agent); err != nil {
		t.Fatal(err)
	}

	// Verify mission ID was assigned.
	if mission.ID == 0 {
		t.Fatal("expected mission ID to be assigned")
	}

	// Verify agent was persisted with MissionID.
	a, err := store.FindAgent(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("expected agent to be persisted")
	}
	if a.MissionID != mission.ID {
		t.Errorf("expected agent.MissionID=%d, got %d", mission.ID, a.MissionID)
	}

	// Dequeue.
	got, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != mission.ID {
		t.Errorf("expected mission ID %d, got %d", mission.ID, got.ID)
	}
	if got.Status != StatusClaimed {
		t.Errorf("expected status %q, got %q", StatusClaimed, got.Status)
	}
}

func TestMemoryQueue_Ack(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	mission := &Mission{AgentTask: "ack-agent", Payload: testPayload(t)}
	agent := &model.AgentEntry{Task: "ack-agent", Status: model.StatusQueued, Mode: "queued"}

	q.Enqueue(ctx, mission, agent)
	got, _ := q.Dequeue(ctx)

	if err := q.Ack(ctx, got.ID); err != nil {
		t.Fatal(err)
	}

	status, err := q.Status(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusCompleted {
		t.Errorf("expected %q, got %q", StatusCompleted, status)
	}
}

func TestMemoryQueue_Fail(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	mission := &Mission{AgentTask: "fail-agent", Payload: testPayload(t)}
	agent := &model.AgentEntry{Task: "fail-agent", Status: model.StatusQueued, Mode: "queued"}

	q.Enqueue(ctx, mission, agent)
	got, _ := q.Dequeue(ctx)

	if err := q.Fail(ctx, got.ID, "something broke"); err != nil {
		t.Fatal(err)
	}

	status, _ := q.Status(ctx, got.ID)
	if status != StatusFailed {
		t.Errorf("expected %q, got %q", StatusFailed, status)
	}
}

func TestMemoryQueue_CancelPending(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	mission := &Mission{AgentTask: "cancel-pending", Payload: testPayload(t)}
	agent := &model.AgentEntry{Task: "cancel-pending", Status: model.StatusQueued, Mode: "queued"}

	q.Enqueue(ctx, mission, agent)

	if err := q.Cancel(ctx, mission.ID); err != nil {
		t.Fatal(err)
	}

	status, _ := q.Status(ctx, mission.ID)
	if status != StatusCancelled {
		t.Errorf("expected %q, got %q", StatusCancelled, status)
	}

	// Dequeue should skip the cancelled mission and block.
	dctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err := q.Dequeue(dctx)
	if err == nil {
		t.Fatal("expected dequeue to time out for cancelled mission")
	}
}

func TestMemoryQueue_CancelClaimed(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	mission := &Mission{AgentTask: "cancel-claimed", Payload: testPayload(t)}
	agent := &model.AgentEntry{Task: "cancel-claimed", Status: model.StatusQueued, Mode: "queued"}

	q.Enqueue(ctx, mission, agent)
	got, _ := q.Dequeue(ctx)

	if err := q.Cancel(ctx, got.ID); err != nil {
		t.Fatal(err)
	}

	status, _ := q.Status(ctx, got.ID)
	if status != StatusCancelled {
		t.Errorf("expected %q, got %q", StatusCancelled, status)
	}
}

func TestMemoryQueue_Len(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		task := "len-agent-" + string(rune('a'+i))
		mission := &Mission{AgentTask: task, Payload: testPayload(t)}
		agent := &model.AgentEntry{Task: task, Status: model.StatusQueued, Mode: "queued"}
		q.Enqueue(ctx, mission, agent)
	}

	n, err := q.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected Len=3, got %d", n)
	}

	// Dequeue one.
	q.Dequeue(ctx)

	n, _ = q.Len(ctx)
	if n != 2 {
		t.Errorf("expected Len=2 after dequeue, got %d", n)
	}
}

func TestMemoryQueue_StatusNotFound(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	_, err := q.Status(context.Background(), 9999)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMission_DAGFields(t *testing.T) {
	parentID := int64(42)
	m := Mission{
		AgentTask:   "dag-agent",
		ObjectiveID: "obj-1",
		ParentID:    &parentID,
		Depth:       2,
		DedupeKey:   "investigate:host=srv-01",
		ProposedBy:  "agent-alpha",
	}
	if m.ObjectiveID != "obj-1" {
		t.Errorf("ObjectiveID = %q, want %q", m.ObjectiveID, "obj-1")
	}
	if m.ParentID == nil || *m.ParentID != 42 {
		t.Errorf("ParentID = %v, want 42", m.ParentID)
	}
	if m.Depth != 2 {
		t.Errorf("Depth = %d, want 2", m.Depth)
	}
	if m.DedupeKey != "investigate:host=srv-01" {
		t.Errorf("DedupeKey = %q, want %q", m.DedupeKey, "investigate:host=srv-01")
	}
	if m.ProposedBy != "agent-alpha" {
		t.Errorf("ProposedBy = %q, want %q", m.ProposedBy, "agent-alpha")
	}
}

func TestMissionPayload_ObjectiveContext(t *testing.T) {
	p := MissionPayload{
		Task:        "test-task",
		RuntimeType:    "claude",
		ObjectiveID: "obj-1",
		MissionID:   123,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got MissionPayload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ObjectiveID != "obj-1" {
		t.Errorf("ObjectiveID = %q, want %q", got.ObjectiveID, "obj-1")
	}
	if got.MissionID != 123 {
		t.Errorf("MissionID = %d, want 123", got.MissionID)
	}
}

func TestMission_DAGFields_JSON(t *testing.T) {
	parentID := int64(7)
	m := Mission{
		ID:          1,
		AgentTask:   "test",
		ObjectiveID: "obj-2",
		ParentID:    &parentID,
		Depth:       3,
		DedupeKey:   "test:key",
		ProposedBy:  "agent-x",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got Mission
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ObjectiveID != "obj-2" {
		t.Errorf("ObjectiveID round-trip: %q", got.ObjectiveID)
	}
	if got.ParentID == nil || *got.ParentID != 7 {
		t.Errorf("ParentID round-trip: %v", got.ParentID)
	}
	if got.Depth != 3 {
		t.Errorf("Depth round-trip: %d", got.Depth)
	}
}

func TestMemoryQueue_DAGFields_RoundTrip(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx := context.Background()
	parentID := int64(99)
	mission := &Mission{
		AgentTask:   "dag-test",
		Payload:     testPayload(t),
		ObjectiveID: "obj-dag",
		ParentID:    &parentID,
		Depth:       2,
		DedupeKey:   "investigate:host=srv-01",
		ProposedBy:  "agent-alpha",
	}
	agent := &model.AgentEntry{Task: "dag-test", Status: model.StatusQueued, Mode: "queued"}

	if err := q.Enqueue(ctx, mission, agent); err != nil {
		t.Fatal(err)
	}

	got, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectiveID != "obj-dag" {
		t.Errorf("ObjectiveID = %q, want %q", got.ObjectiveID, "obj-dag")
	}
	if got.ParentID == nil || *got.ParentID != 99 {
		t.Errorf("ParentID = %v, want 99", got.ParentID)
	}
	if got.Depth != 2 {
		t.Errorf("Depth = %d, want 2", got.Depth)
	}
	if got.DedupeKey != "investigate:host=srv-01" {
		t.Errorf("DedupeKey = %q", got.DedupeKey)
	}
	if got.ProposedBy != "agent-alpha" {
		t.Errorf("ProposedBy = %q", got.ProposedBy)
	}
}

func TestMemoryQueue_DequeueContextCancel(t *testing.T) {
	store := testStore(t)
	q := NewMemoryQueue(store, 10)
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := q.Dequeue(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
