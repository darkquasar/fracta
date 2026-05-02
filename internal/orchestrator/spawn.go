package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/darkquasar/fracta/internal/agentlifecycle"
	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/codex"
	"github.com/darkquasar/fracta/internal/host/opencode"
	"github.com/darkquasar/fracta/internal/hostadapter"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/workspace"
	"gopkg.in/yaml.v3"
)

const (
	maxLastOutput = 4096      // 4KB cap for LastOutput in state.json
	maxLogSize    = 1 << 20   // 1MB log file rotation threshold
	keepLogSize   = 512 << 10 // 512KB to keep after rotation
)

// resolveModel determines which model to use for a spawn.
// resolveModel determines the model for a spawn/say operation.
// Priority: explicit model > tier lookup via HostConfig > HostConfig.Model > empty (host CLI default).
func resolveModel(explicit, tier string, hc *config.HostConfig) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if tier != "" {
		valid := false
		for _, t := range model.ValidTierNames {
			if t == tier {
				valid = true
				break
			}
		}
		if !valid {
			return "", fmt.Errorf("invalid tier %q: must be one of %v", tier, model.ValidTierNames)
		}
		var tiers map[string]string
		if hc != nil {
			tiers = hc.ModelTiers
		}
		m, ok := model.ResolveModelFromTier(tier, tiers)
		if !ok {
			return "", fmt.Errorf("tier %q not configured in model_tiers", tier)
		}
		return m, nil
	}
	if hc != nil && hc.Model != "" {
		return hc.Model, nil
	}
	return "", nil // empty = host CLI uses its own default
}

// ResolvedSpawn holds everything needed to spawn an agent via any dispatch path.
// Computed once by ResolveSpawn, consumed by prepareSpawn and handleQueuedSpawn.
type ResolvedSpawn struct {
	RuntimeType  string
	Host         host.Host
	HostConfig   config.RuntimeEntry
	Model        string
	BaseBranch   string
	Mode         string
	AllowedTools []string
	ConfigPath   string
	GraphAddr    string
	StrategyDir  string
}

// ResolveSpawn computes all spawn parameters from a single entry point.
// This eliminates duplicated resolution logic between direct and queued dispatch.
//
// Resolution priority for each field:
//   - RuntimeType: explicit > Config.Agents.DefaultRuntime > RuntimeRegistry.Default()
//   - Model:       explicit > tier lookup via RuntimeEntry.ModelTiers > RuntimeEntry.Model > SpawnConfig.Model
//   - BaseBranch:  explicit > Config.Project.DefaultBaseBranch
//   - Mode:        explicit > Config.Agents.DefaultMode > "batch"
//   - AllowedTools: Config.Project.AllowedTools > SpawnCfg.AllowedTools
func (o *Orchestrator) ResolveSpawn(runtimeType, spawnModel, tier, baseBranch, mode string) (*ResolvedSpawn, error) {
	// Resolve runtime type: explicit > config default > registry default.
	if runtimeType == "" && o.Config != nil && o.Config.Agents.EffectiveDefaultRuntime() != "" {
		runtimeType = o.Config.Agents.EffectiveDefaultRuntime()
	}

	resolvedRuntimeType, h, err := o.resolveHost(runtimeType)
	if err != nil {
		return nil, err
	}

	// Resolve runtime config. Non-fatal when Config is nil (legacy path).
	var hc config.RuntimeEntry
	var hcPtr *config.RuntimeEntry
	if o.Config != nil {
		resolved, err := o.resolveRuntimeConfig(resolvedRuntimeType)
		if err != nil {
			return nil, err
		}
		hc = resolved
		hcPtr = &hc
	}

	// Resolve model.
	resolvedModel, err := resolveModel(spawnModel, tier, hcPtr)
	if err != nil {
		return nil, err
	}

	// Resolve base branch: explicit > config > empty.
	if baseBranch == "" && o.Config != nil {
		baseBranch = o.Config.Project.DefaultBaseBranch
	}

	// Resolve mode: explicit > config > "batch".
	if mode == "" {
		if o.Config != nil && o.Config.Agents.DefaultMode != "" {
			mode = o.Config.Agents.DefaultMode
		} else {
			mode = "batch"
		}
	}

	// Resolve allowed tools from unified config.
	var allowedTools []string
	if o.Config != nil {
		allowedTools = o.Config.Project.AllowedTools
	}

	// Capability enforcement: stream mode requires stream support.
	caps := h.Capabilities()
	if mode == "stream" && !caps.Stream {
		return nil, fmt.Errorf("runtime %q does not support streaming", resolvedRuntimeType)
	}

	resolved := &ResolvedSpawn{
		RuntimeType:  resolvedRuntimeType,
		Host:         h,
		HostConfig:   hc,
		Model:        resolvedModel,
		BaseBranch:   baseBranch,
		Mode:         mode,
		AllowedTools: allowedTools,
		ConfigPath:   o.ConfigPath,
		GraphAddr:    o.GraphAddr,
		StrategyDir:  o.StrategyDir,
	}

	log := fractalog.Component("orchestrator")
	log.Debug("ResolveSpawn result",
		"runtime", resolvedRuntimeType,
		"model", resolvedModel,
		"base_branch", baseBranch,
		"mode", mode,
		"allowed_tools_count", len(allowedTools),
		"stream", caps.Stream,
		"agent_mcp", caps.AgentMCP,
		"tool_permissions", caps.ToolPermissions,
	)

	return resolved, nil
}

