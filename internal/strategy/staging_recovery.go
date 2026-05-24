package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/loaders"
)

const (
	recoveryMaxRetries    = 2
	recoveryBaseBackoff   = 2 * time.Second
	recoveryBackoffFactor = 2
)

// RecoverActiveRuns resumes in-progress staging runs that were interrupted by
// a pod restart. It loads active runs from the store, verifies Parquet state
// on disk, and re-launches background staging goroutines for incomplete tables.
//
// Parameters:
//   - fetcher: MCPFetcher for re-launching MCP-based table fetches (may be nil if MCP unavailable)
//   - sc: strategy Runner providing StagingDir() for Parquet path verification
//   - store: persistent StagingRunStore for state updates
func RecoverActiveRuns(ctx context.Context, fetcher *loaders.MCPFetcher, sc Runner, store StagingRunStore) error {
	log := fractalog.Component("strategy")

	runs, err := store.ListActive(ctx)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return nil
	}

	log.Info("recovering active staging runs from prior lifecycle", "count", len(runs))

	recovered := 0
	for _, run := range runs {
		if run.Status != RunStatusStaging {
			// Only recover runs that were actively staging. Pending/created runs
			// can be re-triggered by the next strategy_run call.
			log.Info("skipping non-staging run", "run_id", run.ID, "status", string(run.Status))
			continue
		}

		if err := recoverRun(ctx, run, fetcher, sc, store); err != nil {
			log.Warn("failed to recover run", "run_id", run.ID, "error", err)
			// Mark as failed so it doesn't block future runs.
			_ = store.FailRun(ctx, run.ID, &StructuredError{
				Message:  fmt.Sprintf("recovery failed: %v", err),
				Category: "permanent",
				Phase:    "staging",
			})
			continue
		}
		recovered++
	}

	if recovered > 0 {
		log.Info("staging.run.recovered", "recovered", recovered, "total_active", len(runs))
	}
	return nil
}

// recoverRun attempts recovery for a single staging run.
func recoverRun(ctx context.Context, run *StagingRun, fetcher *loaders.MCPFetcher, sc Runner, store StagingRunStore) error {
	log := fractalog.Component("strategy")

	// Increment resume_count and set recovered_at — persist to DB.
	now := time.Now()
	run.ResumeCount++
	run.RecoveredAt = &now
	run.UpdatedAt = now

	if err := store.UpdateRecovery(ctx, run.ID, run.ResumeCount, now); err != nil {
		return fmt.Errorf("persist recovery diagnostics: %w", err)
	}

	// Keep status as "staging" since we're re-entering the goroutines.
	if err := store.UpdateStatus(ctx, run.ID, RunStatusStaging); err != nil {
		return fmt.Errorf("update run status: %w", err)
	}

	tablesRecoverable := 0
	tablesAlreadyDone := 0

	for tableName, ts := range run.Tables {
		switch ts.Status {
		case TableStatusStaged, TableStatusFailed, TableStatusSkipped:
			tablesAlreadyDone++
			continue
		case TableStatusFetching, TableStatusPending:
			// These need recovery.
		default:
			continue
		}

		// Check if Parquet already exists on disk (completed before crash but
		// store wasn't updated).
		if ts.ParquetPath != "" {
			if _, err := os.Stat(ts.ParquetPath); err == nil {
				log.Info("parquet exists on disk, marking staged",
					"run_id", run.ID, "table", tableName, "path", ts.ParquetPath)
				completedNow := time.Now()
				ts.Status = TableStatusStaged
				ts.CompletedAt = &completedNow
				ts.ResumedFromRestart = true
				if err := store.UpdateTable(ctx, run.ID, tableName, ts); err != nil {
					log.Warn("failed to mark recovered table as staged", "table", tableName, "error", err)
				}
				tablesAlreadyDone++
				continue
			}
		}

		// No Parquet on disk — need to re-fetch. Deserialize FetchPlan.
		if len(ts.FetchPlan) == 0 {
			log.Warn("no fetch plan for incomplete table, marking failed",
				"run_id", run.ID, "table", tableName)
			failTable(ctx, store, run.ID, tableName, ts, "no serialized fetch plan for recovery")
			continue
		}

		if fetcher == nil {
			log.Warn("MCP fetcher unavailable, cannot recover table",
				"run_id", run.ID, "table", tableName)
			failTable(ctx, store, run.ID, tableName, ts, "MCP fetcher unavailable at recovery time")
			continue
		}

		var plan SerializedFetchPlan
		if err := json.Unmarshal(ts.FetchPlan, &plan); err != nil {
			log.Warn("failed to deserialize fetch plan, marking failed",
				"run_id", run.ID, "table", tableName, "error", err)
			failTable(ctx, store, run.ID, tableName, ts, fmt.Sprintf("fetch plan unmarshal: %v", err))
			continue
		}

		// Mark table as fetching with resume metadata.
		ts.Status = TableStatusFetching
		ts.ResumedFromRestart = true
		startedNow := time.Now()
		ts.StartedAt = &startedNow
		if err := store.UpdateTable(ctx, run.ID, tableName, ts); err != nil {
			log.Warn("failed to update table to fetching for recovery", "table", tableName, "error", err)
		}

		// Launch recovery goroutine.
		go recoverTable(run.ID, tableName, ts, &plan, fetcher, sc, store)
		tablesRecoverable++
	}

	log.Info("run recovery dispatched",
		"run_id", run.ID,
		"tables_relaunched", tablesRecoverable,
		"tables_already_done", tablesAlreadyDone,
		"resume_count", run.ResumeCount)

	// If all tables were already done, check if we should transition the run.
	if tablesRecoverable == 0 {
		checkAndTransitionRecoveredRun(ctx, run.ID, store)
	}

	return nil
}

