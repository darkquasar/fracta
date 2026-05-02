// Package worker implements the mission execution loop for queue-mode agents.
// Workers dequeue missions, resolve hosts via HostRegistry, execute them in
// ephemeral DirectoryWorkspaces, and write results back to the state store.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/hostadapter"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/darkquasar/fracta/internal/workspace"
)

// Worker dequeues missions and executes them using resolved hosts.
type Worker struct {
	ID            string
	Queue         queue.MissionQueue
	Store         state.Store
	Registry      host.HostRegistry
	Config        *config.Config              // worker's own config for host env resolution
	Backend       runtime.Backend             // from cp.Backend (LocalBackend or KubernetesBackend)
	Stager        credentials.CredentialStager // for rehydrating staged credentials (nil = no staging)
	Events        events.Bus                  // optional lifecycle event bus
	Lifecycle     *agentlifecycle.Writer      // lifecycle transition coordinator
	WorkspaceBase string                      // base directory for ephemeral workspaces
	PollInterval  time.Duration
	remoteBusURL  string                      // CP URL for remote event publishing (K8s split mode)
	logger        *slog.Logger
}

// Option configures a Worker.
type Option func(*Worker)

// WithPollInterval sets how often the cancellation monitor checks mission status.
func WithPollInterval(d time.Duration) Option {
	return func(w *Worker) { w.PollInterval = d }
}

// WithConfig sets the worker's config for host env resolution and skew diagnostics.
func WithConfig(cfg *config.Config) Option {
	return func(w *Worker) { w.Config = cfg }
}

// WithBackend sets the runtime backend for agent execution delegation.
func WithBackend(b runtime.Backend) Option {
	return func(w *Worker) { w.Backend = b }
}

// WithStager sets the credential stager for rehydrating staged credentials.
func WithStager(s credentials.CredentialStager) Option {
	return func(w *Worker) { w.Stager = s }
}

// WithEvents sets the lifecycle event bus.
func WithEvents(bus events.Bus) Option {
	return func(w *Worker) { w.Events = bus }
}

// WithLifecycle sets the lifecycle writer for coordinated terminal transitions.
func WithLifecycle(lc *agentlifecycle.Writer) Option {
	return func(w *Worker) { w.Lifecycle = lc }
}

// WithRemoteBusURL sets the control-plane URL for remote event publishing.
// When set, the worker creates per-mission RemoteBus instances that POST events
// to the CP HTTP endpoint instead of using the in-process event bus.
// This enables observability for K8s workers that lack in-process bus access.
func WithRemoteBusURL(url string) Option {
	return func(w *Worker) { w.remoteBusURL = url }
}