// truncateOutput caps a string to maxLastOutput bytes, keeping the tail.
func truncateOutput(s string) string {
	if len(s) > maxLastOutput {
		return "...(truncated)...\n" + s[len(s)-maxLastOutput:]
	}
	return s
}

// writeAgentSettings delegates to the given Host adapter.
func writeAgentSettings(h host.Host, worktreePath string, allowedTools []string, cfg host.WorkspaceConfig) error {
	return h.WriteWorkspace(worktreePath, allowedTools, cfg)
}

// spawnPrep holds all data produced by the common setup phase of spawning.
type spawnPrep struct {
	workspacePath  string // local staging path
	runtimeWorkDir string // path as seen by the runtime process
	branchName     string
	logFile        string
	resolvedModel  string
	prompt         string
	wsInfo         *workspace.Info // workspace metadata for cleanup
	host           host.Host      // resolved host for this spawn
	runtimeType    string         // resolved runtime type name
	spec           ExecutionSpec  // canonical execution contract
	artifacts      SpawnArtifacts // per-spawn runtime artifacts
}

// runtimeWorkDir returns the agent's working directory as seen by the runtime process.
// Local backend: returns localPath (the real worktree). K8s: computes from PVCMountPath.
func (o *Orchestrator) runtimeWorkDir(task, localPath string) string {
	if o.RuntimeBackend == "kubernetes" && o.Config != nil {
		mountPath := o.Config.Runtime.Kubernetes.PVCMountPath
		if mountPath == "" {
			mountPath = "/workspace" // must match defaultPVCMountPath in runtime/k8s.go
		}
		return fmt.Sprintf("%s/agents/%s", mountPath, task)
	}
	return localPath
}

