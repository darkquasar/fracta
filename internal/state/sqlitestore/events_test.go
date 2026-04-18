package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darkquasar/fracta/internal/events"
)

func TestEmitEvent_SQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Emit a few events
	if err := store.EmitEvent(ctx, "agent-1", "job_created", "host_type=claude"); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
	if err := store.EmitEvent(ctx, "agent-1", "completed", "exit_code=0"); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
	if err := store.EmitEvent(ctx, "agent-2", "failed", "timeout"); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	// Query events back
	rows, err := store.db.QueryContext(ctx, `SELECT task, event, detail FROM agent_events ORDER BY id`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	type event struct {
		Task, Event, Detail string
	}
	var events []event
	for rows.Next() {
		var e event
		var detail sql.NullString
		if err := rows.Scan(&e.Task, &e.Event, &detail); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if detail.Valid {
			e.Detail = detail.String
		}
		events = append(events, e)
	}

	if len(events) != 3 {
		t.Fatalf("events count = %d, want 3", len(events))
	}

	if events[0].Task != "agent-1" || events[0].Event != "job_created" || events[0].Detail != "host_type=claude" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Task != "agent-1" || events[1].Event != "completed" {
		t.Errorf("event[1] = %+v", events[1])
	}
	if events[2].Task != "agent-2" || events[2].Event != "failed" || events[2].Detail != "timeout" {
		t.Errorf("event[2] = %+v", events[2])
	}
}

func TestEmitEvent_NilDetail(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.EmitEvent(ctx, "agent-1", "gateway_ready", ""); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}

	var detail sql.NullString
	err = store.db.QueryRowContext(ctx, `SELECT detail FROM agent_events WHERE task = 'agent-1'`).Scan(&detail)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Empty string detail should be stored (not NULL)
	if !detail.Valid || detail.String != "" {
		t.Errorf("detail = %v, want empty string", detail)
	}
}

func TestRecentEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert structured events via InsertEvent (the production path).
	for i, action := range []string{"heartbeat", "tool.started", "tool.completed", "message.completed"} {
		err := store.InsertEvent(ctx, events.InsertEventParams{
			EventID:   fmt.Sprintf("evt-%d", i),
			Task:      "agent-1",
			Event:     "agent_" + action,
			Component: "host_adapter",
			Category:  "agent_activity",
			Action:    action,
			Severity:  "info",
			Detail:    fmt.Sprintf("detail-%d", i),
			AttrsJSON: `{"runtime":"claude"}`,
		})
		if err != nil {
			t.Fatalf("InsertEvent[%d]: %v", i, err)
		}
	}

	// Also insert an event for a different task (should not appear).
	err = store.InsertEvent(ctx, events.InsertEventParams{
		EventID:   "evt-other",
		Task:      "agent-2",
		Event:     "agent_heartbeat",
		Component: "worker",
		Action:    "heartbeat",
		Severity:  "debug",
	})
	if err != nil {
		t.Fatalf("InsertEvent other: %v", err)
	}

	// RecentEvents with limit 3 should return the 3 most recent for agent-1 (newest first).
	got, err := store.RecentEvents(ctx, "agent-1", 3)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("RecentEvents count = %d, want 3", len(got))
	}

	// Newest first: message.completed, tool.completed, tool.started
	if got[0].Action != "message.completed" {
		t.Errorf("got[0].Action = %q, want %q", got[0].Action, "message.completed")
	}
	if got[1].Action != "tool.completed" {
		t.Errorf("got[1].Action = %q, want %q", got[1].Action, "tool.completed")
	}
	if got[2].Action != "tool.started" {
		t.Errorf("got[2].Action = %q, want %q", got[2].Action, "tool.started")
	}

	// Check attrs unmarshaling.
	if got[0].Attrs == nil || got[0].Attrs["runtime"] != "claude" {
		t.Errorf("got[0].Attrs = %v, want map with runtime=claude", got[0].Attrs)
	}

	// Check other fields.
	if got[0].ID != "evt-3" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "evt-3")
	}
	if got[0].Component != "host_adapter" {
		t.Errorf("got[0].Component = %q, want %q", got[0].Component, "host_adapter")
	}
}

func TestEventsSince(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert 5 events.
	for i := 0; i < 5; i++ {
		err := store.InsertEvent(ctx, events.InsertEventParams{
			EventID:   fmt.Sprintf("evt-%d", i),
			Task:      "agent-1",
			Event:     "agent_heartbeat",
			Component: "worker",
			Action:    "heartbeat",
			Severity:  "debug",
			Detail:    fmt.Sprintf("beat-%d", i),
		})
		if err != nil {
			t.Fatalf("InsertEvent[%d]: %v", i, err)
		}
	}

	// EventsSince("evt-1") should return events after evt-1 (i.e., evt-2,evt-3,evt-4), oldest first.
	got, err := store.EventsSince(ctx, "agent-1", "evt-1", 10)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("EventsSince count = %d, want 3", len(got))
	}

	// Oldest first ordering.
	if got[0].Detail != "beat-2" {
		t.Errorf("got[0].Detail = %q, want %q", got[0].Detail, "beat-2")
	}
	if got[1].Detail != "beat-3" {
		t.Errorf("got[1].Detail = %q, want %q", got[1].Detail, "beat-3")
	}
	if got[2].Detail != "beat-4" {
		t.Errorf("got[2].Detail = %q, want %q", got[2].Detail, "beat-4")
	}

	// EventsSince with a known event_id and limit should cap results.
	got, err = store.EventsSince(ctx, "agent-1", "evt-0", 2)
	if err != nil {
		t.Fatalf("EventsSince limit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("EventsSince limited count = %d, want 2", len(got))
	}

	// EventsSince with unknown event_id returns empty (not full history).
	got, err = store.EventsSince(ctx, "agent-1", "nonexistent", 10)
	if err != nil {
		t.Fatalf("EventsSince unknown cursor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("EventsSince unknown cursor count = %d, want 0", len(got))
	}
}

func TestRecentEvents_EmptyTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	got, err := store.RecentEvents(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatalf("RecentEvents on empty: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
}

func TestEventsSince_NoMatchingTask(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert event for agent-1.
	err = store.InsertEvent(ctx, events.InsertEventParams{
		EventID:   "evt-1",
		Task:      "agent-1",
		Event:     "agent_heartbeat",
		Component: "worker",
		Action:    "heartbeat",
		Severity:  "debug",
	})
	if err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	// Query for agent-2 should return nothing (nonexistent cursor for wrong task).
	got, err := store.EventsSince(ctx, "agent-2", "evt-1", 10)
	if err != nil {
		t.Fatalf("EventsSince: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice for unmatched task, got %v", got)
	}
}
