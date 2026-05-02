package events

import "strings"

// LegacyAlias derives a flat event name from structured fields for backward
// compatibility with the legacy `event` column in agent_events. This is the
// single authoritative mapper — no emitter or sink should hand-roll alias
// strings independently.
func LegacyAlias(e Event) string {
	// Agent activity events from host_adapter and worker components.
	if e.Category == "agent_activity" {
		switch e.Action {
		case "lifecycle.started":
			return "agent_lifecycle_started"
		case "lifecycle.completed":
			return "agent_lifecycle_completed"
		case "lifecycle.failed":
			return "agent_lifecycle_failed"
		case "heartbeat":
			return "agent_heartbeat"
		case "message.delta":
			return "agent_message_delta"
		case "message.completed":
			return "agent_message_completed"
		case "tool.started":
			return "agent_tool_started"
		case "tool.completed":
			return "agent_tool_completed"
		case "command.started":
			return "agent_command_started"
		case "command.completed":
			return "agent_command_completed"
		case "file.changed":
			return "agent_file_changed"
		case "turn.started":
			return "agent_turn_started"
		case "turn.completed":
			return "agent_turn_completed"
		default:
			return "agent_" + strings.ReplaceAll(e.Action, ".", "_")
		}
	}

	// Gateway status changes carry the target status in Attrs.
	if e.Component == "gateway" && e.Action == "status_change" {
		if s := e.Attrs["status"]; s != "" {
			return "gateway_" + s
		}
		return "gateway_status_change"
	}

	// MCP client backend events.
	if e.Component == "mcpclient" {
		switch {
		case e.Action == "connect_attempt" && e.Outcome == "failure":
			return "backend_connect_failed"
		case e.Action == "connect_attempt" && e.Outcome == "timeout":
			return "backend_connect_timeout"
		case e.Action == "tool_refresh":
			return "backend_tool_refresh"
		}
	}

	// Orchestrator auth events.
	if e.Component == "orchestrator" && e.Category == "auth" && e.Action == "seed" {
		if e.Outcome == "success" {
			return "auth_seed_ok"
		}
		return "auth_seed_failed"
	}

	// Orchestrator agent lifecycle.
	if e.Component == "orchestrator" {
		switch e.Action {
		case "create":
			return "job_created"
		case "complete":
			return "completed"
		case "fail":
			return "failed"
		}
	}

	// Runtime K8s events.
	if e.Component == "runtime.k8s" {
		switch e.Action {
		case "job_create":
			return "k8s_job_created"
		case "pod_schedule":
			return "k8s_pod_scheduled"
		}
	}

	// Strategy events.
	if e.Component == "strategy" && e.Action == "execute" {
		if e.Outcome == "success" {
			return "strategy_executed"
		}
		return "strategy_failed"
	}

	// Worker events.
	if e.Component == "worker" {
		switch {
		case e.Category == "queue" && e.Action == "claim":
			return "queue_claimed"
		case e.Category == "queue" && e.Action == "fail":
			return "queue_failed"
		case e.Category == "objective" && e.Action == "resolve":
			return "objective_resolved"
		}
	}

	// Fallback: component_action.
	if e.Component != "" && e.Action != "" {
		return e.Component + "_" + e.Action
	}
	return "unknown"
}