// prepareSpawn performs the common setup for Spawn, SpawnAsync, and SpawnStream:
// validate task name, check for duplicates, create worktree, write settings,
// write bootstrap file, and build the prompt.
//
// It consumes a *ResolvedSpawn produced by ResolveSpawn(), ensuring direct and
// queued dispatch share one resolution path. Do NOT duplicate resolution here.
func (o *Orchestrator) prepareSpawn(task, contractContent string, resolved *ResolvedSpawn) (*spawnPrep, error) {
	if err := model.ValidateTaskName(task); err != nil {
		return nil, err
	}

	existing, err := o.Store.FindAgent(context.Background(), task)
	if err != nil {
		return nil, fmt.Errorf("checking existing agents: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("agent %q already exists", task)
	}

	logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, task+".log")

	// Build ExecutionSpec — canonical contract for this spawn.
	spec := NewExecutionSpec(resolved, task, contractContent, "" /* objectiveID */, o)

	// Build host env from config (empty if no config or no host config).
	var hostEnv []runtime.EnvEntry
	if o.Config != nil {
		hostEnv, err = config.BuildHostEnv(resolved.HostConfig, o.RuntimeBackend)
		if err != nil {
			return nil, fmt.Errorf("building host env for %q: %w", resolved.RuntimeType, err)
		}
	}

	// Resolve credential profile and build/execute credential plan.
	var credOutput *credentials.CredentialOutput
	if o.Config != nil {
		credProfile, hostBinding, resolveErr := config.ResolveCredentialProfile(o.Config, resolved.RuntimeType)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolving credential profile for %q: %w", resolved.RuntimeType, resolveErr)
		}
		if credProfile != nil {
			plan, planErr := credentials.BuildCredentialPlan(
				resolved.HostConfig.AuthProfile,
				credentials.FromConfigProfile(credProfile),
				credentials.FromConfigBinding(hostBinding),
				hostEnv,
				credentials.PlanContext{
					Topology: credentials.TopologyHostEdge,
					Logger:   o.Logger,
				},
			)
			if planErr != nil {
				return nil, fmt.Errorf("building credential plan for %q: %w", resolved.RuntimeType, planErr)
			}
			output, execErr := credentials.ExecuteCredentialPlan(context.Background(), plan, credentials.PlanContext{
				Topology: credentials.TopologyHostEdge,
				Logger:   o.Logger,
			})
			if execErr != nil {
				o.emit(context.Background(), events.Warn("orchestrator", "credentials", execErr.Error()),
					func(e *events.Event) {
						e.Category = "auth"
						e.Task = task
						e.Outcome = "failure"
						e.Resource = "task:" + task
					})
				return nil, fmt.Errorf("executing credential plan: %w", execErr)
			}
			credOutput = output
		}
	}

	// Build SpawnArtifacts — non-serializable per-spawn runtime data.
	var artifacts SpawnArtifacts
	artifacts.HostEnv = hostEnv
	artifacts.Image = resolved.HostConfig.Kubernetes.Image
	if credOutput != nil {
		artifacts.AuthSecretData = credOutput.SecretData
		artifacts.AuthSecretMountPath = credOutput.MountPath
		artifacts.HostEnv = append(artifacts.HostEnv, credOutput.EnvEntries...)
	}
	if o.Config != nil && o.RuntimeBackend == "kubernetes" {
		snapshot, snapErr := BuildConfigSnapshot(o.Config)
		if snapErr != nil {
			return nil, fmt.Errorf("building config snapshot: %w", snapErr)
		}
		artifacts.ConfigSnapshot = snapshot
	}

	// Create the workspace directory (git worktree or plain directory).
	wsInfo, err := o.Workspace.Create(task, resolved.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	// From here on, clean up the workspace on error.
	cleanupWS := func() { o.Workspace.Remove(wsInfo, false) }

	// Capability enforcement logging.
	caps := resolved.Host.Capabilities()
	if !caps.AgentMCP {
		fractalog.Component("orchestrator").Debug("capability enforcement: AgentMCP degraded",
			"task", task, "runtime", resolved.RuntimeType)
	}
	if !caps.ToolPermissions {
		fractalog.Component("orchestrator").Debug("capability enforcement: ToolPermissions disabled",
			"task", task, "runtime", resolved.RuntimeType)
	}

	// Build WorkspaceConfig from ExecutionSpec.
	// GatewayURL comes from spec.Topology — no side arguments.
	rwd := o.runtimeWorkDir(task, wsInfo.Path)
	wsCfg := WorkspaceConfigFromSpec(spec, resolved.Host, o.MCPServers, credOutput)
	wsCfg.ProjectRoot = o.Root
	wsCfg.RuntimeWorkDir = rwd

	if err := writeAgentSettings(resolved.Host, wsInfo.Path, resolved.AllowedTools, wsCfg); err != nil {
		cleanupWS()
		return nil, fmt.Errorf("writing agent settings: %w", err)
	}

	// Use Host.Bootstrap for task file and initial prompt.
	var prompt string
	if contractContent != "" {
		boot := host.BootstrapHost(resolved.Host, task, resolved.BaseBranch, contractContent, wsCfg)
		if boot.FileName != "" {
			taskFile := filepath.Join(wsInfo.Path, boot.FileName)
			if err := os.WriteFile(taskFile, []byte(boot.FileBody), 0644); err != nil {
				cleanupWS()
				return nil, fmt.Errorf("writing %s: %w", boot.FileName, err)
			}
		}
		prompt = boot.InitialPrompt
	} else {
		prompt = task
	}

	// For K8s: collect workspace files for ConfigMap injection.
	if o.RuntimeBackend == "kubernetes" {
		artifacts.WorkspaceFiles = CollectWorkspaceFiles(wsInfo.Path, resolved.RuntimeType)
	}

	if o.Logger != nil {
		o.Logger.Info("spawning agent", "task", task, "model", resolved.Model, "branch", wsInfo.BranchName)
	}
	o.emit(context.Background(), events.Info("orchestrator", "create"),
		func(e *events.Event) {
			e.Category = "agent"
			e.Task = task
			e.Resource = "task:" + task
			e.Detail = fmt.Sprintf("host=%s model=%s", resolved.RuntimeType, resolved.Model)
			e.Attrs = map[string]string{
				"runtime": resolved.RuntimeType,
				"model":     resolved.Model,
			}
		})

	// Credentials.spawn.materialized — log materialized artifacts before spawn.
	if len(artifacts.AuthSecretData) > 0 || credOutput != nil {
		secretKeyNames := make([]string, 0, len(artifacts.AuthSecretData))
		for k := range artifacts.AuthSecretData {
			secretKeyNames = append(secretKeyNames, k)
		}
		var envVarNames []string
		if credOutput != nil {
			for _, e := range credOutput.EnvEntries {
				envVarNames = append(envVarNames, e.Name)
			}
		}
		fractalog.Component("credentials").Info("credentials.spawn.materialized",
			"task", task,
			"secret_mount_path", artifacts.AuthSecretMountPath,
			"secret_key_names", secretKeyNames,
			"env_var_names", envVarNames,
			"workspace_files_prepared", len(artifacts.WorkspaceFiles) > 0,
		)
	}

	return &spawnPrep{
		workspacePath:  wsInfo.Path,
		runtimeWorkDir: rwd,
		branchName:     wsInfo.BranchName,
		logFile:       logFile,
		resolvedModel: resolved.Model,
		prompt:        prompt,
		wsInfo:        wsInfo,
		host:          resolved.Host,
		runtimeType:   resolved.RuntimeType,
		spec:          spec,
		artifacts:     artifacts,
	}, nil
}

