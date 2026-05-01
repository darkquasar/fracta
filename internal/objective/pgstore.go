package objective

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements ObjectiveStore backed by PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a PostgresStore sharing the given pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const objectiveCols = `id, description, status, created_at, updated_at, created_by,
	max_missions, max_depth, max_runtime, max_branching,
	mission_count, finding_count, outcome, outcome_data`

func scanObjective(row pgx.Row) (*Objective, error) {
	var o Objective
	var maxRuntime int64
	var outcomeData []byte
	err := row.Scan(
		&o.ID, &o.Description, &o.Status, &o.CreatedAt, &o.UpdatedAt, &o.CreatedBy,
		&o.MaxMissions, &o.MaxDepth, &maxRuntime, &o.MaxBranching,
		&o.MissionCount, &o.FindingCount, &o.Outcome, &outcomeData,
	)
	if err != nil {
		return nil, err
	}
	o.MaxRuntime = time.Duration(maxRuntime)
	if outcomeData != nil {
		o.OutcomeData = json.RawMessage(outcomeData)
	}
	return &o, nil
}

func (s *PostgresStore) Create(ctx context.Context, o *Objective) error {
	now := time.Now()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = now
	}
	if o.Status == "" {
		o.Status = StatusOpen
	}
	o.ApplyDefaults()

	_, err := s.pool.Exec(ctx,
		`INSERT INTO objectives (id, description, status, created_at, updated_at, created_by,
			max_missions, max_depth, max_runtime, max_branching,
			mission_count, finding_count, outcome, outcome_data)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		o.ID, o.Description, o.Status, o.CreatedAt, o.UpdatedAt, o.CreatedBy,
		o.MaxMissions, o.MaxDepth, int64(o.MaxRuntime), o.MaxBranching,
		o.MissionCount, o.FindingCount, o.Outcome, o.OutcomeData)
	if err != nil {
		return fmt.Errorf("objective pgstore: create: %w", err)
	}
	return nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (*Objective, error) {
	o, err := scanObjective(s.pool.QueryRow(ctx,
		`SELECT `+objectiveCols+` FROM objectives WHERE id = $1`, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("objective pgstore: get: %w", err)
	}
	return o, nil
}

func (s *PostgresStore) Update(ctx context.Context, o *Objective) error {
	o.UpdatedAt = time.Now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE objectives SET description=$1, status=$2, updated_at=$3, created_by=$4,
			max_missions=$5, max_depth=$6, max_runtime=$7, max_branching=$8,
			mission_count=$9, finding_count=$10, outcome=$11, outcome_data=$12
		 WHERE id=$13`,
		o.Description, o.Status, o.UpdatedAt, o.CreatedBy,
		o.MaxMissions, o.MaxDepth, int64(o.MaxRuntime), o.MaxBranching,
		o.MissionCount, o.FindingCount, o.Outcome, o.OutcomeData, o.ID)
	if err != nil {
		return fmt.Errorf("objective pgstore: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) IncrementMissionCount(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE objectives SET mission_count = mission_count + 1, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("objective pgstore: increment mission count: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) IncrementFindingCount(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE objectives SET finding_count = finding_count + 1, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("objective pgstore: increment finding count: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListByStatus(ctx context.Context, status ObjectiveStatus) ([]*Objective, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+objectiveCols+` FROM objectives WHERE status = $1 ORDER BY created_at`, status)
	if err != nil {
		return nil, fmt.Errorf("objective pgstore: list by status: %w", err)
	}
	defer rows.Close()

	var result []*Objective
	for rows.Next() {
		o, err := scanObjective(rows)
		if err != nil {
			return nil, fmt.Errorf("objective pgstore: scan: %w", err)
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

var _ ObjectiveStore = (*PostgresStore)(nil)
