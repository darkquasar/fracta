package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
	"github.com/darkquasar/fracta/internal/worker"
)

// trackingHost records which missions were dispatched to it.
type trackingHost struct {
	name        string
	dispatched  atomic.Int32
	commandSpec host.CommandSpec
	parseResult host.Result
}

func (h *trackingHost) WriteWorkspace(_ string, _ []string, _ host.WorkspaceConfig) error {
	return nil
}

func (h *trackingHost) Bootstrap(_, _, _ string) host.BootstrapResult {
	return host.BootstrapResult{InitialPrompt: "run"}
}

func (h *trackingHost) BuildBatchCommand(_, _, _ string) host.CommandSpec {
	h.dispatched.Add(1)
	return h.commandSpec
}

func (h *trackingHost) ParseBatchOutput(_ []byte, _ error) (host.Result, error) {
	return h.parseResult, nil
}

func (h *trackingHost) StartStream(_, _, _ string) (host.StreamSession, error) {
	return nil, host.ErrStreamNotSupported
}

func (h *trackingHost) Capabilities() host.Capabilities {
	return host.Capabilities{}
}

var _ host.Host = (*trackingHost)(nil)

func testStore(t *testing.T) *sqlitestore.SQLiteStore {
	t.Helper()
	s, err := sqlitestore.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkPayload(t *testing.T, hostType string) json.RawMessage {
	t.Helper()
	p := queue.MissionPayload{Task: "task", RuntimeType: hostType}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestIntegration_FullLifecycle tests the end-to-end flow:
// enqueue missions -> workers execute -> agents reach terminal state.
func TestIntegration_FullLifecycle(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	hostClaude := &trackingHost{
		name:        "claude",
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"claude-result"}},
		parseResult: host.Result{Output: "claude completed"},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", hostClaude)

	wsBase := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start 2 workers.
	for i := 0; i < 2; i++ {
		w := worker.New(fmt.Sprintf("w-%d", i), q, store, reg, wsBase,
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithBackend(runtime.NewLocalBackend()))
		go w.Run(ctx)
	}

	// Enqueue 3 missions.
	for i := 0; i < 3; i++ {
		task := fmt.Sprintf("agent-%d", i)
		m := &queue.Mission{AgentTask: task, Payload: mkPayload(t, "claude")}
		a := &model.AgentEntry{
			Task:     task,
			RuntimeType: "claude",
			Status:   model.StatusQueued,
			Mode:     "queued",
		}
		if err := q.Enqueue(ctx, m, a); err != nil {
			t.Fatal(err)
		}
	}

	// Wait for all agents to reach terminal state.
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for all missions to complete")
		default:
		}

		st, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}

		completed := 0
		for _, a := range st.Agents {
			if a.Status == model.StatusCompleted || a.Status == model.StatusFailed {
				completed++
			}
		}
		if completed == 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify all completed.
	st, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range st.Agents {
		if a.Status != model.StatusCompleted {
			t.Errorf("agent %s status = %q, want Completed", a.Task, a.Status)
		}
		if a.LastOutput != "claude completed" {
			t.Errorf("agent %s output = %q, want %q", a.Task, a.LastOutput, "claude completed")
		}
	}

	// Verify host was called 3 times.
	if got := hostClaude.dispatched.Load(); got != 3 {
		t.Errorf("claude dispatched = %d, want 3", got)
	}
}

