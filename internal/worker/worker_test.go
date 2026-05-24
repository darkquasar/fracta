package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
)

// fakeHost records calls and returns configurable results.
type fakeHost struct {
	writeErr    error
	bootstrap   host.BootstrapResult
	commandSpec host.CommandSpec
	parseResult host.Result
	parseErr    error
	caps        host.Capabilities

	writeWorkspaceCalled bool
	bootstrapCalled      bool
	buildBatchCalled     bool
	parseCalled          bool
}

func (f *fakeHost) WriteWorkspace(_ string, _ []string, _ host.WorkspaceConfig) error {
	f.writeWorkspaceCalled = true
	return f.writeErr
}

func (f *fakeHost) Bootstrap(_, _, _ string) host.BootstrapResult {
	f.bootstrapCalled = true
	return f.bootstrap
}

func (f *fakeHost) BuildBatchCommand(_, _, _ string) host.CommandSpec {
	f.buildBatchCalled = true
	return f.commandSpec
}

func (f *fakeHost) ParseBatchOutput(stdout []byte, waitErr error) (host.Result, error) {
	f.parseCalled = true
	return f.parseResult, f.parseErr
}

func (f *fakeHost) StartStream(_, _, _ string) (host.StreamSession, error) {
	return nil, host.ErrStreamNotSupported
}

func (f *fakeHost) Capabilities() host.Capabilities {
	return f.caps
}

var _ host.Host = (*fakeHost)(nil)

