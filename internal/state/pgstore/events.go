package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/state"
)

// Deprecated: EmitEvent is replaced by events.StoreSink + InsertEvent.
// Retained for backward compatibility with existing tests.
func (s *PostgresStore) EmitEvent(ctx context.Context, task, event, detail string) error {
	log := fractalog.Component("events")
	log.Info("agent event", "task", task, "event", event, "detail", detail)

	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_events (task, event, detail) VALUES ($1, $2, $3)`,
		task, event, detail)
	if err != nil {
		return fmt.Errorf("pgstore: emit event %s/%s: %w", task, event, err)
	}
	return nil
}

// InsertEvent writes a structured event to the agent_events table.
// Satisfies events.EventInserter for StoreSink.
func (s *PostgresStore) InsertEvent(ctx context.Context, p events.InsertEventParams) error {
	eventTime := p.Time
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_events
			(event_id, task, event, component, category, resource, action, outcome,
			 severity, mission_id, objective_id, detail, attrs_json, timestamp)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		p.EventID, p.Task, p.Event, p.Component, p.Category, p.Resource,
		p.Action, p.Outcome, p.Severity, p.MissionID, p.ObjectiveID,
		p.Detail, nilIfEmpty(p.AttrsJSON), eventTime)
	if err != nil {
		return fmt.Errorf("pgstore: insert event: %w", err)
	}
	return nil
}

// nilIfEmpty returns nil for empty strings (so JSONB columns get NULL, not ”).
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RecentEvents returns the most recent events for a task, ordered newest-first.
func (s *PostgresStore) RecentEvents(ctx context.Context, task string, limit int) ([]events.Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, event_id, task, component, category, resource, action, outcome,
		        severity, mission_id, objective_id, detail, attrs_json, timestamp
		 FROM agent_events
		 WHERE task = $1
		 ORDER BY id DESC
		 LIMIT $2`, task, limit)
	if err != nil {
		return nil, fmt.Errorf("pgstore: recent events: %w", err)
	}
	defer rows.Close()

	var result []events.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// EventsSince returns events for a task after the event with the given UUID,
// ordered oldest-first (ascending), up to limit rows.
// Resolves the UUID to a DB row ID via subquery.
func (s *PostgresStore) EventsSince(ctx context.Context, task string, sinceEventID string, limit int) ([]events.Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, event_id, task, component, category, resource, action, outcome,
		        severity, mission_id, objective_id, detail, attrs_json, timestamp
		 FROM agent_events
		 WHERE task = $1 AND id > (SELECT id FROM agent_events WHERE event_id = $2 LIMIT 1)
		 ORDER BY id ASC
		 LIMIT $3`, task, sinceEventID, limit)
	if err != nil {
		return nil, fmt.Errorf("pgstore: events since: %w", err)
	}
	defer rows.Close()

	var result []events.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// pgRow is a minimal interface for scanning rows from pgx.
type pgRow interface {
	Scan(dest ...any) error
}

// scanEvent reconstructs an events.Event from a database row.
func scanEvent(row pgRow) (events.Event, error) {
	var (
		dbID        int64
		eventID     *string
		task        string
		component   *string
		category    *string
		resource    *string
		action      *string
		outcome     *string
		severity    string
		missionID   int64
		objectiveID string
		detail      *string
		attrsJSON   *string
		timestamp   time.Time
	)

	err := row.Scan(&dbID, &eventID, &task, &component, &category, &resource,
		&action, &outcome, &severity, &missionID, &objectiveID, &detail,
		&attrsJSON, &timestamp)
	if err != nil {
		return events.Event{}, fmt.Errorf("pgstore: scan event: %w", err)
	}

	e := events.Event{
		Time:        timestamp,
		Task:        task,
		Severity:    severity,
		MissionID:   missionID,
		ObjectiveID: objectiveID,
	}
	if eventID != nil {
		e.ID = *eventID
	}
	if component != nil {
		e.Component = *component
	}
	if category != nil {
		e.Category = *category
	}
	if resource != nil {
		e.Resource = *resource
	}
	if action != nil {
		e.Action = *action
	}
	if outcome != nil {
		e.Outcome = *outcome
	}
	if detail != nil {
		e.Detail = *detail
	}
	if attrsJSON != nil && *attrsJSON != "" {
		var attrs map[string]string
		if err := json.Unmarshal([]byte(*attrsJSON), &attrs); err == nil {
			e.Attrs = attrs
		}
	}

	return e, nil
}

// Verify interface compliance at compile time.
var _ events.EventInserter = (*PostgresStore)(nil)
var _ state.EventReader = (*PostgresStore)(nil)
