package pgstore

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

const spawnLockKey int64 = 42

const agentColumns = `task, host_type, resume_token, workspace_path, branch_name, base_branch,
	status, last_output, start_time, mode, current_intent, mission_id, objective_id`

// PostgresStore implements state.Store backed by PostgreSQL via pgx v5.
type PostgresStore struct {
	pool    *pgxpool.Pool
	mailbox *PostgresMailbox
}

// New creates a PostgresStore, pings the database, and runs schema migration.
func New(ctx context.Context, dsn string, opts ...Option) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgstore: parse config: %w", err)
	}

	// Pool defaults per spec.
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	for _, opt := range opts {
		opt(config)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("pgstore: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgstore: ping: %w", err)
	}

	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgstore: migrate: %w", err)
	}

	s := &PostgresStore{pool: pool}
	s.mailbox = &PostgresMailbox{pool: pool}
	return s, nil
}

// Option configures the pgxpool before creation.
type Option func(*pgxpool.Config)

// WithMaxConns sets the maximum number of connections in the pool.
func WithMaxConns(n int32) Option {
	return func(c *pgxpool.Config) { c.MaxConns = n }
}

// WithMinConns sets the minimum number of connections in the pool.
func WithMinConns(n int32) Option {
	return func(c *pgxpool.Config) { c.MinConns = n }
}

// WithMaxConnLifetime sets the maximum lifetime of a connection before it is closed and replaced.
func WithMaxConnLifetime(d time.Duration) Option {
	return func(c *pgxpool.Config) { c.MaxConnLifetime = d }
}

// WithMaxConnIdleTime sets how long a connection can be idle before it is closed.
func WithMaxConnIdleTime(d time.Duration) Option {
	return func(c *pgxpool.Config) { c.MaxConnIdleTime = d }
}

func scanAgent(rows pgx.Row) (model.AgentEntry, error) {
	var a model.AgentEntry
	var startTime *time.Time
	err := rows.Scan(&a.Task, &a.RuntimeType, &a.ResumeToken, &a.WorkspacePath, &a.BranchName, &a.BaseBranch,
		&a.Status, &a.LastOutput, &startTime, &a.Mode, &a.CurrentIntent, &a.MissionID, &a.ObjectiveID)
	if err != nil {
		return a, err
	}
	if startTime != nil {
		a.StartTime = *startTime
	}
	return a, nil
}

// Load returns the full state snapshot.
func (s *PostgresStore) Load(ctx context.Context) (model.State, error) {
	var st model.State

	rows, err := s.pool.Query(ctx, "SELECT "+agentColumns+" FROM agents")
	if err != nil {
		return st, fmt.Errorf("pgstore: load agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return st, fmt.Errorf("pgstore: scan agent: %w", err)
		}
		st.Agents = append(st.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("pgstore: iterate agents: %w", err)
	}

	loadChessmaster(ctx, s.pool, &st)
	return st, nil
}

// WithLock acquires an advisory lock, loads state, calls fn, then persists
// the diff atomically. Uses pg_advisory_xact_lock for transaction-scoped locking.
func (s *PostgresStore) WithLock(ctx context.Context, fn func(*model.State) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", spawnLockKey); err != nil {
		return fmt.Errorf("pgstore: advisory lock: %w", err)
	}

	before, err := loadStateTx(ctx, tx)
	if err != nil {
		return err
	}

	after := copyState(before)
	if err := fn(&after); err != nil {
		return err
	}

	if err := applyDiff(ctx, tx, before, after); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// FindAgent returns a single agent by task name, or nil if not found.
func (s *PostgresStore) FindAgent(ctx context.Context, task string) (*model.AgentEntry, error) {
	a, err := scanAgent(s.pool.QueryRow(ctx, "SELECT "+agentColumns+" FROM agents WHERE task = $1", task))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pgstore: find agent: %w", err)
	}
	return &a, nil
}

