package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/runtime"
)

// sayPrep holds the resolved state needed for a Say/SayAsync operation.
type sayPrep struct {
	agent   *model.AgentEntry
	host    host.Host
	spec    host.CommandSpec
	hostEnv []runtime.EnvEntry
	logFile string
}

// prepareSay resolves host, model, command spec, and host env for a say operation.
// Fails fast on missing config, capability issues, or env build errors.
func (o *Orchestrator) prepareSay(task, message string) (*sayPrep, error) {
	ctx := context.Background()
	agent, err := o.Store.FindAgent(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %q not found", task)
	}
	if agent.ResumeToken == "" {
		return nil, fmt.Errorf("agent %q has no session to resume", task)
	}

	_, h, err := o.resolveHost(agent.RuntimeType)
	if err != nil {
		return nil, fmt.Errorf("resolving host: %w", err)
	}

	if !h.Capabilities().ResumeToken {
		return nil, fmt.Errorf("host %q does not support session resumption", agent.RuntimeType)
	}

	// Resolve model from host config. Fail fast if not configured.
	var hcPtr *config.HostConfig
	if o.Config != nil {
		hc, err := o.resolveRuntimeConfig(agent.RuntimeType)
		if err != nil {
			return nil, fmt.Errorf("resolving host config for %q: %w", agent.RuntimeType, err)
		}
		hcPtr = &hc
	}
	sayModel, err := resolveModel("", "", hcPtr)
	if err != nil {
		return nil, fmt.Errorf("resolving model: %w", err)
	}

	// Build host env (without auth — auth env comes from credential plan at spawn time).
	var hostEnv []runtime.EnvEntry
	if hcPtr != nil {
		hostEnv, err = config.BuildHostEnv(*hcPtr, o.RuntimeBackend)
		if err != nil {
			return nil, fmt.Errorf("building host env for %q: %w", agent.RuntimeType, err)
		}
	}

	spec := h.BuildBatchCommand(message, sayModel, agent.ResumeToken)
	logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, task+".log")

	return &sayPrep{
		agent:   agent,
		host:    h,
		spec:    spec,
		hostEnv: hostEnv,
		logFile: logFile,
	}, nil
}

// Say sends a follow-up message synchronously — blocks until the host responds.
// Routes through runtime.Backend when available (supports K8s), falls back to
// direct exec for local-only operation.
func (o *Orchestrator) Say(task, message string) (string, error) {
	prep, err := o.prepareSay(task, message)
	if err != nil {
		return "", err
	}

	var result host.Result

	if o.Backend != nil {
		// Route through Backend — works for both local and K8s.
		handle, err := o.Backend.Spawn(context.Background(), runtime.SpawnOpts{
			ID:      task + "-say",
			Command: prep.spec.Command,
			Args:    prep.spec.Args,
			Env:     prep.spec.Env,
			HostEnv: prep.hostEnv,
			WorkDir: prep.agent.WorkspacePath,
		})
		if err != nil {
			return "", fmt.Errorf("running agent: %w", err)
		}
		waitErr := handle.Wait()
		output, _ := io.ReadAll(handle.Output())
		result, err = prep.host.ParseBatchOutput(output, waitErr)
		if err != nil {
			return "", fmt.Errorf("running agent: %w", err)
		}
	} else {
		// Fallback: direct exec (no Backend configured).
		cmd := exec.Command(prep.spec.Command, prep.spec.Args...)
		cmd.Dir = prep.agent.WorkspacePath
		cmd.Env = buildCmdEnv(prep.spec.Env, prep.hostEnv)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = nil
		waitErr := cmd.Run()
		result, err = prep.host.ParseBatchOutput(stdout.Bytes(), waitErr)
		if err != nil {
			return "", fmt.Errorf("running agent: %w", err)
		}
	}

	logEntry := fmt.Sprintf("[%s] say: %s\n[%s] %s\n",
		time.Now().Format(time.RFC3339), message,
		time.Now().Format(time.RFC3339), result.Output,
	)
	if err := appendToLog(prep.logFile, logEntry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write log: %v\n", err)
	}

	status := model.StatusCompleted
	if result.IsError {
		status = model.StatusFailed
	}

	o.updateAgentResult(task, status, result.Output, result.ResumeToken)

	return result.Output, nil
}

