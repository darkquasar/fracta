package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/fractalog"
)

// Strategy status constants.
const (
	StatusExploratory = "exploratory"
	StatusValidated   = "validated"
	StatusPromoted    = "promoted"
	StatusDeprecated  = "deprecated"
	StatusRetired     = "retired"
)

// strategyVersionInfo holds the fields returned by the status resolution query.
type strategyVersionInfo struct {
	Name        string
	Version     string
	Status      string
	RunCount    int
	Reliability float64
	Composite   float64
	LastRunAt   string // ISO 8601 or empty
}

// resolveEffectiveStatus queries the StrategyVersion node and applies lazy
// status transitions. It returns the effective status after any auto-transitions.
//
// Auto-transitions:
//   - exploratory -> validated: run_count >= 5 AND reliability >= 0.8
//   - promoted -> validated (demote): reliability < 0.7
//   - deprecated -> retired: 30 days with no runs
//
// validated -> promoted requires explicit admin action (strategy_promote tool)
// unless autoPromote is true AND thresholds are met (run_count >= 20, reliability >= 0.95, composite >= 0.7).
func resolveEffectiveStatus(ctx context.Context, gc graph.GraphClient, name, version string, autoPromote bool) (string, error) {
	if version == "" {
		version = "1"
	}

	rows, err := gc.Query(ctx,
		"MATCH (v:StrategyVersion {name: $name, version: $version}) "+
			"OPTIONAL MATCH (v)-[:HAS_RUN]->(r:StrategyRun) "+
			"WITH v, count(r) AS run_count, max(r.started_at) AS last_run "+
			"RETURN v.status AS status, v.total_runs AS total_runs, "+
			"v.reliability AS reliability, v.composite_score AS composite_score, last_run",
		map[string]any{"name": name, "version": version},
	)
	if err != nil {
		return "", fmt.Errorf("querying strategy version: %w", err)
	}
	if len(rows) == 0 {
		return StatusExploratory, nil
	}

	row := rows[0]
	status := stringVal(row, "status", StatusExploratory)
	runCount := intVal(row, "total_runs", 0)
	reliability := floatVal(row, "reliability", 0)
	composite := floatVal(row, "composite_score", 0)
	lastRun := stringVal(row, "last_run", "")

	newStatus := status

	switch status {
	case StatusExploratory:
		if runCount >= 5 && reliability >= 0.8 {
			newStatus = StatusValidated
		}
	case StatusValidated:
		if autoPromote && runCount >= 20 && reliability >= 0.95 && composite >= 0.7 {
			newStatus = StatusPromoted
		}
	case StatusPromoted:
		if reliability < 0.7 {
			newStatus = StatusValidated // auto-demote
		}
	case StatusDeprecated:
		if lastRun != "" {
			if t, err := time.Parse(time.RFC3339, lastRun); err == nil {
				if time.Since(t) > 30*24*time.Hour {
					newStatus = StatusRetired
				}
			}
		}
	}

	if newStatus != status {
		// Update both Strategy.status and StrategyVersion.status to keep them in sync.
		// Graph consumers may read either node; stale Strategy.status would cause drift.
		if uerr := gc.Update(ctx,
			"MATCH (s:Strategy {name: $name})-[:HAS_VERSION]->(v:StrategyVersion {name: $name, version: $version}) "+
				"SET v.status = $status, s.status = $status, s.version = $version",
			map[string]any{"name": name, "version": version, "status": newStatus},
		); uerr != nil {
			fractalog.Component("strategy").Warn("failed to update strategy status", "name", name, "version", version, "error", uerr)
			return status, nil // return old status on write failure
		}
	}

	return newStatus, nil
}

// Helper functions for extracting typed values from graph records.

func stringVal(row graph.Record, key, fallback string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func intVal(row graph.Record, key string, fallback int) int {
	v, ok := row[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return fallback
}

func floatVal(row graph.Record, key string, fallback float64) float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return fallback
}