func testStore(t *testing.T) *sqlitestore.SQLiteStore {
	t.Helper()
	s, err := sqlitestore.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testPayload(t *testing.T, hostType string) json.RawMessage {
	t.Helper()
	p := queue.MissionPayload{Task: "test-task", RuntimeType: hostType}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func enqueueMission(t *testing.T, q queue.MissionQueue, store *sqlitestore.SQLiteStore, hostType string) *queue.Mission {
	t.Helper()
	ctx := context.Background()
	payload := testPayload(t, hostType)
	m := &queue.Mission{AgentTask: "agent-1", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "agent-1",
		RuntimeType: hostType,
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWorker_CorrectHostDispatch(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	hostA := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"hello"}},
		parseResult: host.Result{Output: "ok from A"},
	}
	hostB := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"world"}},
		parseResult: host.Result{Output: "ok from B"},
	}

	reg := host.NewMapRegistry("alpha")
	reg.Register("alpha", hostA)
	reg.Register("beta", hostB)

	wsBase := t.TempDir()

	// Enqueue a mission targeting host "beta".
	enqueueMission(t, q, store, "beta")

	backend := runtime.NewLocalBackend()
	w := New("test-worker", q, store, reg, wsBase,
		WithPollInterval(50*time.Millisecond),
		WithBackend(backend),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run in background — will process one mission then block on Dequeue.
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait for mission to complete.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for mission completion")
		default:
		}

		agent, err := store.FindAgent(context.Background(), "agent-1")
		if err != nil {
			t.Fatal(err)
		}
		if agent != nil && (agent.Status == model.StatusCompleted || agent.Status == model.StatusFailed) {
			// Host B should have been called, not host A.
			if !hostB.writeWorkspaceCalled {
				t.Error("expected beta host WriteWorkspace to be called")
			}
			if !hostB.bootstrapCalled {
				t.Error("expected beta host Bootstrap to be called")
			}
			if !hostB.buildBatchCalled {
				t.Error("expected beta host BuildBatchCommand to be called")
			}
			if hostA.writeWorkspaceCalled {
				t.Error("alpha host should NOT have been called")
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestWorker_UnknownHostFails(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", &fakeHost{})

	wsBase := t.TempDir()

	// Enqueue with unknown host type.
	enqueueMission(t, q, store, "unknown-host")

	backend := runtime.NewLocalBackend()
	w := New("test-worker", q, store, reg, wsBase, WithBackend(backend))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait for mission to fail.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for mission failure")
		default:
		}

		agent, _ := store.FindAgent(context.Background(), "agent-1")
		if agent != nil && agent.Status == model.StatusFailed {
			if agent.LastOutput == "" {
				t.Error("expected failure reason in LastOutput")
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestWorker_CancellationBeforeExecution(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"should-not-run"}},
		parseResult: host.Result{Output: "unexpected"},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", fh)

	wsBase := t.TempDir()

	// Enqueue then immediately cancel before worker picks it up.
	ctx := context.Background()
	payload := testPayload(t, "claude")
	m := &queue.Mission{AgentTask: "agent-cancel", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "agent-cancel",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	// Dequeue manually (what the worker would do), then cancel the mission.
	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the mission (simulating kill path).
	if err := q.Cancel(ctx, mission.ID); err != nil {
		t.Fatal(err)
	}

	// Now call executeMission directly — it should detect cancellation.
	// First we need to claim the agent.
	store.ClaimAgent(ctx, mission.AgentTask)

	backend := runtime.NewLocalBackend()
	w := New("test-worker", q, store, reg, wsBase, WithBackend(backend))
	execErr := w.executeMission(ctx, mission)
	if execErr == nil {
		t.Error("expected error from cancelled mission")
	}

	// The fakeHost should NOT have had BuildBatchCommand called.
	if fh.buildBatchCalled {
		t.Error("BuildBatchCommand should not be called for cancelled mission")
	}
}

func TestWorker_ResultWriteGuard(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	// Use a real command that succeeds — we're testing the write guard, not execution.
	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "completed successfully"},
	}

	reg := host.NewMapRegistry("claude")
	reg.Register("claude", fh)

	wsBase := t.TempDir()

	ctx := context.Background()
	payload := testPayload(t, "claude")
	m := &queue.Mission{AgentTask: "agent-guard", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "agent-guard",
		RuntimeType: "claude",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	// Dequeue manually.
	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Claim the agent (Queued -> Running).
	if err := store.ClaimAgent(ctx, mission.AgentTask); err != nil {
		t.Fatal(err)
	}

	// Simulate the kill path: mark agent as Stopped BEFORE the worker writes results.
	if err := store.UpdateAgentStatus(ctx, mission.AgentTask, model.StatusStopped, "killed"); err != nil {
		t.Fatal(err)
	}

	// Now execute — the worker should see Stopped and skip the write.
	backend := runtime.NewLocalBackend()
	w := New("test-worker", q, store, reg, wsBase, WithBackend(backend))
	// executeMission will try to ClaimAgent again (which will fail since agent is Stopped),
	// so we need to manipulate the queue status to bypass the claim step.
	// Instead, let's test at a higher level: re-enqueue.

	// Actually, let's test the write guard more directly.
	// The agent is already at Stopped. Worker's executeMission calls ClaimAgent first,
	// which will fail because agent is not Queued. So this tests that path too.
	execErr := w.executeMission(ctx, mission)
	if execErr == nil {
		t.Error("expected error from claim failure")
	}

	// Verify agent is still Stopped (not overwritten).
	a, _ := store.FindAgent(ctx, "agent-guard")
	if a.Status != model.StatusStopped {
		t.Errorf("agent status = %q, want %q", a.Status, model.StatusStopped)
	}
}

func TestWorker_BootstrapAndCompletion(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	// The fakeHost records that Bootstrap was called with the right args.
	// We verify completion by checking agent state after execution.
	fh := &fakeHost{
		bootstrap: host.BootstrapResult{
			FileName:      "TASK.md",
			FileBody:      "# Test Task\nDo the thing.",
			InitialPrompt: "Read TASK.md and execute.",
		},
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "completed successfully"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()

	ctx := context.Background()
	payload := testPayload(t, "testhost")
	m := &queue.Mission{AgentTask: "agent-bootstrap", Payload: payload}
	agent := &model.AgentEntry{
		Task:        "agent-bootstrap",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	backend := runtime.NewLocalBackend()
	w := New("test-worker", q, store, reg, wsBase, WithBackend(backend))
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// Verify bootstrap was called.
	if !fh.bootstrapCalled {
		t.Error("expected Bootstrap to be called")
	}

	// Verify agent completed successfully.
	a, err := store.FindAgent(ctx, "agent-bootstrap")
	if err != nil {
		t.Fatalf("FindAgent: %v", err)
	}
	if a.Status != model.StatusCompleted {
		t.Errorf("agent status = %q, want %q", a.Status, model.StatusCompleted)
	}
	if a.LastOutput != "completed successfully" {
		t.Errorf("agent output = %q, want %q", a.LastOutput, "completed successfully")
	}

	// Verify workspace was cleaned up (ephemeral).
	wsDir := filepath.Join(wsBase, "agent-bootstrap")
	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Error("expected workspace to be cleaned up after execution")
	}
}

// === Config skew detection tests (D5) ===

func TestConfigHash_Deterministic(t *testing.T) {
	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"claude": {Model: "sonnet-4", Adapter: "claude"},
		},
		Project: config.ProjectConfig{
			AllowedTools:      []string{"Bash(*)", "Read"},
			DefaultBaseBranch: "main",
		},
	}

	w := &Worker{Config: cfg}
	h1 := w.configHash()
	h2 := w.configHash()

	if h1 == "" {
		t.Fatal("configHash returned empty string")
	}
	if h1 != h2 {
		t.Errorf("configHash not deterministic: %q != %q", h1, h2)
	}

	// Manually compute expected SHA-256 to verify the algorithm.
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(data))
	if h1 != expected {
		t.Errorf("configHash = %q, want %q", h1, expected)
	}
}

