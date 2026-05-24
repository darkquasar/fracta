//go:build postgres

package queue_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/state/pgstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupPG(t *testing.T) (*pgstore.PostgresStore, *queue.PostgresQueue) {
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

	q := queue.NewPostgresQueue(store.Pool(),
		queue.WithLeaseTimeout(5*time.Minute),
		queue.WithPollInterval(100*time.Millisecond),
		queue.WithWorkerID("test-worker"),
	)
	t.Cleanup(func() { q.Close() })

	return store, q
}

func pgPayload(t *testing.T) json.RawMessage {
	t.Helper()
	p := queue.MissionPayload{Task: "test-task", RuntimeType: "claude"}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	return b
}

func TestPostgresQueue_EnqueueDequeue(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "agent-1", Payload: payload, Priority: 0}
	agent := &model.AgentEntry{
		Task:        "agent-1",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}

	require.NoError(t, q.Enqueue(ctx, m, agent))
	assert.NotZero(t, m.ID)

	// Dequeue should return the mission.
	got, err := q.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, m.ID, got.ID)
	assert.Equal(t, "agent-1", got.AgentTask)
	assert.Equal(t, "claimed", got.Status)
}

func TestPostgresQueue_TransactionalEnqueue(t *testing.T) {
	store, q := setupPG(t)
	ctx := context.Background()

	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "tx-agent", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "tx-agent",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
		MissionID:   0, // will be set by Enqueue
	}

	require.NoError(t, q.Enqueue(ctx, m, agent))

	// Agent should be persisted in the store.
	a, err := store.FindAgent(ctx, "tx-agent")
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, model.StatusQueued, a.Status)
	assert.Equal(t, m.ID, a.MissionID)
}

func TestPostgresQueue_ConcurrentClaim(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	// Enqueue one mission.
	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "concurrent-1", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "concurrent-1",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	require.NoError(t, q.Enqueue(ctx, m, agent))

	// Two workers race to claim.
	var wg sync.WaitGroup
	claimed := make(chan *queue.Mission, 2)
	errors := make(chan error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			got, err := q.Dequeue(claimCtx)
			if err != nil {
				errors <- err
				return
			}
			claimed <- got
		}()
	}

	// Wait for results.
	wg.Wait()
	close(claimed)
	close(errors)

	// Exactly one should succeed.
	var claimedMissions []*queue.Mission
	for m := range claimed {
		claimedMissions = append(claimedMissions, m)
	}
	assert.Len(t, claimedMissions, 1, "exactly one worker should claim the mission")
}

func TestPostgresQueue_CancelPending(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "cancel-pending", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "cancel-pending",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	require.NoError(t, q.Enqueue(ctx, m, agent))

	// Cancel the pending mission — should delete it.
	require.NoError(t, q.Cancel(ctx, m.ID))

	// Status should now be not found.
	_, err := q.Status(ctx, m.ID)
	assert.ErrorIs(t, err, queue.ErrNotFound)
}

