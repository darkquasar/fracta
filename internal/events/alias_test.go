package events

import "testing"

func TestLegacyAlias(t *testing.T) {
	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "gateway_ready",
			event: Event{
				Component: "gateway",
				Action:    "status_change",
				Attrs:     map[string]string{"status": "ready"},
			},
			want: "gateway_ready",
		},
		{
			name: "gateway_status_change_no_status",
			event: Event{
				Component: "gateway",
				Action:    "status_change",
			},
			want: "gateway_status_change",
		},
		{
			name: "backend_connect_failed",
			event: Event{
				Component: "mcpclient",
				Action:    "connect_attempt",
				Outcome:   "failure",
			},
			want: "backend_connect_failed",
		},
		{
			name: "backend_connect_timeout",
			event: Event{
				Component: "mcpclient",
				Action:    "connect_attempt",
				Outcome:   "timeout",
			},
			want: "backend_connect_timeout",
		},
		{
			name: "job_created",
			event: Event{
				Component: "orchestrator",
				Action:    "create",
			},
			want: "job_created",
		},
		{
			name: "auth_seed_ok",
			event: Event{
				Component: "orchestrator",
				Category:  "auth",
				Action:    "seed",
				Outcome:   "success",
			},
			want: "auth_seed_ok",
		},
		{
			name: "auth_seed_failed",
			event: Event{
				Component: "orchestrator",
				Category:  "auth",
				Action:    "seed",
				Outcome:   "failure",
			},
			want: "auth_seed_failed",
		},
		{
			name: "completed",
			event: Event{
				Component: "orchestrator",
				Action:    "complete",
				Outcome:   "success",
			},
			want: "completed",
		},
		{
			name: "failed",
			event: Event{
				Component: "orchestrator",
				Action:    "fail",
				Outcome:   "failure",
			},
			want: "failed",
		},
		{
			name: "k8s_job_created",
			event: Event{
				Component: "runtime.k8s",
				Action:    "job_create",
			},
			want: "k8s_job_created",
		},
		{
			name: "strategy_executed",
			event: Event{
				Component: "strategy",
				Action:    "execute",
				Outcome:   "success",
			},
			want: "strategy_executed",
		},
		// Agent activity events (host_adapter component).
		{
			name: "agent_lifecycle_started",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "lifecycle.started",
			},
			want: "agent_lifecycle_started",
		},
		{
			name: "agent_lifecycle_completed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "lifecycle.completed",
			},
			want: "agent_lifecycle_completed",
		},
		{
			name: "agent_lifecycle_failed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "lifecycle.failed",
			},
			want: "agent_lifecycle_failed",
		},
		{
			name: "agent_tool_started",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "tool.started",
			},
			want: "agent_tool_started",
		},
		{
			name: "agent_tool_completed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "tool.completed",
			},
			want: "agent_tool_completed",
		},
		{
			name: "agent_message_delta",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "message.delta",
			},
			want: "agent_message_delta",
		},
		{
			name: "agent_message_completed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "message.completed",
			},
			want: "agent_message_completed",
		},
		{
			name: "agent_command_started",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "command.started",
			},
			want: "agent_command_started",
		},
		{
			name: "agent_command_completed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "command.completed",
			},
			want: "agent_command_completed",
		},
		{
			name: "agent_file_changed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "file.changed",
			},
			want: "agent_file_changed",
		},
		{
			name: "agent_turn_started",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "turn.started",
			},
			want: "agent_turn_started",
		},
		{
			name: "agent_turn_completed",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "turn.completed",
			},
			want: "agent_turn_completed",
		},
		// Agent activity events (worker component — heartbeat).
		{
			name: "agent_heartbeat",
			event: Event{
				Component: "worker",
				Category:  "agent_activity",
				Action:    "heartbeat",
			},
			want: "agent_heartbeat",
		},
		// Agent activity fallback for unknown actions.
		{
			name: "agent_activity_unknown_action",
			event: Event{
				Component: "host_adapter",
				Category:  "agent_activity",
				Action:    "custom.event",
			},
			want: "agent_custom_event",
		},
		{
			name: "fallback_component_action",
			event: Event{
				Component: "worker",
				Category:  "misc",
				Action:    "ping",
			},
			want: "worker_ping",
		},
		{
			name:  "unknown_empty",
			event: Event{},
			want:  "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LegacyAlias(tt.event)
			if got != tt.want {
				t.Errorf("LegacyAlias() = %q, want %q", got, tt.want)
			}
		})
	}
}
