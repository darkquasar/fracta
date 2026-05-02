package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EventInserter is the minimal interface that StoreSink needs from a state
// store. Both PostgresStore and SQLiteStore can satisfy this with a thin
// adapter — the events package never imports the full store packages.
type EventInserter interface {
	InsertEvent(ctx context.Context, p InsertEventParams) error
}

// InsertEventParams carries all columns for an agent_events row.
type InsertEventParams struct {
	EventID     string
	Time        time.Time // event occurrence time (not insertion time)
	Task        string
	Event       string // legacy flat alias
	Component   string
	Category    string
	Resource    string
	Action      string
	Outcome     string
	Severity    string
	MissionID   int64
	ObjectiveID string
	Detail      string
	AttrsJSON   string // JSON-encoded Attrs map
}

// StoreSink persists events to the agent_events table via an EventInserter.
// It is persistence-only and must NOT emit log lines.
type StoreSink struct {
	inserter EventInserter
}

// NewStoreSink creates a StoreSink backed by the given inserter.
func NewStoreSink(inserter EventInserter) *StoreSink {
	return &StoreSink{inserter: inserter}
}

// Handle persists the event with both structured fields and a legacy alias.
func (s *StoreSink) Handle(ctx context.Context, e Event) error {
	attrsJSON := ""
	if len(e.Attrs) > 0 {
		b, err := json.Marshal(e.Attrs)
		if err != nil {
			return fmt.Errorf("storesink: marshal attrs: %w", err)
		}
		attrsJSON = string(b)
	}

	return s.inserter.InsertEvent(ctx, InsertEventParams{
		EventID:     e.ID,
		Time:        e.Time,
		Task:        e.Task,
		Event:       LegacyAlias(e),
		Component:   e.Component,
		Category:    e.Category,
		Resource:    e.Resource,
		Action:      e.Action,
		Outcome:     e.Outcome,
		Severity:    e.Severity,
		MissionID:   e.MissionID,
		ObjectiveID: e.ObjectiveID,
		Detail:      e.Detail,
		AttrsJSON:   attrsJSON,
	})
}

// String returns the sink name for logging.
func (s *StoreSink) String() string { return "StoreSink" }