func TestPostgresQueue_CancelClaimed(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "cancel-claimed", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "cancel-claimed",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	require.NoError(t, q.Enqueue(ctx, m, agent))

	// Claim the mission.
	got, err := q.Dequeue(ctx)
	require.NoError(t, err)

	// Cancel the claimed mission — should mark as cancelled.
	require.NoError(t, q.Cancel(ctx, got.ID))

	status, err := q.Status(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", status)
}

func TestPostgresQueue_AckAndFail(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	// Enqueue two missions.
	payload := pgPayload(t)
	m1 := &queue.Mission{AgentTask: "ack-1", Payload: payload}
	a1 := &model.AgentEntry{Task: "ack-1", RuntimeType: "claude", Status: model.StatusQueued, Mode: "queued"}
	require.NoError(t, q.Enqueue(ctx, m1, a1))

	m2 := &queue.Mission{AgentTask: "fail-1", Payload: payload}
	a2 := &model.AgentEntry{Task: "fail-1", RuntimeType: "claude", Status: model.StatusQueued, Mode: "queued"}
	require.NoError(t, q.Enqueue(ctx, m2, a2))

	// Claim and ack first.
	got1, err := q.Dequeue(ctx)
	require.NoError(t, err)
	require.NoError(t, q.Ack(ctx, got1.ID))

	s, err := q.Status(ctx, got1.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", s)

	// Claim and fail second.
	got2, err := q.Dequeue(ctx)
	require.NoError(t, err)
	require.NoError(t, q.Fail(ctx, got2.ID, "something broke"))

	s, err = q.Status(ctx, got2.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", s)
}

func TestPostgresQueue_Len(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	l, err := q.Len(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, l)

	payload := pgPayload(t)
	for i := 0; i < 3; i++ {
		m := &queue.Mission{AgentTask: "len-" + string(rune('a'+i)), Payload: payload}
		a := &model.AgentEntry{Task: m.AgentTask, RuntimeType: "claude", Status: model.StatusQueued, Mode: "queued"}
		require.NoError(t, q.Enqueue(ctx, m, a))
	}

	l, err = q.Len(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, l)
}

func TestPostgresQueue_StatusNotFound(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	_, err := q.Status(ctx, 99999)
	assert.ErrorIs(t, err, queue.ErrNotFound)
}

func TestPostgresQueue_ListenNotify(t *testing.T) {
	_, q := setupPG(t)
	ctx := context.Background()

	// Give LISTEN connection time to establish.
	time.Sleep(500 * time.Millisecond)

	// Start dequeue in background.
	done := make(chan *queue.Mission, 1)
	go func() {
		m, err := q.Dequeue(ctx)
		if err == nil {
			done <- m
		}
	}()

	// Enqueue after a short delay — NOTIFY should wake up the Dequeue.
	time.Sleep(200 * time.Millisecond)
	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "notify-test", Payload: payload}
	a := &model.AgentEntry{Task: "notify-test", RuntimeType: "claude", Status: model.StatusQueued, Mode: "queued"}
	require.NoError(t, q.Enqueue(ctx, m, a))

	select {
	case got := <-done:
		assert.Equal(t, "notify-test", got.AgentTask)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for LISTEN/NOTIFY wake-up")
	}
}

func TestPostgresQueue_LeaseReclaim(t *testing.T) {
	store, _ := setupPG(t)
	ctx := context.Background()

	// Create a queue with a very short lease timeout for testing.
	q := queue.NewPostgresQueue(store.Pool(),
		queue.WithLeaseTimeout(1*time.Second),
		queue.WithPollInterval(100*time.Millisecond),
		queue.WithWorkerID("reclaim-test"),
	)
	t.Cleanup(func() { q.Close() })

	// Enqueue and claim.
	payload := pgPayload(t)
	m := &queue.Mission{AgentTask: "reclaim-agent", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "reclaim-agent",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	require.NoError(t, q.Enqueue(ctx, m, agent))

	// Claim the mission.
	got, err := q.Dequeue(ctx)
	require.NoError(t, err)

	// Simulate worker claiming the agent (Queued -> Running).
	require.NoError(t, store.ClaimAgent(ctx, "reclaim-agent"))

	// Verify agent is Running.
	a, err := store.FindAgent(ctx, "reclaim-agent")
	require.NoError(t, err)
	assert.Equal(t, model.StatusRunning, a.Status)

	// Backdate the claimed_at to trigger lease expiry.
	_, err = store.Pool().Exec(ctx,
		`UPDATE missions SET claimed_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`,
		got.ID)
	require.NoError(t, err)

	// Wait for the reaper to reclaim (lease_timeout/2 = 500ms, min 30s but we
	// need to trigger it manually). The reaper runs at leaseTimeout/2 intervals,
	// but min is 30s. For testing, let's just wait and check — the reaper with
	// 1s lease should run at 500ms but clamped to 30s. This is too slow for unit tests.
	// Instead, verify the reclaim logic by checking state after waiting.
	// Since the reaper interval is clamped to 30s minimum, we'll check after a delay
	// to see if the mission gets reclaimed.

	// Alternative: just verify by re-dequeuing after waiting for the reaper.
	// The reaper at 1s lease / 2 = 500ms but min 30s. That's too slow.
	// Let's just check that after enough time, a second dequeue can claim it.

	// For this test, let's wait up to 35s for the reaper (clamped to 30s min).
	deadline := time.After(35 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for lease reclaim")
		default:
		}

		status, err := q.Status(ctx, got.ID)
		require.NoError(t, err)
		if status == "pending" {
			// Mission was reclaimed! Verify agent is back to Queued.
			a, err := store.FindAgent(ctx, "reclaim-agent")
			require.NoError(t, err)
			assert.Equal(t, model.StatusQueued, a.Status)
			return
		}
		time.Sleep(1 * time.Second)
	}
}
