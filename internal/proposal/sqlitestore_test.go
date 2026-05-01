package proposal

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS proposals (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	objective_id    TEXT NOT NULL,
	parent_mission  INTEGER NOT NULL,
	proposed_by     TEXT NOT NULL DEFAULT '',
	task            TEXT NOT NULL DEFAULT '',
	contract        TEXT NOT NULL DEFAULT '',
	priority        INTEGER NOT NULL DEFAULT 0,
	dedupe_key      TEXT NOT NULL DEFAULT '',
	rationale       TEXT NOT NULL DEFAULT '',
	evidence        TEXT,
	status          TEXT NOT NULL DEFAULT 'pending',
	created_at      TEXT NOT NULL DEFAULT '',
	reviewed_at     TEXT,
	rejection_note  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_proposals_pending
	ON proposals (status, priority DESC, created_at)
	WHERE status = 'pending';
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

func TestSQLiteStore_SubmitAndPending(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	p := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		ProposedBy:    "agent-alpha",
		Task:          "investigate host srv-01",
		Contract:      "check for C2 indicators",
		DedupeKey:     "investigate:host=srv-01",
		Rationale:     "suspicious beacon detected",
		Priority:      5,
		Evidence:      json.RawMessage(`{"alert_id":"A-123"}`),
	}
	if err := s.Submit(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Error("expected non-zero ID after submit")
	}
	if p.Status != StatusPending {
		t.Errorf("Status = %q, want %q", p.Status, StatusPending)
	}

	// Submit another lower-priority proposal.
	p2 := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		ProposedBy:    "agent-beta",
		Task:          "enrich IP",
		DedupeKey:     "enrich:ip=10.0.0.1",
		Rationale:     "outbound connection",
		Priority:      1,
	}
	s.Submit(ctx, p2)

	pending, err := s.PendingProposals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending proposals, got %d", len(pending))
	}
	// Higher priority first.
	if pending[0].Priority != 5 {
		t.Errorf("first proposal priority = %d, want 5", pending[0].Priority)
	}
	if pending[1].Priority != 1 {
		t.Errorf("second proposal priority = %d, want 1", pending[1].Priority)
	}
}

func TestSQLiteStore_Approve(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	p := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		DedupeKey:     "test:approve",
		Rationale:     "test",
	}
	s.Submit(ctx, p)

	if err := s.Approve(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	// Should no longer appear in pending.
	pending, _ := s.PendingProposals(ctx)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after approve, got %d", len(pending))
	}
}

func TestSQLiteStore_Reject(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	p := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		DedupeKey:     "test:reject",
		Rationale:     "test",
	}
	s.Submit(ctx, p)

	if err := s.Reject(ctx, p.ID, "budget exceeded"); err != nil {
		t.Fatal(err)
	}

	pending, _ := s.PendingProposals(ctx)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after reject, got %d", len(pending))
	}
}

func TestSQLiteStore_UpdateStatus(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	p := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		DedupeKey:     "test:dedupe",
		Rationale:     "test",
	}
	s.Submit(ctx, p)

	if err := s.UpdateStatus(ctx, p.ID, StatusDedupeHit); err != nil {
		t.Fatal(err)
	}

	pending, _ := s.PendingProposals(ctx)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after dedupe_hit, got %d", len(pending))
	}
}

func TestSQLiteStore_NotFound(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	if err := s.Approve(ctx, 9999); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := s.Reject(ctx, 9999, "no"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := s.UpdateStatus(ctx, 9999, "bogus"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_EvidenceRoundTrip(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	p := &MissionProposal{
		ObjectiveID:   "obj-1",
		ParentMission: 10,
		DedupeKey:     "test:evidence",
		Rationale:     "test",
		Evidence:      json.RawMessage(`{"key":"value","nested":{"a":1}}`),
	}
	s.Submit(ctx, p)

	pending, _ := s.PendingProposals(ctx)
	if len(pending) != 1 {
		t.Fatal("expected 1 pending")
	}
	if string(pending[0].Evidence) != `{"key":"value","nested":{"a":1}}` {
		t.Errorf("Evidence = %q", string(pending[0].Evidence))
	}
}
