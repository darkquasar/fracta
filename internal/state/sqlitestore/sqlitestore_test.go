package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
)

func setupSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLiteStore_ImplementsStore(t *testing.T) {
	s := setupSQLiteStore(t)
	var _ state.Store = s
}

func TestSQLiteStore_LoadEmpty(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()
	st, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(st.Agents))
	}
}

func TestSQLiteStore_WithLock(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	err := s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:          "research",
			RuntimeType:      "claude",
			ResumeToken:   "sess-123",
			WorkspacePath: "/tmp/wt",
			BranchName:    "feature/research",
			BaseBranch:    "main",
			Status:        model.StatusRunning,
			LastOutput:    "working on it",
			StartTime:     now,
			Mode:          "heavy",
			CurrentIntent: "analyzing logs",
		})
		st.Chessmaster = model.ChessmasterStatus{
			Status:     "active",
			LastAction: "spawned agent",
			UpdatedAt:  now,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	st, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(st.Agents))
	}
	a := st.Agents[0]
	if a.Task != "research" {
		t.Errorf("Task = %q, want %q", a.Task, "research")
	}
	if a.RuntimeType != "claude" {
		t.Errorf("RuntimeType = %q, want %q", a.RuntimeType, "claude")
	}
	if a.ResumeToken != "sess-123" {
		t.Errorf("ResumeToken = %q, want %q", a.ResumeToken, "sess-123")
	}
	if a.WorkspacePath != "/tmp/wt" {
		t.Errorf("WorkspacePath = %q, want %q", a.WorkspacePath, "/tmp/wt")
	}
	if a.BranchName != "feature/research" {
		t.Errorf("BranchName = %q, want %q", a.BranchName, "feature/research")
	}
	if a.Status != model.StatusRunning {
		t.Errorf("Status = %q, want %q", a.Status, model.StatusRunning)
	}
	if a.LastOutput != "working on it" {
		t.Errorf("LastOutput = %q, want %q", a.LastOutput, "working on it")
	}
	if !a.StartTime.Equal(now) {
		t.Errorf("StartTime = %v, want %v", a.StartTime, now)
	}
	if a.Mode != "heavy" {
		t.Errorf("Mode = %q, want %q", a.Mode, "heavy")
	}
	if a.CurrentIntent != "analyzing logs" {
		t.Errorf("CurrentIntent = %q, want %q", a.CurrentIntent, "analyzing logs")
	}
	if st.Chessmaster.Status != "active" {
		t.Errorf("Chessmaster.Status = %q, want %q", st.Chessmaster.Status, "active")
	}
}

func TestSQLiteStore_FindAgent(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()
	_ = s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents,
			model.AgentEntry{Task: "hunt"},
			model.AgentEntry{Task: "triage"},
		)
		return nil
	})

	agent, err := s.FindAgent(ctx, "hunt")
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("expected agent, got nil")
	}
	if agent.Task != "hunt" {
		t.Fatalf("expected Task 'hunt', got %q", agent.Task)
	}

	missing, err := s.FindAgent(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatal("expected nil for nonexistent task")
	}
}

func TestSQLiteStore_WithLock_Rollback(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	_ = s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "keep"})
		return nil
	})

	err := s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "discard"})
		return &testError{}
	})
	if err == nil {
		t.Fatal("expected error from WithLock")
	}

	st, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Agents) != 1 {
		t.Fatalf("expected 1 agent after rollback, got %d", len(st.Agents))
	}
	if st.Agents[0].Task != "keep" {
		t.Fatalf("expected task 'keep', got %q", st.Agents[0].Task)
	}
}

func TestSQLiteStore_UpdateChessmaster(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	if err := s.UpdateChessmaster(ctx, "active", "spawned agent", now); err != nil {
		t.Fatal(err)
	}

	st, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Chessmaster.Status != "active" {
		t.Errorf("Status = %q, want %q", st.Chessmaster.Status, "active")
	}
	if st.Chessmaster.LastAction != "spawned agent" {
		t.Errorf("LastAction = %q, want %q", st.Chessmaster.LastAction, "spawned agent")
	}
}

func TestSQLiteStore_Mailbox(t *testing.T) {
	s := setupSQLiteStore(t)
	mb := s.Mailbox()
	if mb == nil {
		t.Fatal("Mailbox() returned nil")
	}
}

func TestSQLiteStore_ClaimAgent(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	// Insert a Queued agent.
	err := s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:      "queued-agent",
			Status:    model.StatusQueued,
			Mode:      "queued",
			MissionID: 42,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Claim it.
	if err := s.ClaimAgent(ctx, "queued-agent"); err != nil {
		t.Fatal(err)
	}

	// Verify status changed to Running and StartTime is set.
	agent, err := s.FindAgent(ctx, "queued-agent")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != model.StatusRunning {
		t.Errorf("expected status %q, got %q", model.StatusRunning, agent.Status)
	}
	if agent.StartTime.IsZero() {
		t.Error("expected StartTime to be set")
	}
}

func TestSQLiteStore_ClaimAgent_NotQueued(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	// Insert a Running agent.
	_ = s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:   "running-agent",
			Status: model.StatusRunning,
		})
		return nil
	})

	// Claiming should fail.
	err := s.ClaimAgent(ctx, "running-agent")
	if err == nil {
		t.Fatal("expected error claiming non-queued agent")
	}
}

func TestSQLiteStore_ClaimAgent_NotFound(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	err := s.ClaimAgent(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error claiming nonexistent agent")
	}
}

func TestSQLiteStore_MissionID_RoundTrip(t *testing.T) {
	s := setupSQLiteStore(t)
	ctx := context.Background()

	err := s.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:      "mission-agent",
			Status:    model.StatusQueued,
			Mode:      "queued",
			MissionID: 12345,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	agent, err := s.FindAgent(ctx, "mission-agent")
	if err != nil {
		t.Fatal(err)
	}
	if agent.MissionID != 12345 {
		t.Errorf("expected MissionID=12345, got %d", agent.MissionID)
	}
}

type testError struct{}

func (e *testError) Error() string { return "test error" }