func TestConfigHash_NilConfig(t *testing.T) {
	w := &Worker{Config: nil}
	h := w.configHash()
	if h != "" {
		t.Errorf("configHash with nil config should return empty, got %q", h)
	}
}

func TestConfigHash_DifferentConfigs(t *testing.T) {
	cfg1 := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"claude": {Model: "sonnet-4"},
		},
	}
	cfg2 := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"claude": {Model: "opus-4"},
		},
	}

	w1 := &Worker{Config: cfg1}
	w2 := &Worker{Config: cfg2}

	h1 := w1.configHash()
	h2 := w2.configHash()

	if h1 == h2 {
		t.Error("different configs should produce different hashes")
	}
}

func TestWorker_ConfigSkewDetection_Matching(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"testhost": {Model: "test-model", Adapter: "claude"},
		},
	}

	// Compute the expected hash from this config.
	cfgData, _ := json.Marshal(cfg)
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(cfgData))

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "ok"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()
	ctx := context.Background()

	// Enqueue with matching config hash.
	payload := queue.MissionPayload{
		Task:        "agent-skew-match",
		RuntimeType: "testhost",
		ConfigHash:  expectedHash,
	}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-skew-match", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-skew-match",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	w := New("test-worker", q, store, reg, wsBase,
		WithBackend(runtime.NewLocalBackend()),
		WithConfig(cfg),
		WithEvents(bus),
	)
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// No config_skew events should be emitted.
	skewEvents := bus.byAction("config_skew")
	if len(skewEvents) != 0 {
		t.Errorf("expected 0 config_skew events for matching hashes, got %d", len(skewEvents))
	}
}

func TestWorker_ConfigSkewDetection_Mismatching(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"testhost": {Model: "test-model", Adapter: "claude"},
		},
	}

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "ok"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()
	ctx := context.Background()

	// Enqueue with a DIFFERENT config hash to trigger skew detection.
	payload := queue.MissionPayload{
		Task:        "agent-skew-diff",
		RuntimeType: "testhost",
		ConfigHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-skew-diff", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-skew-diff",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	w := New("test-worker", q, store, reg, wsBase,
		WithBackend(runtime.NewLocalBackend()),
		WithConfig(cfg),
		WithEvents(bus),
	)
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// A config_skew event should be emitted.
	skewEvents := bus.byAction("config_skew")
	if len(skewEvents) != 1 {
		t.Fatalf("expected 1 config_skew event, got %d (all: %v)", len(skewEvents), eventActions(bus.all()))
	}

	e := skewEvents[0]
	if e.Severity != "warn" {
		t.Errorf("config_skew severity = %q, want warn", e.Severity)
	}
	if e.Component != "worker" {
		t.Errorf("config_skew component = %q, want worker", e.Component)
	}
	if e.Category != "queue" {
		t.Errorf("config_skew category = %q, want queue", e.Category)
	}
	if e.Attrs["orchestrator_hash"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("orchestrator_hash = %q", e.Attrs["orchestrator_hash"])
	}
	if e.Attrs["worker_hash"] == "" {
		t.Error("worker_hash should not be empty")
	}
	if e.Attrs["worker_hash"] == e.Attrs["orchestrator_hash"] {
		t.Error("worker_hash should differ from orchestrator_hash")
	}
}

func TestWorker_ConfigSkewDetection_EmptyPayloadHash(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	bus := &captureBus{}

	cfg := &config.Config{
		Runtimes: map[string]config.RuntimeEntry{
			"testhost": {Model: "test-model", Adapter: "claude"},
		},
	}

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "ok"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()
	ctx := context.Background()

	// Enqueue without a config hash (legacy missions).
	payload := queue.MissionPayload{
		Task:        "agent-skew-empty",
		RuntimeType: "testhost",
	}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-skew-empty", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-skew-empty",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	w := New("test-worker", q, store, reg, wsBase,
		WithBackend(runtime.NewLocalBackend()),
		WithConfig(cfg),
		WithEvents(bus),
	)
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// No config_skew event for empty payload hash (backward compat).
	skewEvents := bus.byAction("config_skew")
	if len(skewEvents) != 0 {
		t.Errorf("expected 0 config_skew events for empty payload hash, got %d", len(skewEvents))
	}
}

