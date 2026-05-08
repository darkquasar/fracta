package cpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/fractalog"
)

// StagingSpawnClient decorates a ControlPlaneClient to transparently stage
// host-edge credentials before spawning. On Spawn(), it builds a credential
// plan at host_edge topology, executes it, stages any prepare_now artifacts
// via the CredentialStager, and attaches StagedCredentialRefs to the request
// before delegating to the wrapped client.
//
// All other methods delegate directly to the inner client.
type StagingSpawnClient struct {
	inner  ControlPlaneClient
	stager credentials.CredentialStager
	cfg    *config.Config
}

// NewStagingSpawnClient wraps a ControlPlaneClient with credential staging.
// If stager or cfg is nil, Spawn() delegates directly without staging.
func NewStagingSpawnClient(inner ControlPlaneClient, stager credentials.CredentialStager, cfg *config.Config) *StagingSpawnClient {
	return &StagingSpawnClient{inner: inner, stager: stager, cfg: cfg}
}

func (c *StagingSpawnClient) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResponse, error) {
	log := fractalog.Component("credentials")

	if c.stager == nil || c.cfg == nil {
		return c.inner.Spawn(ctx, req)
	}

	runtimeType := req.RuntimeType
	if runtimeType == "" && c.cfg.Agents.EffectiveDefaultRuntime() != "" {
		runtimeType = c.cfg.Agents.EffectiveDefaultRuntime()
	}
	if runtimeType == "" {
		runtimeType = "claude" // last resort fallback
	}

	credProfile, hostBinding, err := config.ResolveCredentialProfile(c.cfg, runtimeType)
	if err != nil {
		return nil, err
	}
	if credProfile == nil {
		return c.inner.Spawn(ctx, req)
	}

	// Build credential plan at host_edge topology.
	profile := credentials.FromConfigProfile(credProfile)
	binding := credentials.FromConfigBinding(hostBinding)

	plan, err := credentials.BuildCredentialPlan(
		c.cfg.EffectiveRuntimes()[runtimeType].AuthProfile,
		profile,
		binding,
		nil, // no host env at CLI layer
		credentials.PlanContext{
			Topology: credentials.TopologyHostEdge,
			Logger:   log,
		},
	)
	if err != nil {
		return nil, err
	}

	// Execute the plan to materialize host-edge sources.
	output, err := credentials.ExecuteCredentialPlan(ctx, plan, credentials.PlanContext{
		Topology: credentials.TopologyHostEdge,
		Logger:   log,
	})
	if err != nil {
		return nil, err
	}

	// Check if any prepare_now sources produced artifacts that must cross
	// the client→worker boundary (i.e., need staging).
	var needsStaging bool
	for _, src := range output.Plan.AuthOrigins {
		if src.Phase == credentials.PhasePrepareNow && src.MaterializedData != nil {
			needsStaging = true
			break
		}
	}

	if !needsStaging {
		return c.inner.Spawn(ctx, req)
	}

	// Staging is needed. Only queued batch dispatch can carry refs through the
	// queue payload to the worker. Require explicit queued dispatch and reject
	// stream mode (server gives queued precedence over stream, making the
	// combination silently wrong).
	if req.Mode == "stream" {
		return nil, fmt.Errorf(
			"remote spawn for host %q requires staged credentials which are incompatible with stream mode",
			runtimeType,
		)
	}
	if req.Dispatch != "queued" {
		return nil, fmt.Errorf(
			"remote spawn for host %q requires staged credentials; set dispatch=queued (got dispatch=%q)",
			runtimeType, req.Dispatch,
		)
	}

	// Stage prepare_now sources with materialized data via CP API.
	refs := make(map[string]string)
	for _, src := range output.Plan.AuthOrigins {
		if src.Phase == credentials.PhasePrepareNow && src.MaterializedData != nil {
			ref, stageErr := c.stager.Stage(ctx, src.Name, src.MaterializedData, src.AuthOrigin.Path, 5*time.Minute)
			if stageErr != nil {
				log.Warn("credentials.stage.fail",
					"source_name", src.Name,
					"error", stageErr.Error(),
				)
				continue
			}
			refs[src.Name] = ref
		}
	}

	if len(refs) > 0 {
		req.StagedCredentialRefs = refs
	}

	return c.inner.Spawn(ctx, req)
}

