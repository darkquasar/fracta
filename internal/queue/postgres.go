package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresQueue implements MissionQueue backed by PostgreSQL.
// Uses SKIP LOCKED for concurrent claim, LISTEN/NOTIFY for wake-up,
// and a self-contained lease reaper goroutine.
type PostgresQueue struct {
	pool         *pgxpool.Pool
	workerID     string
	leaseTimeout time.Duration
	pollInterval time.Duration
	logger       *slog.Logger

	// LISTEN/NOTIFY
	listenConn *pgxListenConn
	listenMu   sync.Mutex
	notify     chan struct{} // signalled on LISTEN wake-up

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// QueueOption configures a PostgresQueue.
type QueueOption func(*PostgresQueue)

// WithLeaseTimeout sets the duration after which a claimed mission is
// considered stuck and eligible for reclaim.
func WithLeaseTimeout(d time.Duration) QueueOption {
	return func(q *PostgresQueue) { q.leaseTimeout = d }
}

// WithPollInterval sets the fallback poll interval for Dequeue when
// LISTEN/NOTIFY is unavailable.
func WithPollInterval(d time.Duration) QueueOption {
	return func(q *PostgresQueue) { q.pollInterval = d }
}

// WithWorkerID sets the worker identity written to claimed_by.
func WithWorkerID(id string) QueueOption {
	return func(q *PostgresQueue) { q.workerID = id }
}

// NewPostgresQueue creates a PostgresQueue sharing the given pool.
func NewPostgresQueue(pool *pgxpool.Pool, opts ...QueueOption) *PostgresQueue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &PostgresQueue{
		pool:         pool,
		workerID:     "default",
		leaseTimeout: 30 * time.Minute,
		pollInterval: 5 * time.Second,
		logger:       fractalog.Component("pgqueue"),
		notify:       make(chan struct{}, 1),
		ctx:          ctx,
		cancel:       cancel,
	}
	for _, opt := range opts {
		opt(q)
	}

	q.wg.Add(2)
	go q.listenLoop()
	go q.reaperLoop()

	return q
}

// Enqueue inserts a mission and agent record in one transaction.
func (q *PostgresQueue) Enqueue(ctx context.Context, m *Mission, agent *model.AgentEntry) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgqueue enqueue: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Insert mission (includes DAG columns — defaults are backward-compatible).
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO missions (status, payload, agent_task, priority,
			objective_id, parent_id, depth, dedupe_key, proposed_by)
		 VALUES ('pending', $1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		m.Payload, m.AgentTask, m.Priority,
		nilIfEmpty(m.ObjectiveID), m.ParentID, m.Depth, m.DedupeKey, m.ProposedBy).Scan(&id)
	if err != nil {
		return fmt.Errorf("pgqueue enqueue: insert mission: %w", err)
	}
	m.ID = id
	m.Status = StatusPending
	m.CreatedAt = time.Now()

	// Set agent's MissionID.
	agent.MissionID = id

	// Insert agent.
	var startTime *time.Time
	if !agent.StartTime.IsZero() {
		startTime = &agent.StartTime
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO agents (task, host_type, resume_token, workspace_path, branch_name,
		 base_branch, status, last_output, start_time, mode, current_intent, mission_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		agent.Task, agent.RuntimeType, agent.ResumeToken, agent.WorkspacePath, agent.BranchName,
		agent.BaseBranch, string(agent.Status), agent.LastOutput, startTime, agent.Mode,
		agent.CurrentIntent, agent.MissionID)
	if err != nil {
		return fmt.Errorf("pgqueue enqueue: insert agent: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgqueue enqueue: commit: %w", err)
	}

	// Best-effort NOTIFY after commit.
	q.pool.Exec(ctx, "SELECT pg_notify('missions_ready', '')")

	return nil
}

