package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/strategy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStagingRunStore implements strategy.StagingRunStore using PostgreSQL.
type PgStagingRunStore struct {
	pool *pgxpool.Pool
}

// NewPgStagingRunStore creates a StagingRunStore backed by the given pgx pool.
// Schema is assumed to already exist (applied via schema.sql during store init).
func NewPgStagingRunStore(pool *pgxpool.Pool) *PgStagingRunStore {
	return &PgStagingRunStore{pool: pool}
}

func (s *PgStagingRunStore) Create(ctx context.Context, run *strategy.StagingRun) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	paramsJSON, err := json.Marshal(run.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO staging_runs (id, strategy_name, params_json, params_fp, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.ID, run.StrategyName, paramsJSON, run.ParamsFingerprint,
		string(run.Status), time.Now().UTC(), time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	for tableName, ts := range run.Tables {
		if err := pgInsertTable(ctx, tx, run.ID, tableName, ts); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func pgInsertTable(ctx context.Context, tx pgx.Tx, runID, tableName string, ts *strategy.TableState) error {
	var errorJSON, fetchPlanJSON []byte
	if ts.Error != nil {
		errorJSON, _ = json.Marshal(ts.Error)
	}
	if ts.FetchPlan != nil {
		fetchPlanJSON = []byte(ts.FetchPlan)
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO staging_run_tables
		 (run_id, table_name, fetch_mode, required, status, partial, parquet_path,
		  row_count, bytes_staged, pages_completed, total_estimate, error_json,
		  fetch_plan_json, retry_count, last_offset, last_cursor, last_error_at,
		  resumed_from_restart, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		runID, tableName, ts.FetchMode, ts.Required, string(ts.Status), ts.Partial,
		nilStr(ts.ParquetPath), ts.RowCount, ts.BytesStaged, ts.PagesCompleted,
		ts.TotalEstimate, nilJSON(errorJSON), nilJSON(fetchPlanJSON), ts.RetryCount,
		ts.LastOffset, nilStr(ts.LastCursor), ts.LastErrorAt,
		ts.ResumedFromRestart, ts.StartedAt, ts.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("insert table %s: %w", tableName, err)
	}
	return nil
}

func (s *PgStagingRunStore) Get(ctx context.Context, id string) (*strategy.StagingRun, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, strategy_name, params_json, params_fp, status, error_json,
		        result_json, trace_json, resume_count, recovered_at,
		        execution_claimed_at, created_at, updated_at
		 FROM staging_runs WHERE id = $1`, id)

	run, err := pgScanRun(row)
	if err == pgx.ErrNoRows {
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

func (s *PgStagingRunStore) UpdateStatus(ctx context.Context, id string, status strategy.RunStatus) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_runs SET status = $1, updated_at = $2 WHERE id = $3`,
		string(status), time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *PgStagingRunStore) UpdateTable(ctx context.Context, runID, table string, ts *strategy.TableState) error {
	var errorJSON, fetchPlanJSON []byte
	if ts.Error != nil {
		errorJSON, _ = json.Marshal(ts.Error)
	}
	if ts.FetchPlan != nil {
		fetchPlanJSON = []byte(ts.FetchPlan)
	}

	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_run_tables SET
		    status = $1, partial = $2, parquet_path = $3, row_count = $4,
		    bytes_staged = $5, pages_completed = $6, total_estimate = $7,
		    error_json = $8, fetch_plan_json = $9, retry_count = $10,
		    last_offset = $11, last_cursor = $12, last_error_at = $13,
		    resumed_from_restart = $14, started_at = $15, completed_at = $16
		 WHERE run_id = $17 AND table_name = $18`,
		string(ts.Status), ts.Partial, nilStr(ts.ParquetPath), ts.RowCount,
		ts.BytesStaged, ts.PagesCompleted, ts.TotalEstimate,
		nilJSON(errorJSON), nilJSON(fetchPlanJSON), ts.RetryCount,
		ts.LastOffset, nilStr(ts.LastCursor), ts.LastErrorAt,
		ts.ResumedFromRestart, ts.StartedAt, ts.CompletedAt,
		runID, table)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("table %q not found in run %q", table, runID)
	}

	_, _ = s.pool.Exec(ctx,
		`UPDATE staging_runs SET updated_at = $1 WHERE id = $2`, now, runID)
	return nil
}

func (s *PgStagingRunStore) CASExecute(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_runs SET status = 'executing', execution_claimed_at = $1, updated_at = $2
		 WHERE id = $3 AND status = 'staged'`,
		now, now, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PgStagingRunStore) SetResult(ctx context.Context, id string, status strategy.RunStatus, result, trace json.RawMessage) error {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_runs SET status = $1, result_json = $2, trace_json = $3, updated_at = $4
		 WHERE id = $5`,
		string(status), nilJSON(result), nilJSON(trace), now, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *PgStagingRunStore) FailRun(ctx context.Context, id string, structErr *strategy.StructuredError) error {
	now := time.Now().UTC()
	var errorJSON []byte
	if structErr != nil {
		errorJSON, _ = json.Marshal(structErr)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_runs SET status = 'failed', error_json = $1, updated_at = $2 WHERE id = $3`,
		nilJSON(errorJSON), now, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *PgStagingRunStore) UpdateRecovery(ctx context.Context, id string, resumeCount int, recoveredAt time.Time) error {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx,
		`UPDATE staging_runs SET resume_count = $1, recovered_at = $2, updated_at = $3 WHERE id = $4`,
		resumeCount, recoveredAt.UTC(), now, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("staging run %q not found", id)
	}
	return nil
}

func (s *PgStagingRunStore) ListActive(ctx context.Context) ([]*strategy.StagingRun, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, strategy_name, params_json, params_fp, status, error_json,
		        result_json, trace_json, resume_count, recovered_at,
		        execution_claimed_at, created_at, updated_at
		 FROM staging_runs
		 WHERE status NOT IN ('complete', 'failed')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*strategy.StagingRun
	for rows.Next() {
		run, err := pgScanRunFromRows(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
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

func (s *PgStagingRunStore) Reap(ctx context.Context, terminalTTL, staleTTL time.Duration) (int, error) {
	now := time.Now().UTC()

	tag1, err := s.pool.Exec(ctx,
		`DELETE FROM staging_runs WHERE status IN ('complete', 'failed') AND updated_at < $1`,
		now.Add(-terminalTTL))
	if err != nil {
		return 0, err
	}

	tag2, err := s.pool.Exec(ctx,
		`DELETE FROM staging_runs WHERE status NOT IN ('complete', 'failed') AND updated_at < $1`,
		now.Add(-staleTTL))
	if err != nil {
		return int(tag1.RowsAffected()), err
	}

	return int(tag1.RowsAffected() + tag2.RowsAffected()), nil
}

func (s *PgStagingRunStore) loadTables(ctx context.Context, runID string) (map[string]*strategy.TableState, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT table_name, fetch_mode, required, status, partial, parquet_path,
		        row_count, bytes_staged, pages_completed, total_estimate, error_json,
		        fetch_plan_json, retry_count, last_offset, last_cursor, last_error_at,
		        resumed_from_restart, started_at, completed_at
		 FROM staging_run_tables WHERE run_id = $1`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make(map[string]*strategy.TableState)
	for rows.Next() {
		ts, err := pgScanTable(rows)
		if err != nil {
			return nil, err
		}
		tables[ts.Name] = ts
	}
	return tables, rows.Err()
}

// --- Scan helpers ---

func pgScanRun(row pgx.Row) (*strategy.StagingRun, error) {
	var run strategy.StagingRun
	var paramsJSON, errorJSON, resultJSON, traceJSON []byte
	var recoveredAt, executionClaimedAt *time.Time
	var status string

	err := row.Scan(
		&run.ID, &run.StrategyName, &paramsJSON, &run.ParamsFingerprint,
		&status, &errorJSON, &resultJSON, &traceJSON,
		&run.ResumeCount, &recoveredAt, &executionClaimedAt,
		&run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	run.Status = strategy.RunStatus(status)
	if paramsJSON != nil {
		json.Unmarshal(paramsJSON, &run.Params)
	}
	if errorJSON != nil {
		var se strategy.StructuredError
		json.Unmarshal(errorJSON, &se)
		run.Error = &se
	}
	if resultJSON != nil {
		run.Result = json.RawMessage(resultJSON)
	}
	if traceJSON != nil {
		run.Trace = json.RawMessage(traceJSON)
	}
	run.RecoveredAt = recoveredAt
	run.ExecutionClaimedAt = executionClaimedAt

	return &run, nil
}

func pgScanRunFromRows(rows pgx.Rows) (*strategy.StagingRun, error) {
	var run strategy.StagingRun
	var paramsJSON, errorJSON, resultJSON, traceJSON []byte
	var recoveredAt, executionClaimedAt *time.Time
	var status string

	err := rows.Scan(
		&run.ID, &run.StrategyName, &paramsJSON, &run.ParamsFingerprint,
		&status, &errorJSON, &resultJSON, &traceJSON,
		&run.ResumeCount, &recoveredAt, &executionClaimedAt,
		&run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	run.Status = strategy.RunStatus(status)
	if paramsJSON != nil {
		json.Unmarshal(paramsJSON, &run.Params)
	}
	if errorJSON != nil {
		var se strategy.StructuredError
		json.Unmarshal(errorJSON, &se)
		run.Error = &se
	}
	if resultJSON != nil {
		run.Result = json.RawMessage(resultJSON)
	}
	if traceJSON != nil {
		run.Trace = json.RawMessage(traceJSON)
	}
	run.RecoveredAt = recoveredAt
	run.ExecutionClaimedAt = executionClaimedAt

	return &run, nil
}

func pgScanTable(rows pgx.Rows) (*strategy.TableState, error) {
	var ts strategy.TableState
	var parquetPath, lastCursor *string
	var errorJSON, fetchPlanJSON []byte
	var lastErrorAt, startedAt, completedAt *time.Time
	var status string

	err := rows.Scan(
		&ts.Name, &ts.FetchMode, &ts.Required, &status, &ts.Partial, &parquetPath,
		&ts.RowCount, &ts.BytesStaged, &ts.PagesCompleted, &ts.TotalEstimate,
		&errorJSON, &fetchPlanJSON, &ts.RetryCount, &ts.LastOffset, &lastCursor,
		&lastErrorAt, &ts.ResumedFromRestart, &startedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	ts.Status = strategy.TableStatus(status)
	if parquetPath != nil {
		ts.ParquetPath = *parquetPath
	}
	if lastCursor != nil {
		ts.LastCursor = *lastCursor
	}
	if errorJSON != nil {
		var se strategy.StructuredError
		json.Unmarshal(errorJSON, &se)
		ts.Error = &se
	}
	if fetchPlanJSON != nil {
		ts.FetchPlan = json.RawMessage(fetchPlanJSON)
	}
	ts.LastErrorAt = lastErrorAt
	ts.StartedAt = startedAt
	ts.CompletedAt = completedAt

	return &ts, nil
}

// --- Helpers ---

func nilStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilJSON(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