// Spawn runs an agent synchronously — blocks until the agent completes.
// Used by CLI commands where blocking is expected.
func (o *Orchestrator) Spawn(task, contractContent, baseBranch, spawnModel, tier, runtimeType string) error {
	resolved, err := o.ResolveSpawn(runtimeType, spawnModel, tier, baseBranch, "batch")
	if err != nil {
		return err
	}
	prep, err := o.prepareSpawn(task, contractContent, resolved)
	if err != nil {
		return err
	}

	cmdSpec := prep.host.BuildBatchCommand(prep.prompt, prep.resolvedModel, "")
	spawnOpts := BuildSpawnOpts(prep.spec, cmdSpec, prep.workspacePath, prep.artifacts)

	// Delegate process execution to Backend if available, else use direct exec.
	if o.Backend != nil {
		handle, err := o.Backend.Spawn(context.Background(), spawnOpts)
		if err != nil {
			o.Workspace.Remove(prep.wsInfo, false)
			return fmt.Errorf("spawning agent: %w", err)
		}

		waitErr := handle.Wait()
		output, _ := io.ReadAll(handle.Output())
		result, parseErr := prep.host.ParseBatchOutput(output, waitErr)
		if parseErr != nil {
			o.Workspace.Remove(prep.wsInfo, false)
			return fmt.Errorf("running agent: %w", parseErr)
		}
		return o.recordResult(task, prep.runtimeType, prep.workspacePath, prep.branchName, baseBranch, prep.logFile, result)
	}

	// Fallback: direct exec (no Backend configured).
	cmd := exec.Command(cmdSpec.Command, cmdSpec.Args...)
	cmd.Dir = prep.workspacePath
	if len(cmdSpec.Env) > 0 {
		cmd.Env = append(os.Environ(), cmdSpec.Env...)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	waitErr := cmd.Run()
	result, parseErr := prep.host.ParseBatchOutput(stdout.Bytes(), waitErr)
	if parseErr != nil {
		o.Workspace.Remove(prep.wsInfo, false)
		return fmt.Errorf("running agent: %w", parseErr)
	}
	return o.recordResult(task, prep.runtimeType, prep.workspacePath, prep.branchName, baseBranch, prep.logFile, result)
}

// recordResult logs and records the result of a synchronous spawn using host.Result.
func (o *Orchestrator) recordResult(task, runtimeType, worktreePath, branchName, baseBranch, logFile string, result host.Result) error {
	logEntry := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), result.Output)
	if err := appendToLog(logFile, logEntry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write log: %v\n", err)
	}

	status := model.StatusCompleted
	if result.IsError {
		status = model.StatusFailed
	}

	o.emitAgentResult(context.Background(), task, status, result.Output)

	entry := model.AgentEntry{
		Task:          task,
		RuntimeType:   runtimeType,
		ResumeToken:   result.ResumeToken,
		WorkspacePath: worktreePath,
		BranchName:    branchName,
		BaseBranch:    baseBranch,
		Status:        status,
		LastOutput:    truncateOutput(result.Output),
		StartTime:     time.Now(),
		Mode:          "batch",
	}

	return o.withState(func(st *model.State) error {
		if o.SpawnChecker != nil {
			if err := o.SpawnChecker.CheckSpawnAllowed(st); err != nil {
				return err
			}
		}
		st.Agents = append(st.Agents, entry)
		return nil
	})
}

// SpawnAsync launches claude in the background and returns immediately.
// The agent is recorded as Running; a goroutine updates state when claude finishes.
// Used by MCP handlers where non-blocking dispatch is desired.
func (o *Orchestrator) SpawnAsync(task, contractContent, baseBranch, spawnModel, tier, runtimeType string) error {
	resolved, err := o.ResolveSpawn(runtimeType, spawnModel, tier, baseBranch, "batch")
	if err != nil {
		return err
	}
	prep, err := o.prepareSpawn(task, contractContent, resolved)
	if err != nil {
		return err
	}

	cmdSpec := prep.host.BuildBatchCommand(prep.prompt, prep.resolvedModel, "")
	spawnOpts := BuildSpawnOpts(prep.spec, cmdSpec, prep.workspacePath, prep.artifacts)

	// Delegate process execution to Backend if available.
	if o.Backend != nil {
		handle, err := o.Backend.Spawn(context.Background(), spawnOpts)
		if err != nil {
			o.Workspace.Remove(prep.wsInfo, false)
			return fmt.Errorf("spawning agent: %w", err)
		}

		if err := o.Lifecycle.RecordAgentStarted(context.Background(), task, agentlifecycle.CreationMeta{
			LifecycleMeta: agentlifecycle.LifecycleMeta{
				RuntimeType: prep.runtimeType,
				Backend:     o.RuntimeBackend,
			},
			WorkspacePath: prep.workspacePath,
			BranchName:    prep.branchName,
			BaseBranch:    baseBranch,
			Mode:          "batch",
		}); err != nil {
			// Rollback: kill the spawned process and remove workspace.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if killErr := o.Backend.Kill(ctx, task); killErr != nil && !errors.Is(killErr, runtime.ErrNotFound) {
				o.Logger.Error("spawn rollback: failed to kill backend process", "task", task, "error", killErr)
			}
			cancel()
			o.Workspace.Remove(prep.wsInfo, false)
			return fmt.Errorf("updating state: %w", err)
		}

		go o.collectHandleResult(task, prep.runtimeType, prep.host, handle, prep.logFile)
		return nil
	}

	// Fallback: direct exec (no Backend configured)
	cmd := exec.Command(cmdSpec.Command, cmdSpec.Args...)
	cmd.Dir = prep.workspacePath
	if len(cmdSpec.Env) > 0 {
		cmd.Env = append(os.Environ(), cmdSpec.Env...)
	}
	var asyncStdout bytes.Buffer
	cmd.Stdout = &asyncStdout
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		o.Workspace.Remove(prep.wsInfo, false)
		return fmt.Errorf("starting agent: %w", err)
	}

	if err := o.Lifecycle.RecordAgentStarted(context.Background(), task, agentlifecycle.CreationMeta{
		LifecycleMeta: agentlifecycle.LifecycleMeta{
			RuntimeType: prep.runtimeType,
			Backend:     o.RuntimeBackend,
		},
		WorkspacePath: prep.workspacePath,
		BranchName:    prep.branchName,
		BaseBranch:    baseBranch,
		Mode:          "batch",
	}); err != nil {
		cmd.Process.Kill()
		o.Workspace.Remove(prep.wsInfo, false)
		return fmt.Errorf("updating state: %w", err)
	}

	go o.collectResult(task, prep.runtimeType, prep.host, cmd, &asyncStdout, prep.logFile)

	return nil
}

