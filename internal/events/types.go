// Package events provides the internal event bus for fracta.
// Components emit structured events; pluggable sinks decide how to
// persist, log, or forward them.
package events

import (
	"time"

	"github.com/google/uuid"
)

// Event is the canonical event model for the fracta event bus.
// Required fields: Time, Component, Action.
type Event struct {
	ID          string            // random UUID, generated at emit time
	Time        time.Time         // when the event occurred
	Component   string            // emitting subsystem: orchestrator, runtime.k8s, gateway, reconciler, mcpclient, worker
	Category    string            // event family: agent, auth, backend, gateway, queue, objective, strategy
	Resource    string            // typed identifier: task:research-foo, mcp_server:vendor, objective:obj-123
	Action      string            // what happened: create, complete, fail, seed, connect_attempt, status_change, tool_refresh, resolve
	Outcome     string            // result: success, failure, partial, unknown, timeout, rejected, skipped
	Severity    string            // operator severity: debug, info, warn, error

	Task        string            // canonical fracta agent identity (when event relates to an agent)
	MissionID   int64             // optional mission correlation
	ObjectiveID string            // optional objective correlation

	Detail      string            // short human-readable explanation
	Attrs       map[string]string // flexible metadata bag
}

// Info creates an event with severity "info". ID and Time are pre-filled.
func Info(component, action string) Event {
	return Event{
		ID:        uuid.NewString(),
		Time:      time.Now(),
		Severity:  "info",
		Component: component,
		Action:    action,
	}
}

// Warn creates an event with severity "warn". ID, Time, and Detail are pre-filled.
func Warn(component, action, detail string) Event {
	return Event{
		ID:        uuid.NewString(),
		Time:      time.Now(),
		Severity:  "warn",
		Component: component,
		Action:    action,
		Detail:    detail,
	}
}

// Error creates an event with severity "error". ID, Time, and Detail are pre-filled.
func Error(component, action, detail string) Event {
	return Event{
		ID:        uuid.NewString(),
		Time:      time.Now(),
		Severity:  "error",
		Component: component,
		Action:    action,
		Detail:    detail,
	}
}
