package ctxkeys

import "context"

type agentTaskKeyType struct{}

var agentTaskKey = agentTaskKeyType{}

// WithAgentTask returns a new context carrying the agent task identity.
func WithAgentTask(ctx context.Context, task string) context.Context {
	return context.WithValue(ctx, agentTaskKey, task)
}

// AgentTask extracts the agent task from ctx. Returns ("", false) if not set.
func AgentTask(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(agentTaskKey).(string)
	return v, ok && v != ""
}