// Dequeue blocks until a mission is available, claims it atomically, and returns it.
func (q *PostgresQueue) Dequeue(ctx context.Context) (*Mission, error) {
	for {
		m, err := q.tryClaim(ctx)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return m, nil
		}

		// No mission available — wait for notification or poll timeout.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.notify:
			// LISTEN/NOTIFY woke us up — retry claim.
		case <-time.After(q.pollInterval):
			// Poll fallback.
		}
	}
}

// tryClaim attempts to claim one pending mission atomically using SKIP LOCKED.
func (q *PostgresQueue) tryClaim(ctx context.Context) (*Mission, error) {
	m := &Mission{}
	err := q.pool.QueryRow(ctx, `
		UPDATE missions SET status='claimed', claimed_by=$1, claimed_at=NOW()
		WHERE id = (
			SELECT id FROM missions WHERE status='pending'
			ORDER BY priority DESC, created_at
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING id, payload, agent_task, priority, created_at`,
		q.workerID).Scan(&m.ID, &m.Payload, &m.AgentTask, &m.Priority, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pgqueue dequeue: claim: %w", err)
	}
	m.Status = StatusClaimed
	now := time.Now()
	m.ClaimedAt = &now
	m.ClaimedBy = q.workerID
	return m, nil
}

// Ack marks a claimed mission as completed.
func (q *PostgresQueue) Ack(ctx context.Context, missionID int64) error {
	tag, err := q.pool.Exec(ctx,
		`UPDATE missions SET status='completed', completed_at=NOW() WHERE id=$1 AND status='claimed'`,
		missionID)
	if err != nil {
		return fmt.Errorf("pgqueue ack: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Fail marks a claimed mission as failed with a reason.
func (q *PostgresQueue) Fail(ctx context.Context, missionID int64, reason string) error {
	tag, err := q.pool.Exec(ctx,
		`UPDATE missions SET status='failed', error=$1, completed_at=NOW() WHERE id=$2 AND status='claimed'`,
		reason, missionID)
	if err != nil {
		return fmt.Errorf("pgqueue fail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Len returns the number of pending missions.
func (q *PostgresQueue) Len(ctx context.Context) (int, error) {
	var count int
	err := q.pool.QueryRow(ctx, `SELECT COUNT(*) FROM missions WHERE status='pending'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("pgqueue len: %w", err)
	}
	return count, nil
}

// Status returns the current status of a mission.
func (q *PostgresQueue) Status(ctx context.Context, missionID int64) (string, error) {
	var status string
	err := q.pool.QueryRow(ctx, `SELECT status FROM missions WHERE id=$1`, missionID).Scan(&status)
	if err == pgx.ErrNoRows {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("pgqueue status: %w", err)
	}
	return status, nil
}

// Cancel removes a pending mission or marks a claimed mission as cancelled.
func (q *PostgresQueue) Cancel(ctx context.Context, missionID int64) error {
	// Fast path: delete pending mission.
	tag, err := q.pool.Exec(ctx, `DELETE FROM missions WHERE id=$1 AND status='pending'`, missionID)
	if err != nil {
		return fmt.Errorf("pgqueue cancel: delete pending: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	// Slow path: mark claimed mission as cancelled.
	tag, err = q.pool.Exec(ctx, `UPDATE missions SET status='cancelled' WHERE id=$1 AND status='claimed'`, missionID)
	if err != nil {
		return fmt.Errorf("pgqueue cancel: update claimed: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	return ErrNotFound
}

// Close releases queue resources.
func (q *PostgresQueue) Close() error {
	q.cancel()
	q.wg.Wait()
	q.listenMu.Lock()
	if q.listenConn != nil {
		q.listenConn.conn.Close(context.Background())
		q.listenConn = nil
	}
	q.listenMu.Unlock()
	return nil
}

// pgxListenConn wraps a *pgx.Conn hijacked from the pool for dedicated LISTEN use.
type pgxListenConn struct {
	conn *pgx.Conn
}

func (lc *pgxListenConn) Close(ctx context.Context) error {
	return lc.conn.Close(ctx)
}

// listenLoop maintains a LISTEN connection and signals the notify channel.
func (q *PostgresQueue) listenLoop() {
	defer q.wg.Done()

	for {
		if q.ctx.Err() != nil {
			return
		}

		conn, err := q.pool.Acquire(q.ctx)
		if err != nil {
			if q.ctx.Err() != nil {
				return
			}
			q.logger.Warn("listen: failed to acquire connection", "error", err)
			select {
			case <-q.ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// Hijack the pooled connection for dedicated LISTEN use.
		rawConn := conn.Hijack()
		lc := &pgxListenConn{conn: rawConn}

		q.listenMu.Lock()
		q.listenConn = lc
		q.listenMu.Unlock()

		_, err = rawConn.Exec(q.ctx, "LISTEN missions_ready")
		if err != nil {
			q.logger.Warn("listen: LISTEN failed", "error", err)
			lc.Close(context.Background())
			q.listenMu.Lock()
			q.listenConn = nil
			q.listenMu.Unlock()
			continue
		}

		q.logger.Info("LISTEN connection established")

		// Wait for notifications until error or context cancellation.
		for {
			_, err := rawConn.WaitForNotification(q.ctx)
			if err != nil {
				if q.ctx.Err() != nil {
					lc.Close(context.Background())
					return
				}
				q.logger.Warn("listen: notification error, reconnecting", "error", err)
				lc.Close(context.Background())
				q.listenMu.Lock()
				q.listenConn = nil
				q.listenMu.Unlock()
				break
			}
			// Non-blocking signal to wake up Dequeue.
			select {
			case q.notify <- struct{}{}:
			default:
			}
		}
	}
}

// reaperLoop periodically reclaims stuck missions.
func (q *PostgresQueue) reaperLoop() {
	defer q.wg.Done()

	interval := q.leaseTimeout / 2
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-q.ctx.Done():
			return
		case <-ticker.C:
			q.reclaimStuck()
		}
	}
}

// reclaimStuck resets claimed missions past the lease timeout back to pending,
// and atomically resets the paired agent to Queued.
func (q *PostgresQueue) reclaimStuck() {
	ctx, cancel := context.WithTimeout(q.ctx, 30*time.Second)
	defer cancel()

	tx, err := q.pool.Begin(ctx)
	if err != nil {
		q.logger.Error("reclaim: begin tx", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	// Reset stuck missions and collect their agent tasks.
	rows, err := tx.Query(ctx, `
		UPDATE missions SET status='pending', claimed_by=NULL, claimed_at=NULL
		WHERE status='claimed' AND claimed_at < NOW() - $1::interval
		RETURNING agent_task`,
		q.leaseTimeout.String())
	if err != nil {
		q.logger.Error("reclaim: update missions", "error", err)
		return
	}

	var tasks []string
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			q.logger.Error("reclaim: scan agent_task", "error", err)
			continue
		}
		tasks = append(tasks, task)
	}
	rows.Close()

	// Reset paired agents back to Queued so ClaimAgent works on re-dequeue.
	for _, task := range tasks {
		_, err := tx.Exec(ctx, `
			UPDATE agents SET status='Queued', start_time=NULL
			WHERE task=$1 AND status='Running'`, task)
		if err != nil {
			q.logger.Error("reclaim: reset agent", "error", err, "task", task)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		q.logger.Error("reclaim: commit", "error", err)
		return
	}

	if len(tasks) > 0 {
		q.pool.Exec(ctx, "SELECT pg_notify('missions_ready', '')")
		q.logger.Info("reclaimed stuck missions", "count", len(tasks))
	}
}

// nilIfEmpty returns nil for empty strings, or a pointer to s otherwise.
// Used to store TEXT columns as NULL when unset (e.g., objective_id).
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Compile-time check.
var _ MissionQueue = (*PostgresQueue)(nil)