// SpawnStream launches claude in streaming mode. The process is started and
// registered immediately; init and the initial prompt are handled in a background
// goroutine so the MCP handler returns without blocking.
// The StreamSession is registered in the provided registry.
func (o *Orchestrator) SpawnStream(task, contractContent, baseBranch, spawnModel, tier, runtimeType string, registry *ProcessRegistry) error {
	// ResolveSpawn handles capability enforcement for stream mode.
	resolved, err := o.ResolveSpawn(runtimeType, spawnModel, tier, baseBranch, "stream")
	if err != nil {
		return err
	}

	prep, err := o.prepareSpawn(task, contractContent, resolved)
	if err != nil {
		return err
	}

	// Dispatch: K8s stream pod vs local stream process.
	var session host.StreamSession
	if o.RuntimeBackend == "kubernetes" {
		session, err = o.spawnK8sStream(context.Background(), prep)
	} else {
		session, err = prep.host.StartStream(prep.workspacePath, prep.resolvedModel, prep.logFile)
	}
	if err != nil {
		o.Workspace.Remove(prep.wsInfo, false)
		return fmt.Errorf("starting stream session: %w", err)
	}

	// Wire host stream adapter for lifecycle error detection + observability.
	// Observer is installed regardless of o.Events — lifecycle MarkFailed must
	// fire even without an event bus. Event emission is conditional inside.
	if observable, ok := session.(host.LineObservable); ok {
		adapter := hostadapter.NewStreamAdapter(prep.runtimeType, task)
		observable.SetLineObserver(func(line []byte) {
			result := adapter.ParseLine(line)
			if result.FatalError != nil {
				if err := o.Lifecycle.MarkFailed(context.Background(), task, agentlifecycle.ResultMeta{
					LifecycleMeta: agentlifecycle.LifecycleMeta{
						RuntimeType: prep.runtimeType,
						Reason:      result.FatalError.Message,
					},
					LastOutput: result.FatalError.Message,
				}); err != nil && err != agentlifecycle.ErrTransitionSkipped {
					o.Logger.Warn("stream fatal: MarkFailed failed", "task", task, "error", err)
				}
			}
			if o.Events != nil {
				for _, evt := range result.DetailEvents {
					o.Events.Emit(context.Background(), evt)
				}
			}
		})
	}

	// Wire event observer for non-stdio runtimes (SSE, WebSocket).
	if observable, ok := session.(host.EventObservable); ok {
		adapter := hostadapter.NewStreamAdapter(prep.runtimeType, task)
		observable.SetEventObserver(func(event []byte) {
			result := adapter.ParseLine(event)
			if result.FatalError != nil {
				if err := o.Lifecycle.MarkFailed(context.Background(), task, agentlifecycle.ResultMeta{
					LifecycleMeta: agentlifecycle.LifecycleMeta{
						RuntimeType: prep.runtimeType,
						Reason:      result.FatalError.Message,
					},
					LastOutput: result.FatalError.Message,
				}); err != nil && err != agentlifecycle.ErrTransitionSkipped {
					o.Logger.Warn("stream fatal: MarkFailed failed", "task", task, "error", err)
				}
			}
			if o.Events != nil {
				for _, evt := range result.DetailEvents {
					o.Events.Emit(context.Background(), evt)
				}
			}
		})
	}

	// Register handle immediately so say/peek/kill work once init completes.
	registry.Register(task, session)

	// Record agent as Running via lifecycle writer (admission + insert + lifecycle.started).
	if err := o.Lifecycle.RecordAgentStarted(context.Background(), task, agentlifecycle.CreationMeta{
		LifecycleMeta: agentlifecycle.LifecycleMeta{
			RuntimeType: prep.runtimeType,
			Backend:     o.RuntimeBackend,
		},
		WorkspacePath: prep.workspacePath,
		BranchName:    prep.branchName,
		BaseBranch:    baseBranch,
		Mode:          "stream",
	}); err != nil {
		session.Close()
		o.cleanupStreamPod(task)
		registry.Remove(task)
		o.Workspace.Remove(prep.wsInfo, false)
		return fmt.Errorf("updating state: %w", err)
	}

	// Background goroutine: wait for init, send first prompt, update state.
	go o.collectStreamInit(task, prep.runtimeType, session, prep.prompt, registry, prep.logFile)

	return nil
}

