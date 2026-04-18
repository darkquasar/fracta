package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/strategy"

	_ "modernc.org/sqlite"
)

func newTestStagingStore(t *testing.T) *StagingRunStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// In-memory SQLite databases are per-connection. Limit to 1 connection
	// to avoid schema being invisible on other connections in the pool.
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA foreign_keys=ON")

	store, err := NewStagingRunStore(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return store
}

func TestStagingRunStore_CreateAndGet(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	now := time.Now()
	run := &strategy.StagingRun{
		ID:                "run-001",
		StrategyName:      "dns_anomaly_hunt",
		Params:            map[string]any{"time_start": "2026-01-01", "time_end": "2026-01-02"},
		ParamsFingerprint: "abc123",
		Status:            strategy.RunStatusCreated,
		Tables: map[string]*strategy.TableState{
			"alerts": {
				Name:      "alerts",
				FetchMode: "fracta_mcp_gateway",
				Required:  true,
				Status:    strategy.TableStatusPending,
			},
			"iocs": {
				Name:      "iocs",
				FetchMode: "mcp",
				Required:  false,
				Status:    strategy.TableStatusAwaitingAgent,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, "run-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.StrategyName != "dns_anomaly_hunt" {
		t.Errorf("StrategyName = %q, want dns_anomaly_hunt", got.StrategyName)
	}
	if got.Status != strategy.RunStatusCreated {
		t.Errorf("Status = %q, want created", got.Status)
	}
	if len(got.Tables) != 2 {
		t.Errorf("Tables count = %d, want 2", len(got.Tables))
	}
	if got.Tables["alerts"].FetchMode != "fracta_mcp_gateway" {
		t.Errorf("alerts FetchMode = %q, want fracta_mcp_gateway", got.Tables["alerts"].FetchMode)
	}
	if !got.Tables["alerts"].Required {
		t.Error("alerts.Required = false, want true")
	}
	if got.Tables["iocs"].Status != strategy.TableStatusAwaitingAgent {
		t.Errorf("iocs Status = %q, want awaiting_agent", got.Tables["iocs"].Status)
	}
}

func TestStagingRunStore_GetNotFound(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent ID")
	}
}

func TestStagingRunStore_UpdateStatus(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	run := &strategy.StagingRun{
		ID:           "run-002",
		StrategyName: "test",
		Status:       strategy.RunStatusCreated,
		Tables:       map[string]*strategy.TableState{},
	}
	store.Create(ctx, run)

	if err := store.UpdateStatus(ctx, "run-002", strategy.RunStatusStaging); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, _ := store.Get(ctx, "run-002")
	if got.Status != strategy.RunStatusStaging {
		t.Errorf("Status = %q, want staging", got.Status)
	}
}

func TestStagingRunStore_UpdateTable(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	run := &strategy.StagingRun{
		ID:           "run-003",
		StrategyName: "test",
		Status:       strategy.RunStatusStaging,
		Tables: map[string]*strategy.TableState{
			"events": {
				Name:      "events",
				FetchMode: "fracta_mcp_gateway",
				Required:  true,
				Status:    strategy.TableStatusPending,
			},
		},
	}
	store.Create(ctx, run)

	now := time.Now()
	updatedState := &strategy.TableState{
		Name:           "events",
		FetchMode:      "fracta_mcp_gateway",
		Required:       true,
		Status:         strategy.TableStatusFetching,
		RowCount:       5000,
		PagesCompleted: 5,
		StartedAt:      &now,
		FetchPlan:      json.RawMessage(`{"server":"elastic","tool":"search"}`),
	}

	if err := store.UpdateTable(ctx, "run-003", "events", updatedState); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	got, _ := store.Get(ctx, "run-003")
	ts := got.Tables["events"]
	if ts.Status != strategy.TableStatusFetching {
		t.Errorf("Status = %q, want fetching", ts.Status)
	}
	if ts.RowCount != 5000 {
		t.Errorf("RowCount = %d, want 5000", ts.RowCount)
	}
	if ts.PagesCompleted != 5 {
		t.Errorf("PagesCompleted = %d, want 5", ts.PagesCompleted)
	}
	if ts.FetchPlan == nil {
		t.Error("FetchPlan is nil")
	}
}

func TestStagingRunStore_CASExecute(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	run := &strategy.StagingRun{
		ID:           "run-cas",
		StrategyName: "test",
		Status:       strategy.RunStatusStaged,
		Tables:       map[string]*strategy.TableState{},
	}
	store.Create(ctx, run)

	// First caller wins.
	won, err := store.CASExecute(ctx, "run-cas")
	if err != nil {
		t.Fatalf("CASExecute: %v", err)
	}
	if !won {
		t.Error("first CASExecute should win")
	}

	// Second caller loses.
	won2, err := store.CASExecute(ctx, "run-cas")
	if err != nil {
		t.Fatalf("CASExecute 2: %v", err)
	}
	if won2 {
		t.Error("second CASExecute should lose")
	}

	// Verify status changed.
	got, _ := store.Get(ctx, "run-cas")
	if got.Status != strategy.RunStatusExecuting {
		t.Errorf("Status = %q, want executing", got.Status)
	}
	if got.ExecutionClaimedAt == nil {
		t.Error("ExecutionClaimedAt should be set")
	}
}

func TestStagingRunStore_SetResult(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	run := &strategy.StagingRun{
		ID:           "run-result",
		StrategyName: "test",
		Status:       strategy.RunStatusExecuting,
		Tables:       map[string]*strategy.TableState{},
	}
	store.Create(ctx, run)

	result := json.RawMessage(`{"findings": 3}`)
	trace := json.RawMessage(`{"steps": []}`)

	if err := store.SetResult(ctx, "run-result", strategy.RunStatusComplete, result, trace); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	got, _ := store.Get(ctx, "run-result")
	if got.Status != strategy.RunStatusComplete {
		t.Errorf("Status = %q, want complete", got.Status)
	}
	if string(got.Result) != `{"findings": 3}` {
		t.Errorf("Result = %s", got.Result)
	}
	if string(got.Trace) != `{"steps": []}` {
		t.Errorf("Trace = %s", got.Trace)
	}
}

func TestStagingRunStore_ListActive(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	// Create runs in various states.
	for _, r := range []*strategy.StagingRun{
		{ID: "active-1", StrategyName: "test", Status: strategy.RunStatusStaging, Tables: map[string]*strategy.TableState{}},
		{ID: "active-2", StrategyName: "test", Status: strategy.RunStatusPending, Tables: map[string]*strategy.TableState{}},
		{ID: "done-1", StrategyName: "test", Status: strategy.RunStatusComplete, Tables: map[string]*strategy.TableState{}},
		{ID: "done-2", StrategyName: "test", Status: strategy.RunStatusFailed, Tables: map[string]*strategy.TableState{}},
	} {
		store.Create(ctx, r)
	}

	active, err := store.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("ListActive returned %d runs, want 2", len(active))
	}

	ids := map[string]bool{}
	for _, r := range active {
		ids[r.ID] = true
	}
	if !ids["active-1"] || !ids["active-2"] {
		t.Errorf("expected active-1 and active-2, got %v", ids)
	}
}