// recoverTable is the recovery goroutine for a single table. Similar to
// stageTable in mcpserver/strategy_tools.go but reconstructs MCPFetchOpts
// from the serialized FetchPlan.
func recoverTable(
	runID string,
	tableName string,
	ts *TableState,
	plan *SerializedFetchPlan,
	fetcher *loaders.MCPFetcher,
	sc Runner,
	store StagingRunStore,
) {
	log := fractalog.Component("strategy")
	ctx := context.Background()

	// Build MCPFetchOpts from the serialized plan.
	fields := make([]loaders.FieldMapping, 0, len(plan.Fields))
	for _, f := range plan.Fields {
		fields = append(fields, loaders.FieldMapping{
			Source: f.Source,
			Column: f.Column,
			Type:   f.Type,
		})
	}

	maxRows := loaders.DefaultMaxRows
	if plan.MaxRows > 0 {
		maxRows = plan.MaxRows
	}

	var timeout time.Duration
	if plan.TimeoutSecs > 0 {
		timeout = time.Duration(plan.TimeoutSecs) * time.Second
	}

	opts := loaders.MCPFetchOpts{
		Server:          plan.Server,
		Tool:            plan.Tool,
		Args:            plan.Args,
		Fields:          fields,
		ItemsPath:       plan.ItemsPath,
		SingleItem:      plan.SingleItem,
		MaxRows:         maxRows,
		Timeout:         timeout,
		StagingDir:      sc.StagingDir(),
		Table:           tableName,
		RunID:           runID,
		ResponseFormat:  plan.ResponseFormat,
		ResponseAdapter: plan.ResponseAdapter,
	}

	// Reconstruct pagination config if present.
	var pagination *contract.PaginationConfig
	if plan.Pagination != nil {
		pagination = &contract.PaginationConfig{
			Mode:           plan.Pagination.Mode,
			PageSize:       plan.Pagination.PageSize,
			OffsetParam:    plan.Pagination.OffsetParam,
			LimitParam:     plan.Pagination.LimitParam,
			CursorParam:    plan.Pagination.CursorParam,
			NextCursorPath: plan.Pagination.NextCursorPath,
			TotalPath:      plan.Pagination.TotalPath,
		}

		// Apply resume state: adjust offset or cursor from where we left off.
		if ts.LastOffset > 0 && plan.Pagination.Mode == "offset" {
			if opts.Args == nil {
				opts.Args = make(map[string]any)
			}
			offsetParam := plan.Pagination.OffsetParam
			if offsetParam == "" {
				offsetParam = "offset"
			}
			opts.Args[offsetParam] = ts.LastOffset
		}
		if ts.LastCursor != "" && plan.Pagination.Mode == "cursor" {
			if opts.Args == nil {
				opts.Args = make(map[string]any)
			}
			cursorParam := plan.Pagination.CursorParam
			if cursorParam == "" {
				cursorParam = "cursor"
			}
			opts.Args[cursorParam] = ts.LastCursor
		}
	}

	// Retry loop (same as stageTable).
	var lastErr error
	retries := 0

	for retries <= recoveryMaxRetries {
		var loadResult *loaders.LoadResult
		var err error

		if pagination != nil {
			loadResult, err = fetcher.FetchPaginated(ctx, opts, pagination)
		} else {
			loadResult, err = fetcher.Fetch(ctx, opts)
		}

		if err == nil {
			// Success: mark table as staged.
			completedNow := time.Now()
			updated := &TableState{
				Name:               tableName,
				FetchMode:          ts.FetchMode,
				Required:           ts.Required,
				Status:             TableStatusStaged,
				ParquetPath:        loadResult.ParquetPath,
				RowCount:           loadResult.RowCount,
				PagesCompleted:     loadResult.PagesCompleted,
				Partial:            loadResult.Partial,
				CompletedAt:        &completedNow,
				ResumedFromRestart: true,
			}
			if err := store.UpdateTable(ctx, runID, tableName, updated); err != nil {
				log.Warn("failed to update recovered table to staged",
					"run_id", runID, "table", tableName, "error", err)
			}

			checkAndTransitionRecoveredRun(ctx, runID, store)
			return
		}

		lastErr = err
		retries++

		if retries > recoveryMaxRetries {
			break
		}

		// Exponential backoff.
		backoff := recoveryBaseBackoff * time.Duration(1<<(retries-1))
		log.Warn("recovery staging retry",
			"run_id", runID, "table", tableName,
			"retry", retries, "backoff", backoff, "error", err)

		// Update retry count in store.
		retryState := &TableState{
			Name:               tableName,
			FetchMode:          ts.FetchMode,
			Required:           ts.Required,
			Status:             TableStatusFetching,
			RetryCount:         retries,
			ResumedFromRestart: true,
			Error: &StructuredError{
				Message:   err.Error(),
				Category:  "transient",
				Retryable: true,
				Phase:     "staging",
			},
		}
		_ = store.UpdateTable(ctx, runID, tableName, retryState)

		time.Sleep(backoff)
	}

	// All retries exhausted — mark as failed.
	failedNow := time.Now()
	failedState := &TableState{
		Name:               tableName,
		FetchMode:          ts.FetchMode,
		Required:           ts.Required,
		Status:             TableStatusFailed,
		RetryCount:         retries,
		CompletedAt:        &failedNow,
		ResumedFromRestart: true,
		LastErrorAt:        &failedNow,
		Error: &StructuredError{
			Message:  fmt.Sprintf("recovery staging failed after %d retries: %v", retries, lastErr),
			Category: "permanent",
			Phase:    "staging",
		},
	}
	if err := store.UpdateTable(ctx, runID, tableName, failedState); err != nil {
		log.Warn("failed to update recovered table to failed",
			"run_id", runID, "table", tableName, "error", err)
	}

	// If required table failed → transition run to failed with error detail.
	if ts.Required {
		_ = store.FailRun(ctx, runID, &StructuredError{
			Message:  fmt.Sprintf("required table %q failed during recovery: %v", tableName, lastErr),
			Category: "permanent",
			Phase:    "staging",
		})
	} else {
		// Check if this was the last outstanding table.
		checkAndTransitionRecoveredRun(ctx, runID, store)
	}
}