func (c *StagingSpawnClient) ListAgents(ctx context.Context, req ListAgentsRequest) (*ListAgentsResponse, error) {
	return c.inner.ListAgents(ctx, req)
}

func (c *StagingSpawnClient) GetAgent(ctx context.Context, req GetAgentRequest) (*GetAgentResponse, error) {
	return c.inner.GetAgent(ctx, req)
}

func (c *StagingSpawnClient) GetMission(ctx context.Context, req GetMissionRequest) (*GetMissionResponse, error) {
	return c.inner.GetMission(ctx, req)
}

func (c *StagingSpawnClient) Peek(ctx context.Context, req PeekRequest) (*PeekResponse, error) {
	return c.inner.Peek(ctx, req)
}

func (c *StagingSpawnClient) GetLogs(ctx context.Context, req GetLogsRequest) (*GetLogsResponse, error) {
	return c.inner.GetLogs(ctx, req)
}

func (c *StagingSpawnClient) Say(ctx context.Context, req SayRequest) (*SayResponse, error) {
	return c.inner.Say(ctx, req)
}

func (c *StagingSpawnClient) Kill(ctx context.Context, req KillRequest) (*KillResponse, error) {
	return c.inner.Kill(ctx, req)
}

func (c *StagingSpawnClient) Merge(ctx context.Context, req MergeRequest) (*MergeResponse, error) {
	return c.inner.Merge(ctx, req)
}

func (c *StagingSpawnClient) DryRunSpawn(ctx context.Context, req DryRunRequest) (*DryRunResponse, error) {
	return c.inner.DryRunSpawn(ctx, req)
}

func (c *StagingSpawnClient) CreateObjective(ctx context.Context, req CreateObjectiveRequest) (*CreateObjectiveResponse, error) {
	return c.inner.CreateObjective(ctx, req)
}

func (c *StagingSpawnClient) ListObjectives(ctx context.Context, req ListObjectivesRequest) (*ListObjectivesResponse, error) {
	return c.inner.ListObjectives(ctx, req)
}

func (c *StagingSpawnClient) GetObjective(ctx context.Context, req GetObjectiveRequest) (*GetObjectiveResponse, error) {
	return c.inner.GetObjective(ctx, req)
}

func (c *StagingSpawnClient) UnfreezeObjective(ctx context.Context, req UnfreezeObjectiveRequest) (*UnfreezeObjectiveResponse, error) {
	return c.inner.UnfreezeObjective(ctx, req)
}

func (c *StagingSpawnClient) IngestEvents(ctx context.Context, req IngestEventsRequest, task string) (*IngestEventsResponse, error) {
	return c.inner.IngestEvents(ctx, req, task)
}

func (c *StagingSpawnClient) QueryEvents(ctx context.Context, req EventsQueryRequest) (*EventsQueryResponse, error) {
	return c.inner.QueryEvents(ctx, req)
}

func (c *StagingSpawnClient) GraphQuery(ctx context.Context, req GraphQueryRequest) (*GraphQueryResponse, error) {
	return c.inner.GraphQuery(ctx, req)
}

func (c *StagingSpawnClient) GraphUpdate(ctx context.Context, req GraphUpdateRequest) (*GraphUpdateResponse, error) {
	return c.inner.GraphUpdate(ctx, req)
}

func (c *StagingSpawnClient) GraphSchema(ctx context.Context, req GraphSchemaRequest) (*GraphSchemaResponse, error) {
	return c.inner.GraphSchema(ctx, req)
}

func (c *StagingSpawnClient) GraphPath(ctx context.Context, req GraphPathRequest) (*GraphPathResponse, error) {
	return c.inner.GraphPath(ctx, req)
}

func (c *StagingSpawnClient) GraphNeighbors(ctx context.Context, req GraphNeighborsRequest) (*GraphNeighborsResponse, error) {
	return c.inner.GraphNeighbors(ctx, req)
}

// Compile-time interface satisfaction check.
var _ ControlPlaneClient = (*StagingSpawnClient)(nil)
