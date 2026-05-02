package orchestrator

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"gopkg.in/yaml.v3"
)

// ExecutionSpec is the single source of truth for a fully-resolved spawn.
// All downstream paths (queue, worker, host adapter) consume this.
// Constructed via NewExecutionSpec (from ResolvedSpawn) or
// ExecutionSpecFromPayload (from deserialized MissionPayload in the worker).
type ExecutionSpec struct {
	Identity    SpecIdentity     `json:"identity"`
	Resolution  SpecResolution   `json:"resolution"`
	Topology    SpecTopology     `json:"topology"`
	Credentials *SpecCredentials `json:"credentials,omitempty"`
}

// SpecIdentity identifies who this agent is within the mission/objective system.
type SpecIdentity struct {
	Task        string `json:"task"`
	Contract    string `json:"contract"`
	BaseBranch  string `json:"base_branch"`
	ObjectiveID string `json:"objective_id,omitempty"`
	MissionID   int64  `json:"mission_id,omitempty"`
}

// SpecResolution captures the resolved runtime/model/tools decision.
type SpecResolution struct {
	RuntimeType  string   `json:"host_type"` // JSON tag kept for wire compat
	Model        string   `json:"model"`
	Mode         string   `json:"mode"`
	AllowedTools []string `json:"allowed_tools"`
}

// SpecTopology describes where the agent connects to services.
type SpecTopology struct {
	MCPServers  json.RawMessage `json:"mcp_servers,omitempty"`
	Backend     string          `json:"backend"`
	ConfigPath  string          `json:"config_path,omitempty"`
	GraphAddr   string          `json:"graph_addr,omitempty"`
	StrategyDir string          `json:"strategy_dir,omitempty"`
	GatewayURL  string          `json:"gateway_url,omitempty"`
	ConfigHash  string          `json:"config_hash,omitempty"`
}

// SpecCredentials holds auth artifacts for queue transport.
type SpecCredentials struct {
	StagedCredentialRefs map[string]string `json:"staged_credential_refs,omitempty"`
}

// SpawnArtifacts holds per-spawn runtime artifacts that are NOT part of
// the serializable ExecutionSpec. These are produced at spawn time by
// host config resolution, credential plan execution, and workspace writing.
type SpawnArtifacts struct {
	HostEnv             []runtime.EnvEntry
	Image               string
	ConfigSnapshot      string
	WorkspaceFiles      []runtime.WorkspaceArtifact
	AuthSecretData      map[string][]byte
	AuthSecretMountPath string
}

// NewExecutionSpec builds an ExecutionSpec from a ResolvedSpawn and caller parameters.
func NewExecutionSpec(resolved *ResolvedSpawn, task, contract, objectiveID string, orch *Orchestrator) ExecutionSpec {
	var mcpJSON json.RawMessage
	if orch.MCPServers.Servers != nil {
		mcpJSON, _ = json.Marshal(orch.MCPServers)
	}

	var gatewayURL string
	if orch.Config != nil {
		gatewayURL = orch.Config.Gateway.URL
	}

	spec := ExecutionSpec{
		Identity: SpecIdentity{
			Task:        task,
			Contract:    contract,
			BaseBranch:  resolved.BaseBranch,
			ObjectiveID: objectiveID,
		},
		Resolution: SpecResolution{
			RuntimeType:  resolved.RuntimeType,
			Model:        resolved.Model,
			Mode:         resolved.Mode,
			AllowedTools: resolved.AllowedTools,
		},
		Topology: SpecTopology{
			MCPServers:  mcpJSON,
			Backend:     orch.RuntimeBackend,
			ConfigPath:  resolved.ConfigPath,
			GraphAddr:   resolved.GraphAddr,
			StrategyDir: resolved.StrategyDir,
			GatewayURL:  gatewayURL,
			ConfigHash:  ComputeConfigHash(orch.Config),
		},
	}

	return spec
}

// ChildSpec creates a child ExecutionSpec from a parent, inheriting all Resolution
// and Topology fields. Only Identity is overridden with the child's task/contract/objectiveID.
// This fixes the GatewayURL bug -- the child inherits all topology from the parent.
func ChildSpec(parent ExecutionSpec, task, contract, objectiveID string) ExecutionSpec {
	child := parent // struct copy -- inherits Resolution + Topology + Credentials
	child.Identity = SpecIdentity{
		Task:        task,
		Contract:    contract,
		BaseBranch:  parent.Identity.BaseBranch,
		ObjectiveID: objectiveID,
		// MissionID set after Enqueue
	}
	return child
}