// cleanupStreamPod kills the backing K8s pod if the backend supports stream pods.
// No-op for local backends. Ignores ErrNotFound (pod may already be gone).
func (o *Orchestrator) cleanupStreamPod(task string) {
	sb, ok := o.Backend.(runtime.StreamBackend)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sb.KillStreamPod(ctx, task); err != nil && !errors.Is(err, runtime.ErrNotFound) {
		o.Logger.Warn("stream pod cleanup failed", "task", task, "error", err)
	}
}

// spawnK8sStream launches a persistent K8s pod and constructs a StreamSession
// from the returned connection metadata. The session connects over the network
// (WebSocket for Codex, HTTP for OpenCode) instead of local stdio.
func (o *Orchestrator) spawnK8sStream(ctx context.Context, prep *spawnPrep) (host.StreamSession, error) {
	streamBackend, ok := o.Backend.(runtime.StreamBackend)
	if !ok {
		return nil, fmt.Errorf("backend does not support stream pods (missing StreamBackend interface)")
	}

	// Build the serve command based on runtime type.
	streamOpts := runtime.StreamPodOpts{
		SpawnOpts: runtime.SpawnOpts{
			ID:                  prep.spec.Identity.Task,
			Image:               prep.artifacts.Image,
			HostEnv:             prep.artifacts.HostEnv,
			Model:               prep.resolvedModel,
			ConfigSnapshot:      prep.artifacts.ConfigSnapshot,
			WorkspaceFiles:      prep.artifacts.WorkspaceFiles,
			AuthSecretData:      prep.artifacts.AuthSecretData,
			AuthSecretMountPath: prep.artifacts.AuthSecretMountPath,
		},
		RuntimeType: prep.runtimeType,
	}

	switch prep.runtimeType {
	case "codex":
		streamOpts.Command = "codex"
		streamOpts.Args = []string{"app-server", "--listen", "ws://0.0.0.0:8080"}
		streamOpts.Port = 8080
		token := uuid.NewString()
		streamOpts.Args = append(streamOpts.Args, "--ws-auth", "capability-token")
		streamOpts.WebSocketAuthToken = token
		// Inject the auth token as env var for the pod to use.
		streamOpts.Env = append(streamOpts.Env, "CODEX_WS_AUTH_TOKEN="+token)

	case "opencode":
		streamOpts.Command = "opencode"
		streamOpts.Args = []string{"serve", "--port", "4096", "--hostname", "0.0.0.0"}
		streamOpts.Port = 4096
		password := uuid.NewString()
		streamOpts.ServePassword = password
		streamOpts.Env = append(streamOpts.Env, "OPENCODE_SERVER_PASSWORD="+password)

	default:
		return nil, fmt.Errorf("unsupported runtime %q for K8s stream mode", prep.runtimeType)
	}

	info, err := streamBackend.SpawnStreamPod(ctx, streamOpts)
	if err != nil {
		return nil, fmt.Errorf("spawning stream pod: %w", err)
	}

	// Construct the appropriate StreamSession from connection metadata.
	// If session creation fails, kill the orphaned pod.
	var session host.StreamSession
	switch {
	case info.CodexWebSocket != nil:
		session, err = codex.NewWebSocketAppServerSession(codex.WebSocketConfig{
			URL:       info.CodexWebSocket.URL,
			AuthToken: info.CodexWebSocket.AuthToken,
		}, prep.logFile)

	case info.OpenCodeHTTP != nil:
		session, err = opencode.NewRemoteServeSession(
			info.OpenCodeHTTP.BaseURL,
			info.OpenCodeHTTP.Password,
			opencode.StreamPermissionRules(prep.runtimeWorkDir, prep.spec.Identity.ObjectiveID),
		)

	default:
		err = fmt.Errorf("stream pod returned no connection metadata for runtime %q", prep.runtimeType)
	}

	if err != nil {
		_ = streamBackend.KillStreamPod(ctx, prep.spec.Identity.Task)
		return nil, err
	}
	return session, nil
}

// collectStreamInit runs in a background goroutine after SpawnStream returns.
// It sends the initial prompt (which triggers init + response) and updates
// the agent state to Idle on success or Failed on error.
// Raw protocol events are logged during Send() via the handle's logPath.
func (o *Orchestrator) collectStreamInit(task, runtimeType string, session host.StreamSession, prompt string, registry *ProcessRegistry, logFile string) {
	event, err := session.Send(prompt)
	if err != nil {
		session.Close()
		o.cleanupStreamPod(task)
		registry.Remove(task)
		o.transitionAgentToTerminal(context.Background(), task, model.StatusFailed, TerminalMeta{
			Reason:   fmt.Sprintf("initial prompt failed: %v", err),
			RuntimeType: runtimeType,
		})
		return
	}

	logEntry := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), event.Output)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	if err := o.Lifecycle.MarkIdle(context.Background(), task, event.Output, session.ResumeToken(), agentlifecycle.LifecycleMeta{
		RuntimeType: runtimeType,
	}); err != nil && err != agentlifecycle.ErrTransitionSkipped {
		o.Logger.Warn("stream init: MarkIdle failed", "task", task, "error", err)
	}

	// Emit heartbeats for stream agents so unresponsive detection works uniformly.
	go o.emitStreamHeartbeats(task, runtimeType, session)

	// Watch for unexpected process exit.
	go func() {
		<-session.Done()
		registry.Remove(task)
		ctx := context.Background()
		agent, err := o.Store.FindAgent(ctx, task)
		if err == nil && agent != nil && agent.Status != model.StatusFailed {
			o.transitionAgentToTerminal(ctx, task, model.StatusFailed, TerminalMeta{
				Reason:   "stream process exited unexpectedly",
				RuntimeType: runtimeType,
			})
		}
	}()
}

