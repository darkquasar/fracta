package events

import (
	"context"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

// SnapshotSink implements Sink and projects events into a SnapshotStore.
// It updates the per-agent snapshot based on the event's action and attributes.
type SnapshotSink struct {
	store             *SnapshotStore
	heartbeatInterval time.Duration
}

// NewSnapshotSink creates a SnapshotSink that writes to the given store.
// heartbeatInterval is used to determine the unresponsive threshold (3x interval).
// If heartbeatInterval is 0, a default of 15s is used.
func NewSnapshotSink(store *SnapshotStore, heartbeatInterval time.Duration) *SnapshotSink {
	if heartbeatInterval == 0 {
		heartbeatInterval = 15 * time.Second
	}
	return &SnapshotSink{
		store:             store,
		heartbeatInterval: heartbeatInterval,
	}
}

// Handle projects the event into the appropriate agent snapshot.
func (s *SnapshotSink) Handle(_ context.Context, e Event) error {
	if e.Task == "" {
		return nil
	}

	// Terminal events should only update existing snapshots, not create new ones.
	// This prevents zombie snapshots for agents that were already cleaned up.
	isTerminal := e.Action == "lifecycle.completed" || e.Action == "lifecycle.failed" || e.Action == "lifecycle.stopped"

	projectFn := func(snap *AgentSnapshot) {
		snap.LastEventAt = e.Time

		// Set correlation fields if present.
		if e.MissionID != 0 {
			snap.MissionID = e.MissionID
		}
		if e.ObjectiveID != "" {
			snap.ObjectiveID = e.ObjectiveID
		}

		// Project based on action.
		switch e.Action {
		case "heartbeat":
			s.projectHeartbeat(snap, e)
		case "tool.started":
			snap.CurrentTool = attrOr(e, "tool_name", "")
			snap.CurrentPhase = "tool_use"
		case "tool.completed":
			snap.CurrentTool = ""
			snap.CurrentPhase = "executing"
		case "lifecycle.started":
			snap.Status = model.StatusRunning
			snap.CurrentPhase = "executing"
			if snap.StartedAt.IsZero() {
				snap.StartedAt = e.Time
			}
			if b := e.Attrs["backend"]; b != "" {
				snap.Backend = b
			}
			if p := e.Attrs["pod_name"]; p != "" {
				snap.PodName = p
			}
		case "lifecycle.completed":
			snap.Status = model.StatusCompleted
			snap.CurrentPhase = "done"
			snap.CurrentTool = ""
			snap.terminalAt = e.Time
		case "lifecycle.failed":
			snap.Status = model.StatusFailed
			snap.CurrentPhase = "done"
			snap.CurrentTool = ""
			snap.terminalAt = e.Time
		case "lifecycle.stopped":
			snap.Status = model.StatusStopped
			snap.CurrentPhase = "done"
			snap.CurrentTool = ""
			snap.terminalAt = e.Time
		case "message.delta", "message.completed":
			s.projectMessage(snap, e)
		case "command.started":
			snap.CurrentPhase = "executing"
		case "turn.started":
			snap.CurrentPhase = "thinking"
		case "turn.completed":
			snap.CurrentPhase = "executing"
		}
	}

	if isTerminal {
		s.store.UpdateIfExists(e.Task, projectFn)
	} else {
		s.store.Update(e.Task, projectFn)
	}

	return nil
}

// projectHeartbeat updates snapshot fields from a heartbeat event.
// Heartbeats can SET tool/phase when they carry those attrs, but do NOT clear
// CurrentTool when absent — adapter events (tool.started/completed) are authoritative.
func (s *SnapshotSink) projectHeartbeat(snap *AgentSnapshot, e Event) {
	snap.LastHeartbeatAt = e.Time
	if phase := e.Attrs["phase"]; phase != "" {
		// Only overwrite phase when current phase is coarse (heartbeat-level).
		// Don't clobber adapter-set phases like "tool_use" or "thinking".
		switch snap.CurrentPhase {
		case "", "executing", "idle":
			snap.CurrentPhase = phase
		}
	}
	// Only update tool when heartbeat explicitly carries one. Don't clear on absence —
	// adapter events (tool.started/completed) manage the tool lifecycle precisely.
	if tool := e.Attrs["tool"]; tool != "" {
		snap.CurrentTool = tool
	}
}

// projectMessage updates LastMessageExcerpt from message events.
func (s *SnapshotSink) projectMessage(snap *AgentSnapshot, e Event) {
	text := e.Attrs["text_preview"]
	if text == "" {
		text = e.Detail
	}
	if text != "" {
		// Cap at 256 characters.
		if len(text) > 256 {
			text = text[:256]
		}
		snap.LastMessageExcerpt = text
	}
}

// CheckUnresponsive checks a specific agent and marks it unresponsive if
// no heartbeat has been received within 3x the heartbeat interval.
// now is the current time. Returns true if the agent was marked unresponsive.
func (s *SnapshotSink) CheckUnresponsive(task string, now time.Time) bool {
	threshold := 3 * s.heartbeatInterval
	marked := false

	s.store.Update(task, func(snap *AgentSnapshot) {
		if snap.IsTerminal() {
			return
		}
		if snap.LastHeartbeatAt.IsZero() {
			return
		}
		if now.Sub(snap.LastHeartbeatAt) > threshold {
			if snap.CurrentPhase != "unresponsive" {
				snap.CurrentPhase = "unresponsive"
				marked = true
			}
		}
	})

	return marked
}

// CheckAllUnresponsive checks all agents and marks any as unresponsive if
// no heartbeat has been received within 3x the heartbeat interval.
func (s *SnapshotSink) CheckAllUnresponsive(now time.Time) int {
	threshold := 3 * s.heartbeatInterval
	count := 0

	all := s.store.All()
	for task, snap := range all {
		if snap.IsTerminal() {
			continue
		}
		if snap.LastHeartbeatAt.IsZero() {
			continue
		}
		if now.Sub(snap.LastHeartbeatAt) > threshold {
			s.store.Update(task, func(s *AgentSnapshot) {
				if s.CurrentPhase != "unresponsive" && !s.IsTerminal() {
					s.CurrentPhase = "unresponsive"
					count++
				}
			})
		}
	}

	return count
}

// String returns the sink name for logging.
func (s *SnapshotSink) String() string { return "SnapshotSink" }

// attrOr returns the value of a key in the event attrs, or a default.
func attrOr(e Event, key, def string) string {
	if v, ok := e.Attrs[key]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}