// New creates a Worker. WorkspaceBase defaults to os.TempDir()/fracta-workers if empty.
func New(id string, q queue.MissionQueue, store state.Store, reg host.HostRegistry, workspaceBase string, opts ...Option) *Worker {
	if workspaceBase == "" {
		workspaceBase = filepath.Join(os.TempDir(), "fracta-workers")
	}
	w := &Worker{
		ID:            id,
		Queue:         q,
		Store:         store,
		Registry:      reg,
		WorkspaceBase: workspaceBase,
		PollInterval:  10 * time.Second,
		logger:        fractalog.Component("worker").With("worker_id", id),
		Lifecycle:     agentlifecycle.New(store, events.NoopBus{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// emit sends a structured event via the bus. Nil-safe — does nothing when
// Events is nil. Mutators allow call sites to set category, task, etc.
func (w *Worker) emit(ctx context.Context, e events.Event, mutators ...func(*events.Event)) {
	if w.Events == nil {
		return
	}
	for _, m := range mutators {
		m(&e)
	}
	w.Events.Emit(ctx, e)
}

// Run is the main loop. It blocks, dequeuing and executing missions until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("worker started", "workspace_base", w.WorkspaceBase)
	for {
		mission, err := w.Queue.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				w.logger.Info("worker shutting down")
				return ctx.Err()
			}
			w.logger.Error("dequeue failed", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}

		w.logger.Info("claimed mission", "mission_id", mission.ID, "agent_task", mission.AgentTask)

		if err := w.executeMission(ctx, mission); err != nil {
			w.logger.Error("mission failed", "mission_id", mission.ID, "error", err)
		}
	}
}

// missionBus returns the event bus to use for a given mission.
// If remoteBusURL is configured, creates a per-mission RemoteBus.
// Otherwise uses the worker's shared event bus.
func (w *Worker) missionBus(task string) (events.Bus, func()) {
	if w.remoteBusURL != "" {
		rb := events.NewRemoteBus(events.RemoteBusConfig{
			BaseURL: w.remoteBusURL,
			Task:    task,
		})
		return rb, rb.Close
	}
	return w.Events, func() {}
}

// executeMission runs a single mission through the full lifecycle.
func (w *Worker) executeMission(ctx context.Context, m *queue.Mission) error {
	// Select per-mission bus (RemoteBus for K8s split mode, shared bus otherwise).
	bus, closeBus := w.missionBus(m.AgentTask)
	defer closeBus()
	origEvents := w.Events
	w.Events = bus
	defer func() { w.Events = origEvents }()

	// Mission-scoped lifecycle writer for split-mode event parity.
	if w.Lifecycle != nil && w.remoteBusURL != "" {
		origLifecycle := w.Lifecycle
		w.Lifecycle = w.Lifecycle.CloneWithBus(bus)
		defer func() { w.Lifecycle = origLifecycle }()
	}

	// Step 1: Claim the agent (Queued -> Running, StartTime = now) and emit lifecycle.started.
	if err := w.Lifecycle.ClaimQueuedAgent(ctx, m.AgentTask, agentlifecycle.LifecycleMeta{
		MissionID: m.ID,
	}); err != nil {
		if err == agentlifecycle.ErrTransitionSkipped {
			w.Queue.Fail(ctx, m.ID, "agent not claimable (not in Queued state)")
			return fmt.Errorf("claim agent: agent not claimable")
		}
		w.Queue.Fail(ctx, m.ID, fmt.Sprintf("claim agent failed: %v", err))
		return fmt.Errorf("claim agent: %w", err)
	}

	// Step 2: Deserialize payload.
	var payload queue.MissionPayload
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("invalid payload: %v", err))
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	// Step 2b: Inject mission identity into payload (not known at enqueue time).
	payload.MissionID = m.ID
	if payload.ObjectiveID == "" && m.ObjectiveID != "" {
		payload.ObjectiveID = m.ObjectiveID
	}

	// Step 2c: Convert payload to ExecutionSpec — canonical seam for all downstream use.
	spec := orchestrator.ExecutionSpecFromPayload(payload)

	w.emit(ctx, events.Info("worker", "claim"), func(e *events.Event) {
		e.Category = "agent"
		e.Task = m.AgentTask
		e.Resource = "task:" + m.AgentTask
		e.MissionID = m.ID
		e.Attrs = map[string]string{
			"mission_id": fmt.Sprintf("%d", m.ID),
			"runtime":  spec.Resolution.RuntimeType,
		}
	})

	// Step 3: Check if cancelled before expensive work.
	status, _ := w.Queue.Status(ctx, m.ID)
	if status == queue.StatusCancelled {
		w.failMission(ctx, m, model.StatusStopped, "cancelled before execution")
		return fmt.Errorf("mission cancelled before execution")
	}

	// Step 4: Resolve host from registry.
	h, err := w.Registry.Get(spec.Resolution.RuntimeType)
	if err != nil {
		w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("unknown host type %q", spec.Resolution.RuntimeType))
		return fmt.Errorf("resolve host: %w", err)
	}

	// Step 4b: Validate host config exists on this worker (config skew guard).
	if w.Config != nil {
		if _, ok := w.Config.EffectiveRuntimes()[spec.Resolution.RuntimeType]; !ok {
			w.failMission(ctx, m, model.StatusFailed,
				fmt.Sprintf("host type %q not configured on this worker", spec.Resolution.RuntimeType))
			return fmt.Errorf("host %q not in worker config", spec.Resolution.RuntimeType)
		}
		// ConfigHash skew diagnostic.
		if spec.Topology.ConfigHash != "" {
			workerHash := w.configHash()
			if workerHash != "" && workerHash != spec.Topology.ConfigHash {
				w.logger.Warn("config skew detected",
					"mission_id", m.ID,
					"orchestrator_hash", spec.Topology.ConfigHash,
					"worker_hash", workerHash)
				w.emit(ctx, events.Warn("worker", "config_skew",
					fmt.Sprintf("orchestrator=%s worker=%s", spec.Topology.ConfigHash[:8], workerHash[:8])),
					func(e *events.Event) {
						e.Category = "queue"
						e.Task = m.AgentTask
						e.Resource = "task:" + m.AgentTask
						e.MissionID = m.ID
						e.Attrs = map[string]string{
							"mission_id":        fmt.Sprintf("%d", m.ID),
							"orchestrator_hash": spec.Topology.ConfigHash,
							"worker_hash":       workerHash,
						}
					})
			}
		}
	}

	// Step 5: Create ephemeral workspace.
	ws := workspace.NewDirectoryWorkspace(w.WorkspaceBase)
	wsInfo, err := ws.Create(m.AgentTask, "")
	if err != nil {
		w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("workspace create: %v", err))
		return fmt.Errorf("create workspace: %w", err)
	}
	defer ws.Remove(wsInfo, false)

	// Step 6: Write host-specific workspace files.
	// Deserialize MCP servers from payload so agents get external tool access.
	var mcpServers config.MCPServersConfig
	if len(spec.Topology.MCPServers) > 0 {
		_ = json.Unmarshal(spec.Topology.MCPServers, &mcpServers)
	}
	// Capability enforcement: degrade AgentMCP if host doesn't support it.
	agentMCP := h.Capabilities().AgentMCP
	if !agentMCP {
		w.logger.Debug("capability enforcement: AgentMCP degraded",
			"mission_id", m.ID, "runtime", spec.Resolution.RuntimeType)
	}

	// Resolve gateway URL: spec > worker config fallback (spec 3.6).
	gatewayURL := spec.Topology.GatewayURL
	if gatewayURL == "" && w.Config != nil {
		gatewayURL = w.Config.Gateway.URL
	}

	// Resolve credential profile for workspace writing (user-settings.json / apiKeyHelper).
	var credOutput *credentials.CredentialOutput
	var stagedRefs map[string]string
	if spec.Credentials != nil {
		stagedRefs = spec.Credentials.StagedCredentialRefs
	}
	if w.Config != nil {
		credProfile, hostBinding, resolveErr := config.ResolveCredentialProfile(w.Config, spec.Resolution.RuntimeType)
		if resolveErr != nil {
			w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("resolve credentials for workspace: %v", resolveErr))
			return fmt.Errorf("resolve credential profile for workspace: %w", resolveErr)
		}
		if credProfile != nil {
			// Build host env for credential plan (without auth — auth comes from credential plan).
			var wsHostEnv []runtime.EnvEntry
			if hc, ok := w.Config.EffectiveRuntimes()[spec.Resolution.RuntimeType]; ok {
				wsHostEnv, _ = config.BuildHostEnv(hc, spec.Topology.Backend)
			}
			profileName := w.Config.EffectiveRuntimes()[spec.Resolution.RuntimeType].AuthProfile
			plan, planErr := credentials.BuildCredentialPlan(
				profileName,
				credentials.FromConfigProfile(credProfile),
				credentials.FromConfigBinding(hostBinding),
				wsHostEnv,
				credentials.PlanContext{
					Topology: credentials.TopologyInCluster,
					Logger:   w.logger,
				},
			)
			if planErr != nil {
				w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("build credential plan: %v", planErr))
				return fmt.Errorf("build credential plan: %w", planErr)
			}

			// Rehydrate staged credentials: fetch each ref via stager and
			// inject into the plan, upgrading unavailable sources to prepare_now.
			if len(stagedRefs) > 0 && w.Stager == nil {
				w.failMission(ctx, m, model.StatusFailed, "mission has staged credential refs but no stager configured; set control_plane_api.url in worker config for split-deployment mode")
				return fmt.Errorf("staged credential refs without stager")
			}
			if w.Stager != nil && len(stagedRefs) > 0 {
				for sourceName, ref := range stagedRefs {
					staged, fetchErr := w.Stager.Fetch(ctx, ref)
					if fetchErr != nil {
						w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("fetch staged credential %q: %v", sourceName, fetchErr))
						return fmt.Errorf("fetch staged credential %q: %w", sourceName, fetchErr)
					}
					if rehydErr := credentials.RehydrateSource(plan, sourceName, staged.Data); rehydErr != nil {
						w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("rehydrate credential %q: %v", sourceName, rehydErr))
						return fmt.Errorf("rehydrate credential %q: %w", sourceName, rehydErr)
					}
				}
			}

			output, execErr := credentials.ExecuteCredentialPlan(ctx, plan, credentials.PlanContext{
				Topology: credentials.TopologyInCluster,
				Logger:   w.logger,
			})
			if execErr != nil {
				w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("execute credential plan: %v", execErr))
				return fmt.Errorf("execute credential plan: %w", execErr)
			}
			credOutput = output
		}
	}

	// Build WorkspaceConfig from ExecutionSpec (replaces 12-field manual construction).
	wsCfg := orchestrator.WorkspaceConfigFromSpec(spec, h, mcpServers, credOutput)
	wsCfg.ProjectRoot = wsInfo.Path

	// Compute the runtime-visible workspace path for permission rules.
	if spec.Topology.Backend == "kubernetes" && w.Config != nil {
		mountPath := w.Config.Runtime.Kubernetes.PVCMountPath
		if mountPath == "" {
			mountPath = "/workspace"
		}
		wsCfg.RuntimeWorkDir = fmt.Sprintf("%s/agents/%s", mountPath, m.AgentTask)
	} else {
		wsCfg.RuntimeWorkDir = wsInfo.Path
	}

	w.logger.Debug("WorkspaceConfig assembled",
		"mission_id", m.ID,
		"agent_mcp", agentMCP,
		"backend", spec.Topology.Backend,
		"config_path", spec.Topology.ConfigPath,
		"graph_addr", spec.Topology.GraphAddr,
		"strategy_dir", spec.Topology.StrategyDir,
		"gateway_url", gatewayURL,
		"objective_id", spec.Identity.ObjectiveID,
	)

	if err := h.WriteWorkspace(wsInfo.Path, spec.Resolution.AllowedTools, wsCfg); err != nil {
		w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("write workspace: %v", err))
		return fmt.Errorf("write workspace: %w", err)
	}

	// Step 7: Bootstrap — get task file and initial prompt.
	// BootstrapHost delegates to BootstrapWithConfig when available,
	// enabling conditional graph/strategy instructions.
	bootstrap := host.BootstrapHost(h, spec.Identity.Task, spec.Identity.BaseBranch, spec.Identity.Contract, wsCfg)
	if bootstrap.FileName != "" {
		taskFilePath := filepath.Join(wsInfo.Path, bootstrap.FileName)
		if err := os.WriteFile(taskFilePath, []byte(bootstrap.FileBody), 0644); err != nil {
			w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("write task file: %v", err))
			return fmt.Errorf("write task file: %w", err)
		}
	}

	// Step 8: Build the batch command.
	cmdSpec := h.BuildBatchCommand(bootstrap.InitialPrompt, spec.Resolution.Model, "")

	w.emit(ctx, events.Info("worker", "execute"), func(e *events.Event) {
		e.Category = "agent"
		e.Task = m.AgentTask
		e.Resource = "task:" + m.AgentTask
		e.MissionID = m.ID
		e.Attrs = map[string]string{
			"mission_id": fmt.Sprintf("%d", m.ID),
			"model":      spec.Resolution.Model,
			"runtime":  spec.Resolution.RuntimeType,
		}
	})

	// Step 9: Build SpawnArtifacts and SpawnOpts from ExecutionSpec.
	var artifacts orchestrator.SpawnArtifacts
	if w.Config != nil {
		if hc, ok := w.Config.EffectiveRuntimes()[spec.Resolution.RuntimeType]; ok {
			hostEnv, envErr := config.BuildHostEnv(hc, spec.Topology.Backend)
			if envErr != nil {
				w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("build host env: %v", envErr))
				return fmt.Errorf("build host env: %w", envErr)
			}
			artifacts.HostEnv = hostEnv
			artifacts.Image = hc.Kubernetes.Image
		}
	}

	// Merge credential env entries into host env for all backends.
	// Without this, local-mode queued spawns miss env vars like AWS_BEARER_TOKEN_BEDROCK.
	if credOutput != nil {
		artifacts.HostEnv = append(artifacts.HostEnv, credOutput.EnvEntries...)
	}

	if spec.Topology.Backend == "kubernetes" && w.Config != nil {
		if credOutput != nil {
			artifacts.AuthSecretData = credOutput.SecretData
			artifacts.AuthSecretMountPath = credOutput.MountPath
		}

		// Build config snapshot.
		configSnapshot, snapErr := orchestrator.BuildConfigSnapshot(w.Config)
		if snapErr != nil {
			w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("config snapshot: %v", snapErr))
			return fmt.Errorf("build config snapshot: %w", snapErr)
		}
		artifacts.ConfigSnapshot = configSnapshot

		// Collect workspace files for ConfigMap.
		artifacts.WorkspaceFiles = orchestrator.CollectWorkspaceFiles(wsInfo.Path, spec.Resolution.RuntimeType)
	}

	spawnOpts := orchestrator.BuildSpawnOpts(spec, cmdSpec, wsInfo.Path, artifacts)

	// Stage 9: credentials.spawn.materialized — log materialized artifacts before spawn.
	if len(spawnOpts.AuthSecretData) > 0 || credOutput != nil {
		secretKeyNames := make([]string, 0, len(spawnOpts.AuthSecretData))
		for k := range spawnOpts.AuthSecretData {
			secretKeyNames = append(secretKeyNames, k)
		}
		var envVarNames []string
		if credOutput != nil {
			for _, e := range credOutput.EnvEntries {
				envVarNames = append(envVarNames, e.Name)
			}
		}
		w.logger.Info("credentials.spawn.materialized",
			"task", m.AgentTask,
			"secret_mount_path", spawnOpts.AuthSecretMountPath,
			"secret_key_names", secretKeyNames,
			"env_var_names", envVarNames,
			"workspace_files_prepared", len(spawnOpts.WorkspaceFiles) > 0,
		)
	}

	// Step 10: Execute with cancellation monitoring.
	missionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go w.monitorCancellation(missionCtx, cancel, m.ID)

	handle, spawnErr := w.Backend.Spawn(missionCtx, spawnOpts)
	if spawnErr != nil {
		w.failMission(ctx, m, model.StatusFailed, fmt.Sprintf("spawn: %v", spawnErr))
		return fmt.Errorf("backend spawn: %w", spawnErr)
	}

	// lifecycle.started is emitted by ClaimQueuedAgent at Step 1.

	// Resolve heartbeat interval on the calling goroutine to avoid racing on w.Config.
	hbInterval := 15 * time.Second
	if w.Config != nil && w.Config.Observability.HeartbeatInterval.Duration > 0 {
		hbInterval = w.Config.Observability.HeartbeatInterval.Duration
	}
	go w.emitHeartbeats(missionCtx, m.AgentTask, m.ID, spec.Resolution.RuntimeType, hbInterval)

	waitErr := handle.Wait()
	stdout, _ := io.ReadAll(handle.Output())

	// Emit wire-level detail events via host adapter (message.completed, tool events).
	// Lifecycle events are owned by the agentlifecycle.Writer, not the adapter.
	if w.Events != nil {
		adapter := hostadapter.NewStreamAdapter(spec.Resolution.RuntimeType, m.AgentTask)
		parsed := adapter.ParseBatchResult(stdout, waitErr)
		for _, evt := range parsed.DetailEvents {
			evt.MissionID = m.ID
			w.Events.Emit(ctx, evt)
		}
	}

	// Step 10b: Parse output.
	result, parseErr := h.ParseBatchOutput(stdout, waitErr)
	if parseErr != nil && missionCtx.Err() != nil {
		// Killed by cancellation — not a parse error.
		w.emit(ctx, events.Info("worker", "cancel"), func(e *events.Event) {
			e.Category = "agent"
			e.Task = m.AgentTask
			e.Resource = "task:" + m.AgentTask
			e.MissionID = m.ID
			e.Attrs = map[string]string{"mission_id": fmt.Sprintf("%d", m.ID)}
		})
		w.failMission(ctx, m, model.StatusStopped, "cancelled during execution")
		return fmt.Errorf("mission cancelled during execution")
	}

	// Step 11+12: Write result via lifecycle writer.
	// The conditional store primitive rejects the write if the agent is already
	// terminal (killed, TTL expired, or completed by another path) — no separate
	// check-then-update guard needed.
	var agentStatus model.AgentStatus
	if parseErr != nil || result.IsError {
		agentStatus = model.StatusFailed
	} else {
		agentStatus = model.StatusCompleted
	}

	output := result.Output
	if parseErr != nil {
		output = fmt.Sprintf("parse error: %v", parseErr)
	}

	if w.Lifecycle != nil {
		meta := agentlifecycle.ResultMeta{
			LifecycleMeta: agentlifecycle.LifecycleMeta{
				RuntimeType: spec.Resolution.RuntimeType,
				MissionID:   m.ID,
			},
			LastOutput:  output,
			ResumeToken: result.ResumeToken,
		}
		var lcErr error
		if agentStatus == model.StatusCompleted {
			lcErr = w.Lifecycle.MarkCompleted(ctx, m.AgentTask, meta)
		} else {
			lcErr = w.Lifecycle.MarkFailed(ctx, m.AgentTask, meta)
		}
		if lcErr == agentlifecycle.ErrTransitionSkipped {
			w.Queue.Fail(ctx, m.ID, "agent already terminal")
			w.logger.Info("skipping result write — agent already terminal",
				"mission_id", m.ID)
			return nil
		} else if lcErr != nil {
			w.logger.Error("failed to update agent result via lifecycle writer", "error", lcErr, "mission_id", m.ID)
		}
	} else {
		if err := w.Store.UpdateAgentResult(ctx, m.AgentTask, agentStatus, output, result.ResumeToken); err != nil {
			w.logger.Error("failed to update agent result", "error", err, "mission_id", m.ID)
		}
	}

	// Step 13: Ack or Fail the mission in the queue.
	if agentStatus == model.StatusCompleted {
		w.emit(ctx, events.Info("worker", "complete"), func(e *events.Event) {
			e.Category = "agent"
			e.Task = m.AgentTask
			e.Resource = "task:" + m.AgentTask
			e.Outcome = "success"
			e.MissionID = m.ID
			e.Attrs = map[string]string{"mission_id": fmt.Sprintf("%d", m.ID)}
		})
		w.Queue.Ack(ctx, m.ID)
		w.logger.Info("mission completed", "mission_id", m.ID)
	} else {
		reason := output
		if len(reason) > 500 {
			reason = reason[:500]
		}
		w.emit(ctx, events.Warn("worker", "fail", reason), func(e *events.Event) {
			e.Category = "agent"
			e.Task = m.AgentTask
			e.Resource = "task:" + m.AgentTask
			e.Outcome = "failure"
			e.MissionID = m.ID
			e.Attrs = map[string]string{"mission_id": fmt.Sprintf("%d", m.ID)}
		})
		w.Queue.Fail(ctx, m.ID, reason)
		w.logger.Info("mission failed", "mission_id", m.ID, "reason", reason)
	}

	return nil
}