// TestIntegration_MultiHostDispatch tests that missions with different
// HostTypes are dispatched to the correct host implementations.
func TestIntegration_MultiHostDispatch(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	hostAlpha := &trackingHost{
		name:        "alpha",
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"alpha"}},
		parseResult: host.Result{Output: "from alpha"},
	}
	hostBeta := &trackingHost{
		name:        "beta",
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"beta"}},
		parseResult: host.Result{Output: "from beta"},
	}

	reg := host.NewMapRegistry("alpha")
	reg.Register("alpha", hostAlpha)
	reg.Register("beta", hostBeta)

	wsBase := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start workers.
	for i := 0; i < 2; i++ {
		w := worker.New(fmt.Sprintf("w-%d", i), q, store, reg, wsBase,
			worker.WithPollInterval(50*time.Millisecond),
			worker.WithBackend(runtime.NewLocalBackend()))
		go w.Run(ctx)
	}

	// Enqueue: 2 alpha missions, 1 beta mission.
	for i := 0; i < 2; i++ {
		task := fmt.Sprintf("alpha-%d", i)
		m := &queue.Mission{AgentTask: task, Payload: mkPayload(t, "alpha")}
		a := &model.AgentEntry{Task: task, RuntimeType: "alpha", Status: model.StatusQueued, Mode: "queued"}
		if err := q.Enqueue(ctx, m, a); err != nil {
			t.Fatal(err)
		}
	}
	{
		m := &queue.Mission{AgentTask: "beta-0", Payload: mkPayload(t, "beta")}
		a := &model.AgentEntry{Task: "beta-0", RuntimeType: "beta", Status: model.StatusQueued, Mode: "queued"}
		if err := q.Enqueue(ctx, m, a); err != nil {
			t.Fatal(err)
		}
	}

	// Wait for all 3 to complete.
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for multi-host missions")
		default:
		}

		st, _ := store.Load(ctx)
		completed := 0
		for _, a := range st.Agents {
			if a.Status == model.StatusCompleted || a.Status == model.StatusFailed {
				completed++
			}
		}
		if completed == 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify dispatch counts.
	if got := hostAlpha.dispatched.Load(); got != 2 {
		t.Errorf("alpha dispatched = %d, want 2", got)
	}
	if got := hostBeta.dispatched.Load(); got != 1 {
		t.Errorf("beta dispatched = %d, want 1", got)
	}

	// Verify outputs match the host.
	for i := 0; i < 2; i++ {
		a, _ := store.FindAgent(ctx, fmt.Sprintf("alpha-%d", i))
		if a.LastOutput != "from alpha" {
			t.Errorf("alpha-%d output = %q, want %q", i, a.LastOutput, "from alpha")
		}
	}
	a, _ := store.FindAgent(ctx, "beta-0")
	if a.LastOutput != "from beta" {
		t.Errorf("beta-0 output = %q, want %q", a.LastOutput, "from beta")
	}
}

// TestIntegration_KillCancelsExecution tests that killing a queued agent
// cancels the mission and the worker stops execution.
func TestIntegration_KillCancelsExecution(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	// This host runs "sleep 60" — the cancellation should kill it before completion.
	slowHost := &trackingHost{
		name:        "slow",
		commandSpec: host.CommandSpec{Command: "sleep", Args: []string{"60"}},
		parseResult: host.Result{Output: "should not reach this"},
	}

	reg := host.NewMapRegistry("slow")
	reg.Register("slow", slowHost)

	wsBase := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	w := worker.New("kill-test", q, store, reg, wsBase,
		worker.WithPollInterval(200*time.Millisecond),
		worker.WithBackend(runtime.NewLocalBackend()))
	go w.Run(ctx)

	// Enqueue a slow mission.
	m := &queue.Mission{AgentTask: "slow-agent", Payload: mkPayload(t, "slow")}
	a := &model.AgentEntry{Task: "slow-agent", RuntimeType: "slow", Status: model.StatusQueued, Mode: "queued"}
	if err := q.Enqueue(ctx, m, a); err != nil {
		t.Fatal(err)
	}

	// Wait for the agent to be claimed (Running).
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for agent to start running")
		default:
		}
		agent, _ := store.FindAgent(ctx, "slow-agent")
		if agent != nil && agent.Status == model.StatusRunning {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Simulate the kill path: cancel the mission in the queue.
	if err := q.Cancel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	// Mark agent as Stopped (what kill.go does).
	store.UpdateAgentStatus(ctx, "slow-agent", model.StatusStopped, "killed")

	// Wait for the worker to detect cancellation and finish.
	deadline = time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for cancelled mission to finalize")
		default:
		}

		agent, _ := store.FindAgent(ctx, "slow-agent")
		if agent != nil && agent.Status == model.StatusStopped {
			// The worker should have seen the Stopped status and not overwritten it.
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
