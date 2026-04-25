package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/model"
)

var _ Backend = (*LocalBackend)(nil)

// LocalBackend runs agents as local OS processes via exec.Cmd.
type LocalBackend struct {
	mu      sync.Mutex
	handles map[string]*localHandle
	root    string     // project root for log file access
	events  events.Bus // optional; reserved for future local event emission
}

// NewLocalBackend creates a LocalBackend. If root is non-empty, Logs()
// can read agent log files from {root}/.fracta/logs/{id}.log.
func NewLocalBackend(root ...string) *LocalBackend {
	r := ""
	if len(root) > 0 {
		r = root[0]
	}
	return &LocalBackend{
		handles: make(map[string]*localHandle),
		root:    r,
	}
}

// SetEventBus attaches an event bus to the local backend for future event emission.
func (b *LocalBackend) SetEventBus(bus events.Bus) {
	b.events = bus
}

// localHandle wraps an exec.Cmd and implements AgentHandle.
type localHandle struct {
	cmd       *exec.Cmd
	stdout    *bytes.Buffer
	startTime time.Time
	done      chan struct{}
	waitErr   error
	waitOnce  sync.Once
}

func (h *localHandle) Wait() error {
	<-h.done
	return h.waitErr
}

func (h *localHandle) Output() io.Reader {
	return h.stdout
}

func (h *localHandle) ExitCode() int {
	select {
	case <-h.done:
	default:
		return -1
	}
	if h.waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(h.waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func (h *localHandle) StartTime() time.Time {
	return h.startTime
}

// Spawn starts a local process with the command and args from opts.
// Rejects any HostEnv entry with SecretRef (defense-in-depth layer 2).
func (b *LocalBackend) Spawn(ctx context.Context, opts SpawnOpts) (AgentHandle, error) {
	if opts.Command == "" {
		return nil, fmt.Errorf("runtime/local: Command is required")
	}

	// Defense-in-depth: reject SecretRef entries for local execution.
	for _, e := range opts.HostEnv {
		if e.SecretRef != nil {
			return nil, fmt.Errorf("runtime/local: env %s has secret_ref which is not supported for local execution", e.Name)
		}
	}

	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	cmd.Dir = opts.WorkDir

	if len(opts.Env) > 0 || len(opts.HostEnv) > 0 {
		env := cmd.Environ()
		env = append(env, opts.Env...)
		for _, e := range opts.HostEnv {
			env = append(env, e.Name+"="+e.Value)
		}
		cmd.Env = env
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runtime/local: starting %s: %w", opts.Command, err)
	}

	h := &localHandle{
		cmd:       cmd,
		stdout:    &stdout,
		startTime: time.Now(),
		done:      make(chan struct{}),
	}

	// Background goroutine to wait for completion.
	go func() {
		h.waitErr = cmd.Wait()
		close(h.done)
	}()

	b.mu.Lock()
	b.handles[opts.ID] = h
	b.mu.Unlock()

	return h, nil
}

// Kill terminates a running local agent process.
func (b *LocalBackend) Kill(ctx context.Context, id string) error {
	b.mu.Lock()
	h, ok := b.handles[id]
	b.mu.Unlock()

	if !ok {
		return fmt.Errorf("runtime/local: agent %q: %w", id, ErrNotFound)
	}

	if h.cmd.Process == nil {
		return fmt.Errorf("runtime/local: agent %q has no process", id)
	}

	if err := h.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("runtime/local: killing agent %q: %w", id, err)
	}

	// Wait for the done channel so the handle is fully cleaned up.
	<-h.done

	b.mu.Lock()
	delete(b.handles, id)
	b.mu.Unlock()

	return nil
}

// Status returns the execution status of a local agent.
func (b *LocalBackend) Status(ctx context.Context, id string) (model.AgentStatus, error) {
	b.mu.Lock()
	h, ok := b.handles[id]
	b.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("runtime/local: agent %q not found", id)
	}

	select {
	case <-h.done:
		if h.waitErr != nil {
			return model.StatusFailed, nil
		}
		return model.StatusCompleted, nil
	default:
		return model.StatusRunning, nil
	}
}

// Logs returns recent output for a local agent. For live agents, returns
// the in-memory stdout buffer. For completed/absent agents, falls back to
// the log file at {root}/.fracta/logs/{id}.log.
func (b *LocalBackend) Logs(_ context.Context, id string, tailLines int) (string, error) {
	b.mu.Lock()
	h, ok := b.handles[id]
	b.mu.Unlock()

	// If we have a live handle, return its stdout buffer.
	if ok {
		return h.stdout.String(), nil
	}

	// Fall back to log file.
	if b.root == "" {
		return "", fmt.Errorf("runtime/local: agent %q: %w", id, ErrNotFound)
	}

	logFile := filepath.Join(b.root, model.FractaDir, model.LogsDir, id+".log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("runtime/local: agent %q log not found: %w", id, ErrNotFound)
		}
		return "", fmt.Errorf("runtime/local: reading log for %q: %w", id, err)
	}

	// If tailLines requested, return only the last N lines.
	if tailLines > 0 {
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
		return string(bytes.Join(lines, []byte("\n"))), nil
	}

	return string(data), nil
}