// emitHeartbeats sends periodic heartbeat events while the mission is executing.
// It runs as a goroutine during handle.Wait() and stops when ctx is cancelled.
func (w *Worker) emitHeartbeats(ctx context.Context, task string, missionID int64, runtimeType string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			uptime := int(time.Since(started).Seconds())
			w.emit(ctx, events.Event{
				ID:        uuid.NewString(),
				Time:      time.Now(),
				Severity:  "debug",
				Component: "worker",
				Category:  "agent_activity",
				Action:    "heartbeat",
				Task:      task,
				MissionID: missionID,
				Attrs: map[string]string{
					"runtime": runtimeType,
					"phase":     "executing",
					"uptime_s":  fmt.Sprintf("%d", uptime),
				},
			})
		}
	}
}

// monitorCancellation polls mission status and cancels the context if the mission is cancelled.
func (w *Worker) monitorCancellation(ctx context.Context, cancel context.CancelFunc, missionID int64) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s, _ := w.Queue.Status(ctx, missionID)
			if s == queue.StatusCancelled {
				w.logger.Info("cancellation detected", "mission_id", missionID)
				cancel()
				return
			}
		}
	}
}

// configHash returns the SHA-256 hash of the worker's config for skew diagnostics.
func (w *Worker) configHash() string {
	if w.Config == nil {
		return ""
	}
	data, err := json.Marshal(w.Config)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// failMission updates both the agent status and fails the mission in the queue.
func (w *Worker) failMission(ctx context.Context, m *queue.Mission, status model.AgentStatus, reason string) {
	detail := reason
	if len(detail) > 512 {
		detail = detail[:512]
	}
	// Legacy worker-level event.
	w.emit(ctx, events.Warn("worker", "fail", detail), func(e *events.Event) {
		e.Category = "agent"
		e.Task = m.AgentTask
		e.Resource = "task:" + m.AgentTask
		e.Outcome = "failure"
		e.MissionID = m.ID
		e.Attrs = map[string]string{
			"mission_id": fmt.Sprintf("%d", m.ID),
			"reason":     detail,
		}
	})
	// Single choke point: durable state + canonical observability event.
	w.transitionWorkerToTerminal(ctx, m.AgentTask, status, detail, m.ID)
	w.Queue.Fail(ctx, m.ID, reason)
}

// transitionWorkerToTerminal is the single choke point for worker-owned terminal
// transitions. Delegates to the lifecycle writer for coordinated state + event.
// In split-deployment mode, executeMission swaps w.Lifecycle to a mission-scoped
// clone using the per-mission RemoteBus, so lifecycle events reach the remote CP.
func (w *Worker) transitionWorkerToTerminal(ctx context.Context, task string, status model.AgentStatus, reason string, missionID int64) {
	if w.Lifecycle == nil {
		// Fallback: direct store update if writer not wired.
		w.Store.UpdateAgentStatus(ctx, task, status, reason)
		return
	}

	lm := agentlifecycle.LifecycleMeta{
		MissionID: missionID,
		Reason:    reason,
	}

	var err error
	switch status {
	case model.StatusCompleted:
		err = w.Lifecycle.MarkCompleted(ctx, task, agentlifecycle.ResultMeta{
			LifecycleMeta: lm,
			LastOutput:    reason,
		})
	case model.StatusFailed:
		err = w.Lifecycle.MarkFailed(ctx, task, agentlifecycle.ResultMeta{
			LifecycleMeta: lm,
			LastOutput:    reason,
		})
	case model.StatusStopped:
		err = w.Lifecycle.MarkStopped(ctx, task, lm)
	default:
		w.Store.UpdateAgentStatus(ctx, task, status, reason)
	}

	if err != nil && err != agentlifecycle.ErrTransitionSkipped {
		w.logger.Warn("lifecycle transition failed", "task", task, "status", status, "error", err)
	}
}
