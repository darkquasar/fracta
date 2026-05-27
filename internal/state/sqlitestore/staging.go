package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/strategy"
)

// StagingRunStore implements strategy.StagingRunStore using SQLite.
type StagingRunStore struct {
	db *sql.DB
}

// NewStagingRunStore creates a new StagingRunStore backed by the given SQLite DB.
// It creates the required tables if they don't exist.
func NewStagingRunStore(db *sql.DB) (*StagingRunStore, error) {
	// Execute schema statements individually (some SQLite drivers only run
	// the first statement in a multi-statement Exec).
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS staging_runs (
			id                   TEXT PRIMARY KEY,
			strategy_name        TEXT NOT NULL,
			params_json          TEXT NOT NULL,
			params_fp            TEXT NOT NULL,
			status               TEXT NOT NULL DEFAULT 'created',
			error_json           TEXT,
			result_json          TEXT,
			trace_json           TEXT,
			resume_count         INTEGER DEFAULT 0,
			recovered_at         TEXT,
			execution_claimed_at TEXT,
			created_at           TEXT NOT NULL,
			updated_at           TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS staging_run_tables (
			run_id               TEXT NOT NULL REFERENCES staging_runs(id) ON DELETE CASCADE,
			table_name           TEXT NOT NULL,
			fetch_mode           TEXT NOT NULL,
			required             INTEGER NOT NULL DEFAULT 1,
			status               TEXT NOT NULL DEFAULT 'pending',
			partial              INTEGER NOT NULL DEFAULT 0,
			parquet_path         TEXT,
			row_count            INTEGER DEFAULT 0,
			bytes_staged         INTEGER DEFAULT 0,
			pages_completed      INTEGER DEFAULT 0,
			total_estimate       INTEGER DEFAULT 0,
			error_json           TEXT,
			fetch_plan_json      TEXT,
			retry_count          INTEGER DEFAULT 0,
			last_offset          INTEGER DEFAULT 0,
			last_cursor          TEXT,
			last_error_at        TEXT,
			resumed_from_restart INTEGER DEFAULT 0,
			started_at           TEXT,
			completed_at         TEXT,
			PRIMARY KEY (run_id, table_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_staging_runs_status ON staging_runs(status)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return nil, fmt.Errorf("create staging tables: %w", err)
		}
	}
	return &StagingRunStore{db: db}, nil
}

func (s *StagingRunStore) Create(ctx context.Context, run *strategy.StagingRun) error {
	paramsJSON, err := json.Marshal(run.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO staging_runs (id, strategy_name, params_json, params_fp, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.StrategyName, string(paramsJSON), run.ParamsFingerprint,
		string(run.Status), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	for tableName, ts := range run.Tables {
		if err := insertTable(ctx, tx, run.ID, tableName, ts); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func insertTable(ctx context.Context, tx *sql.Tx, runID, tableName string, ts *strategy.TableState) error {
	var errorJSON, fetchPlanJSON *string
	if ts.Error != nil {
		b, _ := json.Marshal(ts.Error)
		s := string(b)
		errorJSON = &s
	}
	if ts.FetchPlan != nil {
		s := string(ts.FetchPlan)
		fetchPlanJSON = &s
	}

	var startedAt, completedAt, lastErrorAt *string
	if ts.StartedAt != nil {
		s := ts.StartedAt.UTC().Format(time.RFC3339Nano)
		startedAt = &s
	}
	if ts.CompletedAt != nil {
		s := ts.CompletedAt.UTC().Format(time.RFC3339Nano)
		completedAt = &s
	}
	if ts.LastErrorAt != nil {
		s := ts.LastErrorAt.UTC().Format(time.RFC3339Nano)
		lastErrorAt = &s
	}

	required := 0
	if ts.Required {
		required = 1
	}
	partial := 0
	if ts.Partial {
		partial = 1
	}
	resumedFromRestart := 0
	if ts.ResumedFromRestart {
		resumedFromRestart = 1
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO staging_run_tables
		 (run_id, table_name, fetch_mode, required, status, partial, parquet_path,
		  row_count, bytes_staged, pages_completed, total_estimate, error_json,
		  fetch_plan_json, retry_count, last_offset, last_cursor, last_error_at,
		  resumed_from_restart, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, tableName, ts.FetchMode, required, string(ts.Status), partial,
		nilStr(ts.ParquetPath), ts.RowCount, ts.BytesStaged, ts.PagesCompleted,
		ts.TotalEstimate, errorJSON, fetchPlanJSON, ts.RetryCount,
		ts.LastOffset, nilStr(ts.LastCursor), lastErrorAt, resumedFromRestart,
		startedAt, completedAt,
	)
	if err != nil {
		return fmt.Errorf("insert table %s: %w", tableName, err)
	}
	return nil
}

func (s *StagingRunStore) Get(ctx context.Context, id string) (*strategy.StagingRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, strategy_name, params_json, params_fp, status, error_json,
		        result_json, trace_json, resume_count, recovered_at,
		        execution_claimed_at, created_at, updated_at
		 FROM staging_runs WHERE id = ?`, id)

	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	tables, err := s.loadTables(ctx, id)
	if err != nil {
		return nil, err
	}
	run.Tables = tables
	return run, nil
}

func (s *StagingRunStore) UpdateStatus(ctx context.Context, id string, status strategy.RunStatus) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_runs SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *StagingRunStore) UpdateTable(ctx context.Context, runID, table string, ts *strategy.TableState) error {
	var errorJSON, fetchPlanJSON *string
	if ts.Error != nil {
		b, _ := json.Marshal(ts.Error)
		str := string(b)
		errorJSON = &str
	}
	if ts.FetchPlan != nil {
		str := string(ts.FetchPlan)
		fetchPlanJSON = &str
	}

	var startedAt, completedAt, lastErrorAt *string
	if ts.StartedAt != nil {
		str := ts.StartedAt.UTC().Format(time.RFC3339Nano)
		startedAt = &str
	}
	if ts.CompletedAt != nil {
		str := ts.CompletedAt.UTC().Format(time.RFC3339Nano)
		completedAt = &str
	}
	if ts.LastErrorAt != nil {
		str := ts.LastErrorAt.UTC().Format(time.RFC3339Nano)
		lastErrorAt = &str
	}

	partial := 0
	if ts.Partial {
		partial = 1
	}
	resumedFromRestart := 0
	if ts.ResumedFromRestart {
		resumedFromRestart = 1
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_run_tables SET
		    status = ?, partial = ?, parquet_path = ?, row_count = ?,
		    bytes_staged = ?, pages_completed = ?, total_estimate = ?,
		    error_json = ?, fetch_plan_json = ?, retry_count = ?,
		    last_offset = ?, last_cursor = ?, last_error_at = ?,
		    resumed_from_restart = ?, started_at = ?, completed_at = ?
		 WHERE run_id = ? AND table_name = ?`,
		string(ts.Status), partial, nilStr(ts.ParquetPath), ts.RowCount,
		ts.BytesStaged, ts.PagesCompleted, ts.TotalEstimate,
		errorJSON, fetchPlanJSON, ts.RetryCount,
		ts.LastOffset, nilStr(ts.LastCursor), lastErrorAt,
		resumedFromRestart, startedAt, completedAt,
		runID, table)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("table %q not found in run %q", table, runID)
	}

	// Update run's updated_at
	_, _ = s.db.ExecContext(ctx,
		`UPDATE staging_runs SET updated_at = ? WHERE id = ?`, now, runID)
	return nil
}

func (s *StagingRunStore) CASExecute(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_runs SET status = 'executing', execution_claimed_at = ?, updated_at = ?
		 WHERE id = ? AND status = 'staged'`,
		now, now, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *StagingRunStore) SetResult(ctx context.Context, id string, status strategy.RunStatus, result, trace json.RawMessage) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_runs SET status = ?, result_json = ?, trace_json = ?, updated_at = ?
		 WHERE id = ?`,
		string(status), nilRaw(result), nilRaw(trace), now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *StagingRunStore) FailRun(ctx context.Context, id string, structErr *strategy.StructuredError) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var errorJSON *string
	if structErr != nil {
		b, _ := json.Marshal(structErr)
		s := string(b)
		errorJSON = &s
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_runs SET status = 'failed', error_json = ?, updated_at = ? WHERE id = ?`,
		errorJSON, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *StagingRunStore) UpdateRecovery(ctx context.Context, id string, resumeCount int, recoveredAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rat := recoveredAt.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE staging_runs SET resume_count = ?, recovered_at = ?, updated_at = ? WHERE id = ?`,
		resumeCount, rat, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *StagingRunStore) ListActive(ctx context.Context) ([]*strategy.StagingRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, strategy_name, params_json, params_fp, status, error_json,
		        result_json, trace_json, resume_count, recovered_at,
		        execution_claimed_at, created_at, updated_at
		 FROM staging_runs
		 WHERE status NOT IN ('complete', 'failed')`)
	if err != nil {
		return nil, err
	}

	// Collect runs first, close rows before issuing loadTables queries
	// (avoids deadlock with MaxOpenConns=1 on in-memory SQLite).
	var runs []*strategy.StagingRun
	for rows.Next() {
		run, err := scanRunFromRows(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, run := range runs {
		tables, err := s.loadTables(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		run.Tables = tables
	}
	return runs, nil
}

func (s *StagingRunStore) Reap(ctx context.Context, terminalTTL, staleTTL time.Duration) (int, error) {
	now := time.Now().UTC()

	// Reap terminal runs older than terminalTTL.
	terminalCutoff := now.Add(-terminalTTL).Format(time.RFC3339Nano)
	res1, err := s.db.ExecContext(ctx,
		`DELETE FROM staging_runs WHERE status IN ('complete', 'failed') AND updated_at < ?`,
		terminalCutoff)
	if err != nil {
		return 0, err
	}
	n1, _ := res1.RowsAffected()

	// Reap stale non-terminal runs older than staleTTL.
	staleCutoff := now.Add(-staleTTL).Format(time.RFC3339Nano)
	res2, err := s.db.ExecContext(ctx,
		`DELETE FROM staging_runs WHERE status NOT IN ('complete', 'failed') AND updated_at < ?`,
		staleCutoff)
	if err != nil {
		return int(n1), err
	}
	n2, _ := res2.RowsAffected()

	return int(n1 + n2), nil
}

// loadTables retrieves all table states for a given run.
func (s *StagingRunStore) loadTables(ctx context.Context, runID string) (map[string]*strategy.TableState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT table_name, fetch_mode, required, status, partial, parquet_path,
		        row_count, bytes_staged, pages_completed, total_estimate, error_json,
		        fetch_plan_json, retry_count, last_offset, last_cursor, last_error_at,
		        resumed_from_restart, started_at, completed_at
		 FROM staging_run_tables WHERE run_id = ?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make(map[string]*strategy.TableState)
	for rows.Next() {
		ts, err := scanTable(rows)
		if err != nil {
			return nil, err
		}
		tables[ts.Name] = ts
	}
	return tables, rows.Err()
}

// --- Scan helpers ---

func scanRun(row *sql.Row) (*strategy.StagingRun, error) {
	var run strategy.StagingRun
	var paramsJSON, errorJSON, resultJSON, traceJSON sql.NullString
	var recoveredAt, executionClaimedAt, createdAt, updatedAt sql.NullString
	var status string

	err := row.Scan(
		&run.ID, &run.StrategyName, &paramsJSON, &run.ParamsFingerprint,
		&status, &errorJSON, &resultJSON, &traceJSON,
		&run.ResumeCount, &recoveredAt, &executionClaimedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	run.Status = strategy.RunStatus(status)
	if paramsJSON.Valid {
		json.Unmarshal([]byte(paramsJSON.String), &run.Params)
	}
	if errorJSON.Valid {
		var se strategy.StructuredError
		json.Unmarshal([]byte(errorJSON.String), &se)
		run.Error = &se
	}
	if resultJSON.Valid {
		run.Result = json.RawMessage(resultJSON.String)
	}
	if traceJSON.Valid {
		run.Trace = json.RawMessage(traceJSON.String)
	}
	run.RecoveredAt = parseNullTime(recoveredAt)
	run.ExecutionClaimedAt = parseNullTime(executionClaimedAt)
	run.CreatedAt = parseTimeOrZero(createdAt)
	run.UpdatedAt = parseTimeOrZero(updatedAt)

	return &run, nil
}

func scanRunFromRows(rows *sql.Rows) (*strategy.StagingRun, error) {
	var run strategy.StagingRun
	var paramsJSON, errorJSON, resultJSON, traceJSON sql.NullString
	var recoveredAt, executionClaimedAt, createdAt, updatedAt sql.NullString
	var status string

	err := rows.Scan(
		&run.ID, &run.StrategyName, &paramsJSON, &run.ParamsFingerprint,
		&status, &errorJSON, &resultJSON, &traceJSON,
		&run.ResumeCount, &recoveredAt, &executionClaimedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	run.Status = strategy.RunStatus(status)
	if paramsJSON.Valid {
		json.Unmarshal([]byte(paramsJSON.String), &run.Params)
	}
	if errorJSON.Valid {
		var se strategy.StructuredError
		json.Unmarshal([]byte(errorJSON.String), &se)
		run.Error = &se
	}
	if resultJSON.Valid {
		run.Result = json.RawMessage(resultJSON.String)
	}
	if traceJSON.Valid {
		run.Trace = json.RawMessage(traceJSON.String)
	}
	run.RecoveredAt = parseNullTime(recoveredAt)
	run.ExecutionClaimedAt = parseNullTime(executionClaimedAt)
	run.CreatedAt = parseTimeOrZero(createdAt)
	run.UpdatedAt = parseTimeOrZero(updatedAt)

	return &run, nil
}

func scanTable(rows *sql.Rows) (*strategy.TableState, error) {
	var ts strategy.TableState
	var required, partial, resumedFromRestart int
	var parquetPath, errorJSON, fetchPlanJSON, lastCursor sql.NullString
	var lastErrorAt, startedAt, completedAt sql.NullString
	var status string

	err := rows.Scan(
		&ts.Name, &ts.FetchMode, &required, &status, &partial, &parquetPath,
		&ts.RowCount, &ts.BytesStaged, &ts.PagesCompleted, &ts.TotalEstimate,
		&errorJSON, &fetchPlanJSON, &ts.RetryCount, &ts.LastOffset, &lastCursor,
		&lastErrorAt, &resumedFromRestart, &startedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	ts.Status = strategy.TableStatus(status)
	ts.Required = required == 1
	ts.Partial = partial == 1
	ts.ResumedFromRestart = resumedFromRestart == 1
	if parquetPath.Valid {
		ts.ParquetPath = parquetPath.String
	}
	if lastCursor.Valid {
		ts.LastCursor = lastCursor.String
	}
	if errorJSON.Valid {
		var se strategy.StructuredError
		json.Unmarshal([]byte(errorJSON.String), &se)
		ts.Error = &se
	}
	if fetchPlanJSON.Valid {
		ts.FetchPlan = json.RawMessage(fetchPlanJSON.String)
	}
	ts.LastErrorAt = parseNullTime(lastErrorAt)
	ts.StartedAt = parseNullTime(startedAt)
	ts.CompletedAt = parseNullTime(completedAt)

	return &ts, nil
}

// --- Helpers ---

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilRaw(r json.RawMessage) *string {
	if r == nil {
		return nil
	}
	s := string(r)
	return &s
}

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func parseTimeOrZero(ns sql.NullString) time.Time {
	if !ns.Valid || ns.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, ns.String)
	return t
}