// SayAsync sends a follow-up message and returns immediately.
// The agent is marked Running; a goroutine updates state when the host responds.
// Routes through runtime.Backend when available (supports K8s).
func (o *Orchestrator) SayAsync(task, message string) error {
	ctx := context.Background()
	agent, err := o.Store.FindAgent(ctx, task)
	if err != nil {
		return fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("agent %q not found", task)
	}
	if agent.ResumeToken == "" {
		return fmt.Errorf("agent %q has no session to resume", task)
	}
	if agent.Status == model.StatusRunning {
		return fmt.Errorf("agent %q is already running", task)
	}

	prep, err := o.prepareSay(task, message)
	if err != nil {
		return err
	}

	if o.Backend != nil {
		// Route through Backend — works for both local and K8s.
		handle, err := o.Backend.Spawn(ctx, runtime.SpawnOpts{
			ID:      task + "-say",
			Command: prep.spec.Command,
			Args:    prep.spec.Args,
			Env:     prep.spec.Env,
			HostEnv: prep.hostEnv,
			WorkDir: prep.agent.WorkspacePath,
		})
		if err != nil {
			return fmt.Errorf("running agent: %w", err)
		}

		// Mark as Running — rollback on failure (kill the spawned process).
		if err := o.Store.UpdateAgentStatus(ctx, task, model.StatusRunning, ""); err != nil {
			killCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if killErr := o.Backend.Kill(killCtx, task+"-say"); killErr != nil {
				o.Logger.Error("say rollback: failed to kill backend process", "task", task, "error", killErr)
			}
			cancel()
			return fmt.Errorf("updating state: %w", err)
		}
		o.SnapshotProgress()

		logFile := prep.logFile
		logEntry := fmt.Sprintf("[%s] say: %s\n", time.Now().Format(time.RFC3339), message)
		if err := appendToLog(logFile, logEntry); err != nil {
			o.Logger.Warn("log write failed", "task", task, "error", err)
		}

		go o.collectHandleResult(task, prep.agent.RuntimeType, prep.host, handle, logFile)
		return nil
	}

	// Fallback: direct exec (no Backend configured).
	cmd := exec.Command(prep.spec.Command, prep.spec.Args...)
	cmd.Dir = prep.agent.WorkspacePath
	cmd.Env = buildCmdEnv(prep.spec.Env, prep.hostEnv)
	var asyncStdout bytes.Buffer
	cmd.Stdout = &asyncStdout
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting agent: %w", err)
	}

	// Mark as Running
	if err := o.Store.UpdateAgentStatus(ctx, task, model.StatusRunning, ""); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("updating state: %w", err)
	}
	o.SnapshotProgress()

	logFile := prep.logFile
	logEntry := fmt.Sprintf("[%s] say: %s\n", time.Now().Format(time.RFC3339), message)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	go o.collectResult(task, prep.agent.RuntimeType, prep.host, cmd, &asyncStdout, logFile)

	return nil
}

// buildCmdEnv constructs the process environment for direct exec.
// Merges base OS env + host adapter env (spec.Env) + host config env (hostEnv).
// Only plain Value entries are included — SecretRef entries are skipped
// (they require K8s secretKeyRef rendering via Backend).
func buildCmdEnv(specEnv []string, hostEnv []runtime.EnvEntry) []string {
	env := os.Environ()
	if len(specEnv) > 0 {
		env = append(env, specEnv...)
	}
	for _, e := range hostEnv {
		if e.Value != "" {
			env = append(env, e.Name+"="+e.Value)
		}
		// SecretRef entries are intentionally skipped for direct exec.
		// They require K8s secretKeyRef rendering via Backend.Spawn.
	}
	return env
}

// SayStream sends a message to a streaming agent via its StreamSession.
// Blocks until the response is received, then returns the result text.
func (o *Orchestrator) SayStream(task, message string, registry *ProcessRegistry) (string, error) {
	handle := registry.Get(task)
	if handle == nil {
		return "", fmt.Errorf("no stream handle for agent %q", task)
	}

	ctx := context.Background()
	// Mark as Running
	if err := o.Store.UpdateAgentStatus(ctx, task, model.StatusRunning, ""); err != nil {
		return "", fmt.Errorf("updating state: %w", err)
	}
	o.SnapshotProgress()

	logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, task+".log")
	logEntry := fmt.Sprintf("[%s] say: %s\n", time.Now().Format(time.RFC3339), message)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	result, err := handle.Send(message)
	if err != nil {
		o.updateAgentResult(task, model.StatusFailed, err.Error(), "")
		return "", fmt.Errorf("sending message: %w", err)
	}

	logEntry = fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), result.Output)
	if err := appendToLog(logFile, logEntry); err != nil {
		o.Logger.Warn("log write failed", "task", task, "error", err)
	}

	o.updateAgentResult(task, model.StatusIdle, result.Output, handle.ResumeToken())

	return result.Output, nil
}

// SayStreamAsync sends a message to a streaming agent asynchronously.
// Returns immediately; the result is collected in a goroutine.
func (o *Orchestrator) SayStreamAsync(task, message string, registry *ProcessRegistry) error {
	handle := registry.Get(task)
	if handle == nil {
		return fmt.Errorf("no stream handle for agent %q", task)
	}

	ctx := context.Background()
	agent, err := o.Store.FindAgent(ctx, task)
	if err != nil {
		return fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("agent %q not found", task)
	}
	if agent.Status == model.StatusRunning {
		return fmt.Errorf("agent %q is already running", task)
	}

	// Mark as Running
	if err := o.Store.UpdateAgentStatus(ctx, task, model.StatusRunning, ""); err != nil {
		return fmt.Errorf("updating state: %w", err)
	}
	o.SnapshotProgress()

	go func() {
		logFile := filepath.Join(o.Root, model.FractaDir, model.LogsDir, task+".log")
		logEntry := fmt.Sprintf("[%s] say: %s\n", time.Now().Format(time.RFC3339), message)
		if err := appendToLog(logFile, logEntry); err != nil {
			o.Logger.Warn("log write failed", "task", task, "error", err)
		}

		result, err := handle.Send(message)

		var status model.AgentStatus
		var lastOutput, resumeToken string
		if err != nil {
			status = model.StatusFailed
			lastOutput = err.Error()
		} else {
			status = model.StatusIdle
			lastOutput = result.Output
			resumeToken = handle.ResumeToken()
		}

		logEntry = fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), lastOutput)
		if err := appendToLog(logFile, logEntry); err != nil {
			o.Logger.Warn("log write failed", "task", task, "error", err)
		}

		o.updateAgentResult(task, status, lastOutput, resumeToken)
	}()

	return nil
}