func TestStagingRunStore_Reap(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	// Insert runs with old timestamps by directly updating.
	oldTime := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)

	for _, r := range []*strategy.StagingRun{
		{ID: "old-complete", StrategyName: "test", Status: strategy.RunStatusComplete, Tables: map[string]*strategy.TableState{}},
		{ID: "old-failed", StrategyName: "test", Status: strategy.RunStatusFailed, Tables: map[string]*strategy.TableState{}},
		{ID: "old-staging", StrategyName: "test", Status: strategy.RunStatusStaging, Tables: map[string]*strategy.TableState{}},
		{ID: "fresh-staging", StrategyName: "test", Status: strategy.RunStatusStaging, Tables: map[string]*strategy.TableState{}},
	} {
		store.Create(ctx, r)
	}

	// Manually set updated_at for the "old" ones.
	store.db.Exec("UPDATE staging_runs SET updated_at = ? WHERE id LIKE 'old-%'", oldTime)

	// Reap with 1h terminal TTL and 90min stale TTL.
	reaped, err := store.Reap(ctx, 1*time.Hour, 90*time.Minute)
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}

	// old-complete, old-failed (terminal >1h), old-staging (stale >90min) should be reaped.
	if reaped != 3 {
		t.Errorf("reaped = %d, want 3", reaped)
	}

	// fresh-staging should survive.
	got, _ := store.Get(ctx, "fresh-staging")
	if got == nil {
		t.Error("fresh-staging was reaped unexpectedly")
	}
}