// collectHandleResult waits for a Backend AgentHandle to complete and updates state.
func (o *Orchestrator) collectHandleResult(task, runtimeType string, h host.Host, handle runtime.AgentHandle, logFile string) {
	waitErr := handle.Wait()
	output, _ := io.ReadAll(handle.Output())

	// Emit wire-level detail events via host adapter.
	if o.Events != nil {
		adapter := hostadapter.NewStreamAdapter(runtimeType, task)
		parsed := adapter.ParseBatchResult(output, waitErr)
		for _, evt := range parsed.DetailEvents {
			o.Events.Emit(context.Background(), evt)
		}
	}

	result, parseErr := h.ParseBatchOutput(output, waitErr)

	var status model.AgentStatus
	var lastOutput, resumeToken string
	if parseErr != nil {
		status = model.StatusFailed
		lastOutput = parseErr.Error()
	} else {
		resumeToken = result.ResumeToken
		lastOutput = result.Output
		if result.IsError {
			status = model.StatusFailed
		} else {
			status = model.StatusCompleted
		}
	}

	logEntry := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), lastOutput)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	meta := agentlifecycle.ResultMeta{
		LifecycleMeta: agentlifecycle.LifecycleMeta{RuntimeType: runtimeType},
		LastOutput:    truncateOutput(lastOutput),
		ResumeToken:   resumeToken,
	}
	var lcErr error
	if status == model.StatusCompleted {
		lcErr = o.Lifecycle.MarkCompleted(context.Background(), task, meta)
	} else {
		lcErr = o.Lifecycle.MarkFailed(context.Background(), task, meta)
	}
	if lcErr != nil && lcErr != agentlifecycle.ErrTransitionSkipped {
		o.Logger.Warn("collectHandleResult: lifecycle transition failed", "task", task, "error", lcErr)
	}
}

// collectResult waits for a background process to finish and updates state.
func (o *Orchestrator) collectResult(task, runtimeType string, h host.Host, cmd *exec.Cmd, stdout *bytes.Buffer, logFile string) {
	waitErr := cmd.Wait()

	// Emit wire-level detail events via host adapter.
	if o.Events != nil {
		adapter := hostadapter.NewStreamAdapter(runtimeType, task)
		parsed := adapter.ParseBatchResult(stdout.Bytes(), waitErr)
		for _, evt := range parsed.DetailEvents {
			o.Events.Emit(context.Background(), evt)
		}
	}

	result, parseErr := h.ParseBatchOutput(stdout.Bytes(), waitErr)

	var status model.AgentStatus
	var lastOutput, resumeToken string
	if parseErr != nil {
		status = model.StatusFailed
		lastOutput = parseErr.Error()
	} else {
		resumeToken = result.ResumeToken
		lastOutput = result.Output
		if result.IsError {
			status = model.StatusFailed
		} else {
			status = model.StatusCompleted
		}
	}

	logEntry := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), lastOutput)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	meta := agentlifecycle.ResultMeta{
		LifecycleMeta: agentlifecycle.LifecycleMeta{RuntimeType: runtimeType},
		LastOutput:    truncateOutput(lastOutput),
		ResumeToken:   resumeToken,
	}
	var lcErr error
	if status == model.StatusCompleted {
		lcErr = o.Lifecycle.MarkCompleted(context.Background(), task, meta)
	} else {
		lcErr = o.Lifecycle.MarkFailed(context.Background(), task, meta)
	}
	if lcErr != nil && lcErr != agentlifecycle.ErrTransitionSkipped {
		o.Logger.Warn("collectResult: lifecycle transition failed", "task", task, "error", lcErr)
	}
}

// updateAgentResult is DEPRECATED — retained only for backward-compatible call sites.
// New code should use Lifecycle.MarkCompleted/MarkFailed directly.
func (o *Orchestrator) updateAgentResult(task string, status model.AgentStatus, lastOutput, resumeToken string) {
	if err := o.Store.UpdateAgentResult(context.Background(), task, status, truncateOutput(lastOutput), resumeToken); err != nil {
		o.Logger.Warn("agent result update failed", "task", task, "error", err)
	}
	o.emitAgentResult(context.Background(), task, status, lastOutput)
	o.SnapshotProgress()
}

