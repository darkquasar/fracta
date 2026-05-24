//go:build postgres

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state/pgstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupStore(t *testing.T) *pgstore.PostgresStore {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("fracta_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	store, err := pgstore.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	return store
}

func TestNew_PingAndMigrate(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	st, err := store.Load(ctx)
	require.NoError(t, err)
	assert.Empty(t, st.Agents)
}

func TestWithLock_SpawnAgent(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Microsecond)

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{
			Task:          "agent-1",
			RuntimeType:   "claude",
			ResumeToken:   "tok-abc",
			WorkspacePath: "/tmp/ws",
			BranchName:    "feature/test",
			BaseBranch:    "main",
			Status:        model.StatusRunning,
			LastOutput:    "hello",
			StartTime:     now,
			Mode:          "batch",
			CurrentIntent: "testing",
		})
		return nil
	})
	require.NoError(t, err)

	st, err := store.Load(ctx)
	require.NoError(t, err)
	require.Len(t, st.Agents, 1)

	a := st.Agents[0]
	assert.Equal(t, "agent-1", a.Task)
	assert.Equal(t, "claude", a.RuntimeType)
	assert.Equal(t, "tok-abc", a.ResumeToken)
	assert.Equal(t, "/tmp/ws", a.WorkspacePath)
	assert.Equal(t, "feature/test", a.BranchName)
	assert.Equal(t, "main", a.BaseBranch)
	assert.Equal(t, model.StatusRunning, a.Status)
	assert.Equal(t, "hello", a.LastOutput)
	assert.Equal(t, "batch", a.Mode)
	assert.Equal(t, "testing", a.CurrentIntent)
	// Postgres TIMESTAMPTZ has microsecond precision.
	assert.WithinDuration(t, now, a.StartTime, time.Microsecond)
}

func TestWithLock_DiffUpdate(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	// Spawn two agents.
	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents,
			model.AgentEntry{Task: "a1", Status: model.StatusRunning},
			model.AgentEntry{Task: "a2", Status: model.StatusRunning},
		)
		return nil
	})
	require.NoError(t, err)

	// Modify one, remove one, add one.
	err = store.WithLock(ctx, func(st *model.State) error {
		// Remove a1.
		st.Agents = st.Agents[1:]
		// Modify a2.
		st.Agents[0].Status = model.StatusCompleted
		// Add a3.
		st.Agents = append(st.Agents, model.AgentEntry{Task: "a3", Status: model.StatusPending})
		return nil
	})
	require.NoError(t, err)

	st, err := store.Load(ctx)
	require.NoError(t, err)
	require.Len(t, st.Agents, 2)

	taskMap := make(map[string]model.AgentEntry)
	for _, a := range st.Agents {
		taskMap[a.Task] = a
	}
	assert.Equal(t, model.StatusCompleted, taskMap["a2"].Status)
	assert.Equal(t, model.StatusPending, taskMap["a3"].Status)
	_, exists := taskMap["a1"]
	assert.False(t, exists, "a1 should be deleted")
}

func TestWithLock_ChessmasterUpdate(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Chessmaster.Status = "active"
		st.Chessmaster.LastAction = "spawned agent-1"
		st.Chessmaster.UpdatedAt = now
		return nil
	})
	require.NoError(t, err)

	st, err := store.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, "active", st.Chessmaster.Status)
	assert.Equal(t, "spawned agent-1", st.Chessmaster.LastAction)
	assert.WithinDuration(t, now, st.Chessmaster.UpdatedAt, time.Microsecond)
}

func TestFindAgent(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "find-me", Status: model.StatusRunning})
		return nil
	})
	require.NoError(t, err)

	a, err := store.FindAgent(ctx, "find-me")
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, "find-me", a.Task)

	a, err = store.FindAgent(ctx, "not-here")
	require.NoError(t, err)
	assert.Nil(t, a)
}

func TestRemoveAgent(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "rm-me", Status: model.StatusRunning})
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, store.RemoveAgent(ctx, "rm-me"))

	a, err := store.FindAgent(ctx, "rm-me")
	require.NoError(t, err)
	assert.Nil(t, a)

	err = store.RemoveAgent(ctx, "rm-me")
	assert.Error(t, err, "should error on missing agent")
}

func TestUpdateAgentStatus(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "status-test", Status: model.StatusRunning})
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateAgentStatus(ctx, "status-test", model.StatusCompleted, "done"))

	a, err := store.FindAgent(ctx, "status-test")
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, a.Status)
	assert.Equal(t, "done", a.LastOutput)
}

func TestUpdateAgentResult(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "result-test", Status: model.StatusRunning, ResumeToken: "old-tok"})
		return nil
	})
	require.NoError(t, err)

	// Update with new token.
	require.NoError(t, store.UpdateAgentResult(ctx, "result-test", model.StatusCompleted, "output", "new-tok"))
	a, err := store.FindAgent(ctx, "result-test")
	require.NoError(t, err)
	assert.Equal(t, "new-tok", a.ResumeToken)

	// Update without token — old token preserved.
	require.NoError(t, store.UpdateAgentResult(ctx, "result-test", model.StatusFailed, "err", ""))
	a, err = store.FindAgent(ctx, "result-test")
	require.NoError(t, err)
	assert.Equal(t, "new-tok", a.ResumeToken)
	assert.Equal(t, model.StatusFailed, a.Status)
}

func TestUpdateAgentIntent(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.WithLock(ctx, func(st *model.State) error {
		st.Agents = append(st.Agents, model.AgentEntry{Task: "intent-test", Status: model.StatusRunning})
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateAgentIntent(ctx, "intent-test", "researching"))

	a, err := store.FindAgent(ctx, "intent-test")
	require.NoError(t, err)
	assert.Equal(t, "researching", a.CurrentIntent)
}

func TestUpdateChessmaster(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	require.NoError(t, store.UpdateChessmaster(ctx, "idle", "nothing", now))

	st, err := store.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, "idle", st.Chessmaster.Status)
	assert.Equal(t, "nothing", st.Chessmaster.LastAction)
	assert.WithinDuration(t, now, st.Chessmaster.UpdatedAt, time.Microsecond)
}

func TestNotFoundErrors(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	assert.Error(t, store.UpdateAgentStatus(ctx, "nope", model.StatusRunning, ""))
	assert.Error(t, store.UpdateAgentResult(ctx, "nope", model.StatusRunning, "", ""))
	assert.Error(t, store.UpdateAgentIntent(ctx, "nope", ""))
	assert.Error(t, store.RemoveAgent(ctx, "nope"))
}

func TestMailboxViaStore(t *testing.T) {
	store := setupStore(t)
	mb := store.Mailbox()
	assert.NotNil(t, mb)
}