// ToMissionPayload converts an ExecutionSpec to a queue.MissionPayload for serialization.
func (es ExecutionSpec) ToMissionPayload() queue.MissionPayload {
	payload := queue.MissionPayload{
		Task:         es.Identity.Task,
		Contract:     es.Identity.Contract,
		BaseBranch:   es.Identity.BaseBranch,
		Model:        es.Resolution.Model,
		Mode:         es.Resolution.Mode,
		RuntimeType:  es.Resolution.RuntimeType,
		AllowedTools: es.Resolution.AllowedTools,
		MCPServers:   es.Topology.MCPServers,
		Backend:      es.Topology.Backend,
		ConfigPath:   es.Topology.ConfigPath,
		GraphAddr:    es.Topology.GraphAddr,
		StrategyDir:  es.Topology.StrategyDir,
		ConfigHash:   es.Topology.ConfigHash,
		GatewayURL:   es.Topology.GatewayURL,
		ObjectiveID:  es.Identity.ObjectiveID,
		MissionID:    es.Identity.MissionID,
	}
	if es.Credentials != nil {
		payload.StagedCredentialRefs = es.Credentials.StagedCredentialRefs
	}
	return payload
}

// ExecutionSpecFromPayload converts a deserialized MissionPayload back to an ExecutionSpec.
// Used at the top of worker.executeMission to establish the canonical seam.
func ExecutionSpecFromPayload(payload queue.MissionPayload) ExecutionSpec {
	spec := ExecutionSpec{
		Identity: SpecIdentity{
			Task:        payload.Task,
			Contract:    payload.Contract,
			BaseBranch:  payload.BaseBranch,
			ObjectiveID: payload.ObjectiveID,
			MissionID:   payload.MissionID,
		},
		Resolution: SpecResolution{
			RuntimeType:  payload.RuntimeType,
			Model:        payload.Model,
			Mode:         payload.Mode,
			AllowedTools: payload.AllowedTools,
		},
		Topology: SpecTopology{
			MCPServers:  payload.MCPServers,
			Backend:     payload.Backend,
			ConfigPath:  payload.ConfigPath,
			GraphAddr:   payload.GraphAddr,
			StrategyDir: payload.StrategyDir,
			ConfigHash:  payload.ConfigHash,
			GatewayURL:  payload.GatewayURL,
		},
	}
	if len(payload.StagedCredentialRefs) > 0 {
		spec.Credentials = &SpecCredentials{
			StagedCredentialRefs: payload.StagedCredentialRefs,
		}
	}
	return spec
}

// WorkspaceConfigFromSpec builds a host.WorkspaceConfig from an ExecutionSpec.
// GatewayURL comes from spec.Topology — no side arguments.
func WorkspaceConfigFromSpec(spec ExecutionSpec, h host.Host, mcpServers config.MCPServersConfig, credOutput *credentials.CredentialOutput) host.WorkspaceConfig {
	agentMCP := h.Capabilities().AgentMCP
	return host.WorkspaceConfig{
		AgentMCP:         agentMCP,
		Servers:          mcpServers,
		Backend:          spec.Topology.Backend,
		ConfigPath:       spec.Topology.ConfigPath,
		GraphAddr:        spec.Topology.GraphAddr,
		StrategyDir:      spec.Topology.StrategyDir,
		GatewayURL:       spec.Topology.GatewayURL,
		AgentTask:        spec.Identity.Task,
		ObjectiveID:      spec.Identity.ObjectiveID,
		MissionID:        spec.Identity.MissionID,
		CredentialOutput: credOutput,
	}
}

// BuildSpawnOpts constructs runtime.SpawnOpts from an ExecutionSpec, a host.CommandSpec,
// a workspace path, and per-spawn artifacts.
func BuildSpawnOpts(spec ExecutionSpec, cmdSpec host.CommandSpec, wsPath string, artifacts SpawnArtifacts) runtime.SpawnOpts {
	return runtime.SpawnOpts{
		ID:                  spec.Identity.Task,
		Command:             cmdSpec.Command,
		Args:                cmdSpec.Args,
		Env:                 cmdSpec.Env,
		HostEnv:             artifacts.HostEnv,
		WorkDir:             wsPath,
		Model:               spec.Resolution.Model,
		Image:               artifacts.Image,
		ConfigSnapshot:      artifacts.ConfigSnapshot,
		WorkspaceFiles:      artifacts.WorkspaceFiles,
		AuthSecretData:      artifacts.AuthSecretData,
		AuthSecretMountPath: artifacts.AuthSecretMountPath,
	}
}

// ComputeConfigHash returns a SHA-256 hex digest of the config for skew diagnostics.
func ComputeConfigHash(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