// emitStreamHeartbeats runs in a goroutine and periodically emits heartbeat
// events for a stream agent until the session is done. This ensures local stream
// agents have the same heartbeat/unresponsive detection as batch workers.
func (o *Orchestrator) emitStreamHeartbeats(task, runtimeType string, session host.StreamSession) {
	if o.Events == nil {
		return
	}
	interval := 15 * time.Second
	if o.Config != nil && o.Config.Observability.HeartbeatInterval.Duration > 0 {
		interval = o.Config.Observability.HeartbeatInterval.Duration
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	started := time.Now()

	for {
		select {
		case <-session.Done():
			return
		case <-ticker.C:
			uptime := int(time.Since(started).Seconds())
			// Derive phase from durable agent status (Idle vs Running).
			phase := "idle"
			if agent, err := o.Store.FindAgent(context.Background(), task); err == nil && agent != nil {
				if agent.Status == model.StatusRunning {
					phase = "executing"
				}
			}
			o.Events.Emit(context.Background(), events.Event{
				ID:        uuid.NewString(),
				Time:      time.Now(),
				Severity:  "debug",
				Component: "worker",
				Category:  "agent_activity",
				Action:    "heartbeat",
				Task:      task,
				Attrs: map[string]string{
					"runtime": runtimeType,
					"phase":     phase,
					"uptime_s":  fmt.Sprintf("%d", uptime),
				},
			})
		}
	}
}

func appendToLog(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(content)
	fi, statErr := f.Stat()
	f.Close()

	if writeErr != nil {
		return writeErr
	}

	// Rotate if file exceeds size limit
	if statErr == nil && fi.Size() > maxLogSize {
		if err := rotateLog(path, keepLogSize); err != nil {
			fractalog.Component("orchestrator").Warn("log rotation failed", "path", path, "error", err)
		}
	}

	return nil
}

// rotateLog keeps only the last keepBytes of a log file.
func rotateLog(path string, keepBytes int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening log for rotation: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log for rotation: %w", err)
	}
	if fi.Size() <= keepBytes {
		f.Close()
		return nil
	}
	if _, err := f.Seek(fi.Size()-keepBytes, io.SeekStart); err != nil {
		f.Close()
		return fmt.Errorf("seeking log for rotation: %w", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("reading log for rotation: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}


// workspaceFileSpec maps a relative workspace path to its ConfigMap key.
type workspaceFileSpec struct {
	RelPath      string // relative path in workspace (e.g. ".codex/config.toml")
	ConfigMapKey string // flat K8s-safe key (e.g. "dot-codex--config.toml")
}

// runtimeWorkspaceFiles defines the workspace files to collect per runtime type.
// The task/instructions file (CLAUDE.md, AGENTS.md) is written by Bootstrap,
// so it's included here for packaging into the ConfigMap.
var runtimeWorkspaceFiles = map[string][]workspaceFileSpec{
	"claude": {
		{filepath.Join(".claude", "settings.json"), "dot-claude--settings.json"},
		{".mcp.json", "dot-mcp.json"},
		{filepath.Join(".fracta", "user-settings.json"), "dot-fracta--user-settings.json"},
		{"CLAUDE.md", "CLAUDE.md"},
	},
	"codex": {
		{filepath.Join(".codex", "config.toml"), "dot-codex--config.toml"},
		{"AGENTS.md", "AGENTS.md"},
	},
	"opencode": {
		{"opencode.json", "opencode.json"},
		{"AGENTS.md", "AGENTS.md"},
	},
}

// CollectWorkspaceFiles reads workspace files from the local staging directory
// into a slice of WorkspaceArtifacts for K8s ConfigMap injection. The file set
// is determined by runtimeType. Missing files are silently skipped.
func CollectWorkspaceFiles(workspacePath, runtimeType string) []runtime.WorkspaceArtifact {
	specs, ok := runtimeWorkspaceFiles[runtimeType]
	if !ok {
		// Unknown runtime — fall back to Claude for backward compat.
		specs = runtimeWorkspaceFiles["claude"]
	}

	var artifacts []runtime.WorkspaceArtifact
	for _, spec := range specs {
		data, err := os.ReadFile(filepath.Join(workspacePath, spec.RelPath))
		if err != nil {
			continue // file not present
		}
		artifacts = append(artifacts, runtime.WorkspaceArtifact{
			ConfigMapKey: spec.ConfigMapKey,
			DestPath:     spec.RelPath,
			Content:      string(data),
		})
	}
	return artifacts
}

// agentConfigSnapshot is the minimal config subset an in-pod agent needs.
// Excludes runtime, hosts, auth_profiles, project (already resolved at spawn time).
type agentConfigSnapshot struct {
	Connections map[string]config.ConnectionConfig `yaml:"connections,omitempty"`
	Strategy    config.StrategyConfig              `yaml:"strategy,omitempty"`
	Logging     config.LoggingConfig               `yaml:"logging,omitempty"`
}

// BuildConfigSnapshot serializes a minimal agent config for K8s ConfigMap injection.
func BuildConfigSnapshot(cfg *config.Config) (string, error) {
	snap := agentConfigSnapshot{
		Connections: cfg.Connections,
		Strategy:    cfg.Strategy,
		Logging:     cfg.Logging,
	}
	data, err := yaml.Marshal(snap)
	if err != nil {
		return "", fmt.Errorf("marshaling config snapshot: %w", err)
	}
	return string(data), nil
}