// checkAndTransitionRecoveredRun checks if all auto-staging tables are done and
// transitions the run using the shared DeriveRunStatus logic.
func checkAndTransitionRecoveredRun(ctx context.Context, runID string, store StagingRunStore) {
	run, err := store.Get(ctx, runID)
	if err != nil || run == nil {
		return
	}

	nextStatus := DeriveRunStatus(run)
	if nextStatus == "" || nextStatus == run.Status {
		return // no transition needed
	}

	if nextStatus == RunStatusFailed {
		for _, ts := range run.Tables {
			if ts.Required && ts.Status == TableStatusFailed {
				errMsg := "required table failed"
				if ts.Error != nil {
					errMsg = ts.Error.Message
				}
				_ = store.FailRun(ctx, runID, &StructuredError{
					Message:  errMsg,
					Category: "permanent",
					Phase:    "staging",
				})
				return
			}
		}
	}

	_ = store.UpdateStatus(ctx, runID, nextStatus)
}

// failTable marks a table as failed with the given reason.
func failTable(ctx context.Context, store StagingRunStore, runID, tableName string, ts *TableState, reason string) {
	failedNow := time.Now()
	ts.Status = TableStatusFailed
	ts.CompletedAt = &failedNow
	ts.LastErrorAt = &failedNow
	ts.Error = &StructuredError{
		Message:  reason,
		Category: "permanent",
		Phase:    "staging",
	}
	_ = store.UpdateTable(ctx, runID, tableName, ts)
}
