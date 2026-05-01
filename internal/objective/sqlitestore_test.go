package objective

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS objectives (
	id               TEXT PRIMARY KEY,
	description      TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL DEFAULT 'open',
	created_at       TEXT NOT NULL DEFAULT '',
	updated_at       TEXT NOT NULL DEFAULT '',
	created_by       TEXT NOT NULL DEFAULT '',
	max_missions     INTEGER NOT NULL DEFAULT 100,
	max_depth        INTEGER NOT NULL DEFAULT 5,
	max_runtime      INTEGER NOT NULL DEFAULT 14400000000000,
	max_branching    INTEGER NOT NULL DEFAULT 5,
	max_tokens       INTEGER NOT NULL DEFAULT 0,
	max_graph_writes INTEGER NOT NULL DEFAULT 0,
	mission_count    INTEGER NOT NULL DEFAULT 0,
	finding_count    INTEGER NOT NULL DEFAULT 0,
	tokens_used      INTEGER NOT NULL DEFAULT 0,
	graph_writes     INTEGER NOT NULL DEFAULT 0,
	outcome          TEXT NOT NULL DEFAULT '',
	outcome_data     TEXT
);
`

func setupSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLiteStore(db)
}

func TestSQLiteStore_CreateAndGet(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	o := &Objective{
		ID:          "obj-1",
		Description: "Find lateral movement",
		CreatedBy:   "chessmaster",
		MaxMissions: 50,
		MaxDepth:    3,
	}
	if err := s.Create(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "obj-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "obj-1" {
		t.Errorf("ID = %q, want %q", got.ID, "obj-1")
	}
	if got.Description != "Find lateral movement" {
		t.Errorf("Description = %q, want %q", got.Description, "Find lateral movement")
	}
	if got.Status != StatusOpen {
		t.Errorf("Status = %q, want %q", got.Status, StatusOpen)
	}
	if got.MaxMissions != 50 {
		t.Errorf("MaxMissions = %d, want 50", got.MaxMissions)
	}
	if got.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", got.MaxDepth)
	}
	// Defaults should be applied.
	if got.MaxBranching != DefaultMaxBranching {
		t.Errorf("MaxBranching = %d, want %d", got.MaxBranching, DefaultMaxBranching)
	}
	if got.MaxRuntime != DefaultMaxRuntime {
		t.Errorf("MaxRuntime = %v, want %v", got.MaxRuntime, DefaultMaxRuntime)
	}
}

func TestSQLiteStore_GetNotFound(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_Update(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	o := &Objective{ID: "obj-2", Description: "initial"}
	s.Create(ctx, o)

	o.Description = "updated description"
	o.Status = StatusAnswered
	o.Outcome = "confirmed C2"
	o.OutcomeData = json.RawMessage(`{"hosts":["srv-01"]}`)
	if err := s.Update(ctx, o); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(ctx, "obj-2")
	if got.Description != "updated description" {
		t.Errorf("Description = %q, want %q", got.Description, "updated description")
	}
	if got.Status != StatusAnswered {
		t.Errorf("Status = %q, want %q", got.Status, StatusAnswered)
	}
	if got.Outcome != "confirmed C2" {
		t.Errorf("Outcome = %q, want %q", got.Outcome, "confirmed C2")
	}
	if string(got.OutcomeData) != `{"hosts":["srv-01"]}` {
		t.Errorf("OutcomeData = %q", string(got.OutcomeData))
	}
}

func TestSQLiteStore_UpdateNotFound(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	err := s.Update(ctx, &Objective{ID: "nonexistent"})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_IncrementCounters(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	s.Create(ctx, &Objective{ID: "obj-3"})

	if err := s.IncrementMissionCount(ctx, "obj-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementMissionCount(ctx, "obj-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementFindingCount(ctx, "obj-3"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(ctx, "obj-3")
	if got.MissionCount != 2 {
		t.Errorf("MissionCount = %d, want 2", got.MissionCount)
	}
	if got.FindingCount != 1 {
		t.Errorf("FindingCount = %d, want 1", got.FindingCount)
	}
}

func TestSQLiteStore_IncrementCounters_NotFound(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	if err := s.IncrementMissionCount(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := s.IncrementFindingCount(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_ListByStatus(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	s.Create(ctx, &Objective{ID: "open-1"})
	s.Create(ctx, &Objective{ID: "open-2"})
	o3 := &Objective{ID: "answered-1"}
	s.Create(ctx, o3)
	o3.Status = StatusAnswered
	s.Update(ctx, o3)

	open, err := s.ListByStatus(ctx, StatusOpen)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Errorf("expected 2 open objectives, got %d", len(open))
	}

	answered, err := s.ListByStatus(ctx, StatusAnswered)
	if err != nil {
		t.Fatal(err)
	}
	if len(answered) != 1 {
		t.Errorf("expected 1 answered objective, got %d", len(answered))
	}
}

func TestSQLiteStore_MaxRuntime_RoundTrip(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	o := &Objective{ID: "rt-1", MaxRuntime: 2 * time.Hour}
	s.Create(ctx, o)

	got, _ := s.Get(ctx, "rt-1")
	if got.MaxRuntime != 2*time.Hour {
		t.Errorf("MaxRuntime = %v, want %v", got.MaxRuntime, 2*time.Hour)
	}
}