// RemoveAgent deletes a single agent by task name.
func (s *PostgresStore) RemoveAgent(ctx context.Context, task string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM agents WHERE task = $1", task)
	if err != nil {
		return fmt.Errorf("pgstore: remove agent %s: %w", task, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgstore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentStatus updates status and last_output for a single agent.
func (s *PostgresStore) UpdateAgentStatus(ctx context.Context, task string, status model.AgentStatus, lastOutput string) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE agents SET status = $1, last_output = $2 WHERE task = $3",
		string(status), lastOutput, task)
	if err != nil {
		return fmt.Errorf("pgstore: update agent status %s: %w", task, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgstore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentResult updates status, last_output, and optionally resume_token.
func (s *PostgresStore) UpdateAgentResult(ctx context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
	var err error
	var tag pgconn.CommandTag
	if resumeToken != "" {
		tag, err = s.pool.Exec(ctx,
			"UPDATE agents SET status = $1, last_output = $2, resume_token = $3 WHERE task = $4",
			string(status), lastOutput, resumeToken, task)
	} else {
		tag, err = s.pool.Exec(ctx,
			"UPDATE agents SET status = $1, last_output = $2 WHERE task = $3",
			string(status), lastOutput, task)
	}
	if err != nil {
		return fmt.Errorf("pgstore: update agent result %s: %w", task, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgstore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentStatusIf conditionally updates status and last_output.
// The update is applied only if the agent's current status is in expected.
func (s *PostgresStore) UpdateAgentStatusIf(ctx context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
	if len(expected) == 0 {
		return false, nil
	}
	statuses := make([]string, len(expected))
	for i, st := range expected {
		statuses[i] = string(st)
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE agents SET status = $1, last_output = $2 WHERE task = $3 AND status = ANY($4)",
		string(newStatus), lastOutput, task, statuses)
	if err != nil {
		return false, fmt.Errorf("pgstore: update agent status if %s: %w", task, err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpdateAgentResultIf conditionally updates status, last_output, and resume_token.
// The update is applied only if the agent's current status is in expected.
func (s *PostgresStore) UpdateAgentResultIf(ctx context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
	if len(expected) == 0 {
		return false, nil
	}
	statuses := make([]string, len(expected))
	for i, st := range expected {
		statuses[i] = string(st)
	}
	var tag pgconn.CommandTag
	var err error
	if resumeToken != "" {
		tag, err = s.pool.Exec(ctx,
			"UPDATE agents SET status = $1, last_output = $2, resume_token = $3 WHERE task = $4 AND status = ANY($5)",
			string(status), lastOutput, resumeToken, task, statuses)
	} else {
		tag, err = s.pool.Exec(ctx,
			"UPDATE agents SET status = $1, last_output = $2 WHERE task = $3 AND status = ANY($4)",
			string(status), lastOutput, task, statuses)
	}
	if err != nil {
		return false, fmt.Errorf("pgstore: update agent result if %s: %w", task, err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpdateAgentIntent updates only the current_intent field.
func (s *PostgresStore) UpdateAgentIntent(ctx context.Context, task, intent string) error {
	tag, err := s.pool.Exec(ctx, "UPDATE agents SET current_intent = $1 WHERE task = $2", intent, task)
	if err != nil {
		return fmt.Errorf("pgstore: update agent intent %s: %w", task, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgstore: agent %s not found", task)
	}
	return nil
}

// UpdateChessmaster updates the chessmaster singleton.
func (s *PostgresStore) UpdateChessmaster(ctx context.Context, status, lastAction string, updatedAt time.Time) error {
	var ts *time.Time
	if !updatedAt.IsZero() {
		ts = &updatedAt
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO chessmaster (id, status, last_action, updated_at)
		 VALUES (1, $1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET status = $1, last_action = $2, updated_at = $3`,
		status, lastAction, ts)
	if err != nil {
		return fmt.Errorf("pgstore: update chessmaster: %w", err)
	}
	return nil
}

// Mailbox returns the PostgresMailbox sharing this store's pool.
func (s *PostgresStore) Mailbox() mailbox.Mailbox {
	return s.mailbox
}

// Close releases the connection pool.
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// --- internal helpers ---

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func loadChessmaster(ctx context.Context, q querier, st *model.State) {
	var status, lastAction string
	var updatedAt *time.Time
	err := q.QueryRow(ctx, "SELECT status, last_action, updated_at FROM chessmaster WHERE id = 1").
		Scan(&status, &lastAction, &updatedAt)
	if err == nil {
		st.Chessmaster.Status = status
		st.Chessmaster.LastAction = lastAction
		if updatedAt != nil {
			st.Chessmaster.UpdatedAt = *updatedAt
		}
	}
}

func loadStateTx(ctx context.Context, tx pgx.Tx) (model.State, error) {
	var st model.State

	rows, err := tx.Query(ctx, "SELECT "+agentColumns+" FROM agents")
	if err != nil {
		return st, fmt.Errorf("pgstore: load agents tx: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return st, fmt.Errorf("pgstore: scan agent tx: %w", err)
		}
		st.Agents = append(st.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("pgstore: iterate agents tx: %w", err)
	}

	loadChessmaster(ctx, tx, &st)
	return st, nil
}

func copyState(s model.State) model.State {
	out := model.State{
		Chessmaster: s.Chessmaster,
	}
	if s.Agents != nil {
		out.Agents = make([]model.AgentEntry, len(s.Agents))
		copy(out.Agents, s.Agents)
	}
	return out
}

// applyDiff compares before/after and applies minimal changes:
// INSERT new agents, UPDATE changed agents, DELETE removed agents, UPSERT chessmaster.
func applyDiff(ctx context.Context, tx pgx.Tx, before, after model.State) error {
	beforeMap := make(map[string]model.AgentEntry, len(before.Agents))
	for _, a := range before.Agents {
		beforeMap[a.Task] = a
	}
	afterMap := make(map[string]model.AgentEntry, len(after.Agents))
	for _, a := range after.Agents {
		afterMap[a.Task] = a
	}

	// DELETE removed agents.
	for task := range beforeMap {
		if _, ok := afterMap[task]; !ok {
			if _, err := tx.Exec(ctx, "DELETE FROM agents WHERE task = $1", task); err != nil {
				return fmt.Errorf("pgstore: diff delete agent %s: %w", task, err)
			}
		}
	}

	// INSERT new or UPDATE changed agents.
	for _, a := range after.Agents {
		old, existed := beforeMap[a.Task]
		if !existed {
			if err := insertAgentTx(ctx, tx, &a); err != nil {
				return err
			}
		} else if a != old {
			if err := updateAgentTx(ctx, tx, &a); err != nil {
				return err
			}
		}
	}

	// UPSERT chessmaster if changed.
	if after.Chessmaster != before.Chessmaster {
		var ts *time.Time
		if !after.Chessmaster.UpdatedAt.IsZero() {
			ts = &after.Chessmaster.UpdatedAt
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO chessmaster (id, status, last_action, updated_at)
			 VALUES (1, $1, $2, $3)
			 ON CONFLICT (id) DO UPDATE SET status = $1, last_action = $2, updated_at = $3`,
			after.Chessmaster.Status, after.Chessmaster.LastAction, ts)
		if err != nil {
			return fmt.Errorf("pgstore: diff save chessmaster: %w", err)
		}
	}

	return nil
}

func insertAgentTx(ctx context.Context, tx pgx.Tx, a *model.AgentEntry) error {
	var startTime *time.Time
	if !a.StartTime.IsZero() {
		startTime = &a.StartTime
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO agents (`+agentColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		a.Task, a.RuntimeType, a.ResumeToken, a.WorkspacePath, a.BranchName, a.BaseBranch,
		string(a.Status), a.LastOutput, startTime, a.Mode, a.CurrentIntent, a.MissionID, a.ObjectiveID)
	if err != nil {
		return fmt.Errorf("pgstore: insert agent %s: %w", a.Task, err)
	}
	return nil
}

func updateAgentTx(ctx context.Context, tx pgx.Tx, a *model.AgentEntry) error {
	var startTime *time.Time
	if !a.StartTime.IsZero() {
		startTime = &a.StartTime
	}
	_, err := tx.Exec(ctx,
		`UPDATE agents SET host_type=$1, resume_token=$2, workspace_path=$3, branch_name=$4,
		 base_branch=$5, status=$6, last_output=$7, start_time=$8, mode=$9, current_intent=$10,
		 mission_id=$11, objective_id=$12
		 WHERE task=$13`,
		a.RuntimeType, a.ResumeToken, a.WorkspacePath, a.BranchName, a.BaseBranch,
		string(a.Status), a.LastOutput, startTime, a.Mode, a.CurrentIntent, a.MissionID, a.ObjectiveID, a.Task)
	if err != nil {
		return fmt.Errorf("pgstore: update agent %s: %w", a.Task, err)
	}
	return nil
}

// ClaimAgent atomically transitions an agent from Queued to Running
// and resets StartTime to now.
func (s *PostgresStore) ClaimAgent(ctx context.Context, task string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agents SET status = $1, start_time = NOW() WHERE task = $2 AND status = $3`,
		string(model.StatusRunning), task, string(model.StatusQueued))
	if err != nil {
		return fmt.Errorf("pgstore: claim agent %s: %w", task, err)
	}
	if tag.RowsAffected() == 0 {
		return state.ErrAgentNotClaimable
	}
	return nil
}

// Pool returns the underlying pgxpool for shared use (e.g., PostgresQueue).
func (s *PostgresStore) Pool() *pgxpool.Pool {
	return s.pool
}

// Verify interface compliance at compile time.
var _ state.Store = (*PostgresStore)(nil)
