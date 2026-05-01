package admission

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/queue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MissionReader provides read-only access to missions for the admission controller.
// Separate from MissionQueue to avoid extending the queue interface.
type MissionReader interface {
	// GetMission returns a mission by ID. Returns queue.ErrNotFound if absent.
	GetMission(ctx context.Context, id int64) (*queue.Mission, error)

	// CountActiveChildren returns the number of active (pending or claimed)
	// child missions of the given parent.
	CountActiveChildren(ctx context.Context, parentID int64) (int, error)

	// AllMissionsTerminal returns true if all missions for the given objective
	// are in a terminal state (completed, failed, cancelled). Returns true if
	// no missions exist for the objective.
	AllMissionsTerminal(ctx context.Context, objectiveID string) (bool, error)
}

// PostgresMissionReader implements MissionReader backed by PostgreSQL.
type PostgresMissionReader struct {
	pool *pgxpool.Pool
}

// NewPostgresMissionReader creates a PostgresMissionReader sharing the given pool.
func NewPostgresMissionReader(pool *pgxpool.Pool) *PostgresMissionReader {
	return &PostgresMissionReader{pool: pool}
}

func (r *PostgresMissionReader) GetMission(ctx context.Context, id int64) (*queue.Mission, error) {
	var m queue.Mission
	var objectiveID *string
	err := r.pool.QueryRow(ctx,
		`SELECT id, payload, agent_task, status, priority, created_at,
			claimed_by, claimed_at, error,
			objective_id, parent_id, depth, dedupe_key, proposed_by
		 FROM missions WHERE id = $1`, id).Scan(
		&m.ID, &m.Payload, &m.AgentTask, &m.Status, &m.Priority, &m.CreatedAt,
		&m.ClaimedBy, &m.ClaimedAt, &m.Error,
		&objectiveID, &m.ParentID, &m.Depth, &m.DedupeKey, &m.ProposedBy,
	)
	if err == pgx.ErrNoRows {
		return nil, queue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mission reader: get: %w", err)
	}
	if objectiveID != nil {
		m.ObjectiveID = *objectiveID
	}
	return &m, nil
}

func (r *PostgresMissionReader) CountActiveChildren(ctx context.Context, parentID int64) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM missions
		 WHERE parent_id = $1 AND status IN ('pending', 'claimed')`,
		parentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("mission reader: count active children: %w", err)
	}
	return count, nil
}

func (r *PostgresMissionReader) AllMissionsTerminal(ctx context.Context, objectiveID string) (bool, error) {
	var nonTerminal int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM missions
		 WHERE objective_id = $1 AND status IN ('pending', 'claimed')`,
		objectiveID).Scan(&nonTerminal)
	if err != nil {
		return false, fmt.Errorf("mission reader: all terminal: %w", err)
	}
	return nonTerminal == 0, nil
}

var _ MissionReader = (*PostgresMissionReader)(nil)

// SQLiteMissionReader implements MissionReader backed by SQLite.
type SQLiteMissionReader struct {
	db *sql.DB
}

// NewSQLiteMissionReader creates a SQLiteMissionReader sharing the given database.
func NewSQLiteMissionReader(db *sql.DB) *SQLiteMissionReader {
	return &SQLiteMissionReader{db: db}
}

func (r *SQLiteMissionReader) GetMission(ctx context.Context, id int64) (*queue.Mission, error) {
	var m queue.Mission
	var objectiveID sql.NullString
	var parentID sql.NullInt64
	var claimedBy sql.NullString
	var claimedAt sql.NullString
	var errStr sql.NullString
	var createdAt string

	err := r.db.QueryRowContext(ctx,
		`SELECT id, payload, agent_task, status, priority, created_at,
			claimed_by, claimed_at, error,
			objective_id, parent_id, depth, dedupe_key, proposed_by
		 FROM missions WHERE id = ?`, id).Scan(
		&m.ID, &m.Payload, &m.AgentTask, &m.Status, &m.Priority, &createdAt,
		&claimedBy, &claimedAt, &errStr,
		&objectiveID, &parentID, &m.Depth, &m.DedupeKey, &m.ProposedBy,
	)
	if err == sql.ErrNoRows {
		return nil, queue.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite mission reader: get: %w", err)
	}

	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if objectiveID.Valid {
		m.ObjectiveID = objectiveID.String
	}
	if parentID.Valid {
		v := parentID.Int64
		m.ParentID = &v
	}
	if claimedBy.Valid {
		m.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, claimedAt.String)
		m.ClaimedAt = &t
	}
	if errStr.Valid {
		m.Error = errStr.String
	}
	return &m, nil
}

func (r *SQLiteMissionReader) CountActiveChildren(ctx context.Context, parentID int64) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM missions
		 WHERE parent_id = ? AND status IN ('pending', 'claimed')`,
		parentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite mission reader: count active children: %w", err)
	}
	return count, nil
}

func (r *SQLiteMissionReader) AllMissionsTerminal(ctx context.Context, objectiveID string) (bool, error) {
	var nonTerminal int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM missions
		 WHERE objective_id = ? AND status IN ('pending', 'claimed')`,
		objectiveID).Scan(&nonTerminal)
	if err != nil {
		return false, fmt.Errorf("sqlite mission reader: all terminal: %w", err)
	}
	return nonTerminal == 0, nil
}

var _ MissionReader = (*SQLiteMissionReader)(nil)
