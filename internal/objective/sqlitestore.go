package objective

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SQLiteStore implements ObjectiveStore backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a SQLiteStore sharing the given database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func scanObjectiveSQLite(scanner interface{ Scan(...any) error }) (*Objective, error) {
	var o Objective
	var maxRuntime int64
	var createdAt, updatedAt string
	var outcomeData sql.NullString
	err := scanner.Scan(
		&o.ID, &o.Description, &o.Status, &createdAt, &updatedAt, &o.CreatedBy,
		&o.MaxMissions, &o.MaxDepth, &maxRuntime, &o.MaxBranching,
		&o.MissionCount, &o.FindingCount, &o.Outcome, &outcomeData,
	)
	if err != nil {
		return nil, err
	}
	o.MaxRuntime = time.Duration(maxRuntime)
	if createdAt != "" {
		o.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	}
	if updatedAt != "" {
		o.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	}
	if outcomeData.Valid && outcomeData.String != "" {
		o.OutcomeData = json.RawMessage(outcomeData.String)
	}
	return &o, nil
}

const sqliteObjectiveCols = `id, description, status, created_at, updated_at, created_by,
	max_missions, max_depth, max_runtime, max_branching,
	mission_count, finding_count, outcome, outcome_data`

func (s *SQLiteStore) Create(ctx context.Context, o *Objective) error {
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

	var outcomeData *string
	if o.OutcomeData != nil {
		s := string(o.OutcomeData)
		outcomeData = &s
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO objectives (id, description, status, created_at, updated_at, created_by,
			max_missions, max_depth, max_runtime, max_branching,
			mission_count, finding_count, outcome, outcome_data)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.Description, o.Status,
		o.CreatedAt.Format(time.RFC3339Nano), o.UpdatedAt.Format(time.RFC3339Nano),
		o.CreatedBy,
		o.MaxMissions, o.MaxDepth, int64(o.MaxRuntime), o.MaxBranching,
		o.MissionCount, o.FindingCount, o.Outcome, outcomeData)
	if err != nil {
		return fmt.Errorf("objective sqlitestore: create: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Objective, error) {
	o, err := scanObjectiveSQLite(s.db.QueryRowContext(ctx,
		`SELECT `+sqliteObjectiveCols+` FROM objectives WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("objective sqlitestore: get: %w", err)
	}
	return o, nil
}

func (s *SQLiteStore) Update(ctx context.Context, o *Objective) error {
	o.UpdatedAt = time.Now()

	var outcomeData *string
	if o.OutcomeData != nil {
		s := string(o.OutcomeData)
		outcomeData = &s
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE objectives SET description=?, status=?, updated_at=?, created_by=?,
			max_missions=?, max_depth=?, max_runtime=?, max_branching=?,
			mission_count=?, finding_count=?, outcome=?, outcome_data=?
		 WHERE id=?`,
		o.Description, o.Status, o.UpdatedAt.Format(time.RFC3339Nano), o.CreatedBy,
		o.MaxMissions, o.MaxDepth, int64(o.MaxRuntime), o.MaxBranching,
		o.MissionCount, o.FindingCount, o.Outcome, outcomeData, o.ID)
	if err != nil {
		return fmt.Errorf("objective sqlitestore: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) IncrementMissionCount(ctx context.Context, id string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE objectives SET mission_count = mission_count + 1, updated_at = ? WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("objective sqlitestore: increment mission count: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) IncrementFindingCount(ctx context.Context, id string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE objectives SET finding_count = finding_count + 1, updated_at = ? WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("objective sqlitestore: increment finding count: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) ListByStatus(ctx context.Context, status ObjectiveStatus) ([]*Objective, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sqliteObjectiveCols+` FROM objectives WHERE status = ? ORDER BY created_at`, string(status))
	if err != nil {
		return nil, fmt.Errorf("objective sqlitestore: list by status: %w", err)
	}
	defer rows.Close()

	var result []*Objective
	for rows.Next() {
		o, err := scanObjectiveSQLite(rows)
		if err != nil {
			return nil, fmt.Errorf("objective sqlitestore: scan: %w", err)
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

var _ ObjectiveStore = (*SQLiteStore)(nil)