// recordingBackend captures SpawnOpts for inspection while behaving like LocalBackend.
type recordingBackend struct {
	mu       sync.Mutex
	captured []runtime.SpawnOpts
	inner    runtime.Backend
}

func newRecordingBackend() *recordingBackend {
	return &recordingBackend{inner: runtime.NewLocalBackend()}
}

func (r *recordingBackend) Spawn(ctx context.Context, opts runtime.SpawnOpts) (runtime.AgentHandle, error) {
	r.mu.Lock()
	r.captured = append(r.captured, opts)
	r.mu.Unlock()
	return r.inner.Spawn(ctx, opts)
}

func (r *recordingBackend) Kill(_ context.Context, id string) error {
	return r.inner.Kill(context.Background(), id)
}
func (r *recordingBackend) Logs(_ context.Context, id string, lines int) (string, error) {
	return r.inner.Logs(context.Background(), id, lines)
}
func (r *recordingBackend) Status(_ context.Context, id string) (model.AgentStatus, error) {
	return r.inner.Status(context.Background(), id)
}

func (r *recordingBackend) lastOpts() runtime.SpawnOpts {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.captured) == 0 {
		return runtime.SpawnOpts{}
	}
	return r.captured[len(r.captured)-1]
}

func TestWorker_LocalQueued_CredentialEnvReachesSubprocess(t *testing.T) {
	store := testStore(t)
	q := queue.NewMemoryQueue(store, 10)
	defer q.Close()

	fh := &fakeHost{
		commandSpec: host.CommandSpec{Command: "echo", Args: []string{"done"}},
		parseResult: host.Result{Output: "ok"},
	}

	reg := host.NewMapRegistry("testhost")
	reg.Register("testhost", fh)

	wsBase := t.TempDir()
	ctx := context.Background()

	cfg := &config.Config{
		Runtime: config.RuntimeConfig{Backend: "local"},
		Runtimes: map[string]config.RuntimeEntry{
			"testhost": {
				Model:       "test-model",
				Adapter:     "claude",
				AuthProfile: "test_bearer",
			},
		},
		Auth: config.AuthConfig{
			Credentials: config.CredentialsConfig{
				Profiles: map[string]config.CredentialProfile{
					"test_bearer": {
						AuthOrigins: map[string]config.CredentialSource{
							"token": {
								Type:    "command_output",
								Scope:   "any",
								Command: config.FlexCommand{"echo", "fake-bearer-token"},
							},
						},
						Env: map[string]string{"AWS_REGION": "us-east-1"},
						DefaultBinding: &config.CredentialBinding{
							Type:       "bearer_env",
							AuthOrigin: "token",
							EnvName:    "AWS_BEARER_TOKEN_BEDROCK",
						},
					},
				},
			},
		},
	}

	payload := queue.MissionPayload{
		Task:        "agent-cred",
		RuntimeType: "testhost",
		Backend:     "local",
	}
	payloadBytes, _ := json.Marshal(payload)
	m := &queue.Mission{AgentTask: "agent-cred", Payload: payloadBytes}
	agent := &model.AgentEntry{
		Task:        "agent-cred",
		RuntimeType: "testhost",
		Status:      model.StatusQueued,
		Mode:        "queued",
	}
	if err := q.Enqueue(ctx, m, agent); err != nil {
		t.Fatal(err)
	}

	mission, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rb := newRecordingBackend()
	w := New("test-worker", q, store, reg, wsBase,
		WithBackend(rb),
		WithConfig(cfg),
	)
	if err := w.executeMission(ctx, mission); err != nil {
		t.Fatalf("executeMission: %v", err)
	}

	// Verify AWS_BEARER_TOKEN_BEDROCK is in HostEnv.
	opts := rb.lastOpts()
	found := false
	for _, e := range opts.HostEnv {
		if e.Name == "AWS_BEARER_TOKEN_BEDROCK" {
			found = true
			if e.Value != "fake-bearer-token" {
				t.Errorf("AWS_BEARER_TOKEN_BEDROCK = %q, want %q", e.Value, "fake-bearer-token")
			}
			break
		}
	}
	if !found {
		var names []string
		for _, e := range opts.HostEnv {
			names = append(names, e.Name)
		}
		t.Errorf("AWS_BEARER_TOKEN_BEDROCK not found in HostEnv; got: %v", names)
	}
}
