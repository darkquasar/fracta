package mcpserver

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/state"
)

// ObjectiveContextResolver resolves the objective context for an agent.
// Gateway mode queries the store per-request; agent mode returns baked-in context.
type ObjectiveContextResolver interface {
	Resolve(ctx context.Context, task string) (agentTask, objectiveID string, missionID int64, err error)
}

// StoreResolver queries the agent's objective context from the state store.
// Used by GatewayServer where multiple agents with different objectives
// share one server instance.
type StoreResolver struct {
	Store state.Store
}

func (r *StoreResolver) Resolve(ctx context.Context, task string) (string, string, int64, error) {
	agent, err := r.Store.FindAgent(ctx, task)
	if err != nil {
		return "", "", 0, fmt.Errorf("looking up agent %q: %w", task, err)
	}
	if agent == nil {
		return "", "", 0, fmt.Errorf("agent %q not found", task)
	}
	if agent.ObjectiveID == "" {
		return "", "", 0, fmt.Errorf("agent %q has no objective context", task)
	}
	return agent.Task, agent.ObjectiveID, agent.MissionID, nil
}

// StaticResolver returns fixed objective context baked in at construction time.
// Used by AgentServer in stdio mode where each server serves exactly one agent.
type StaticResolver struct {
	AgentTask   string
	ObjectiveID string
	MissionID   int64
}

func (r *StaticResolver) Resolve(_ context.Context, _ string) (string, string, int64, error) {
	return r.AgentTask, r.ObjectiveID, r.MissionID, nil
}