func TestStagingRunStore_StartupRecovery(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	// Simulate a run that was mid-staging when the pod died.
	now := time.Now()
	run := &strategy.StagingRun{
		ID:           "recovering",
		StrategyName: "hunt",
		Status:       strategy.RunStatusStaging,
		Tables: map[string]*strategy.TableState{
			"events": {
				Name:           "events",
				FetchMode:      "fracta_mcp_gateway",
				Required:       true,
				Status:         strategy.TableStatusFetching,
				RowCount:       25000,
				PagesCompleted: 25,
				LastOffset:     25000,
				StartedAt:      &now,
				FetchPlan:      json.RawMessage(`{"server":"elastic","tool":"search","args":{}}`),
			},
		},
	}
	store.Create(ctx, run)

	// ListActive should find it.
	active, err := store.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active run, got %d", len(active))
	}
	r := active[0]
	if r.ID != "recovering" {
		t.Errorf("ID = %q", r.ID)
	}
	ts := r.Tables["events"]
	if ts == nil {
		t.Fatal("events table not loaded")
	}
	if ts.LastOffset != 25000 {
		t.Errorf("LastOffset = %d, want 25000", ts.LastOffset)
	}
	if ts.PagesCompleted != 25 {
		t.Errorf("PagesCompleted = %d, want 25", ts.PagesCompleted)
	}
	if ts.FetchPlan == nil {
		t.Error("FetchPlan is nil after recovery load")
	}
}

// TestMixedModeBackgroundCompletionTransitionsToPending verifies that when background
// staging finishes for fracta_mcp_gateway tables but mcp tables still await agent action,
// the run transitions from staging → pending (not stuck in staging).
func TestMixedModeBackgroundCompletionTransitionsToPending(t *testing.T) {
	store := newTestStagingStore(t)
	ctx := context.Background()

	// Create a mixed-mode run: one background table (fetching) + one agent table (awaiting_agent).
	run := &strategy.StagingRun{
		ID:           "mixed-mode-1",
		StrategyName: "mixed_hunt",
		Status:       strategy.RunStatusStaging,
		Tables: map[string]*strategy.TableState{
			"auto_table": {
				Name:      "auto_table",
				FetchMode: "fracta_mcp_gateway",
				Required:  true,
				Status:    strategy.TableStatusFetching,
			},
			"agent_table": {
				Name:      "agent_table",
				FetchMode: "mcp",
				Required:  true,
				Status:    strategy.TableStatusAwaitingAgent,
			},
		},
	}
	if err := store.Create(ctx, run); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate background staging completion for auto_table.
	if err := store.UpdateTable(ctx, run.ID, "auto_table", &strategy.TableState{
		Name:      "auto_table",
		FetchMode: "fracta_mcp_gateway",
		Required:  true,
		Status:    strategy.TableStatusStaged,
		RowCount:  50000,
	}); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	// Reload and derive: should be pending (not staging, not staged).
	got, err := store.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	nextStatus := strategy.DeriveRunStatus(got)
	if nextStatus != strategy.RunStatusPending {
		t.Errorf("DeriveRunStatus = %q, want %q (mixed-mode: auto done, agent waiting)", nextStatus, strategy.RunStatusPending)
	}

	// Apply the transition.
	if err := store.UpdateStatus(ctx, run.ID, nextStatus); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// Verify final state.
	final, _ := store.Get(ctx, run.ID)
	if final.Status != strategy.RunStatusPending {
		t.Errorf("run status = %q, want pending", final.Status)
	}

	// Now simulate agent staging the manual table.
	if err := store.UpdateTable(ctx, run.ID, "agent_table", &strategy.TableState{
		Name:      "agent_table",
		FetchMode: "mcp",
		Required:  true,
		Status:    strategy.TableStatusStaged,
		RowCount:  100,
	}); err != nil {
		t.Fatalf("UpdateTable agent: %v", err)
	}

	// Reload and derive again: now all staged → should be staged.
	got2, _ := store.Get(ctx, run.ID)
	nextStatus2 := strategy.DeriveRunStatus(got2)
	if nextStatus2 != strategy.RunStatusStaged {
		t.Errorf("DeriveRunStatus after agent stage = %q, want %q", nextStatus2, strategy.RunStatusStaged)
	}
}
