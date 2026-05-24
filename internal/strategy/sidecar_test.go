package strategy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
)

func TestStrategyInfoJSON(t *testing.T) {
	raw := `{"name":"correlate-ip","description":"Correlate an IP","tags":["hunt","network"],"params":{"ip":{"type":"string","required":true}},"file":"correlation/correlate_ip.py"}`

	var info StrategyInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Name != "correlate-ip" {
		t.Errorf("name = %q, want %q", info.Name, "correlate-ip")
	}
	if info.Description != "Correlate an IP" {
		t.Errorf("description = %q, want %q", info.Description, "Correlate an IP")
	}
	if len(info.Tags) != 2 || info.Tags[0] != "hunt" {
		t.Errorf("tags = %v, want [hunt network]", info.Tags)
	}
	if info.File != "correlation/correlate_ip.py" {
		t.Errorf("file = %q, want %q", info.File, "correlation/correlate_ip.py")
	}
	if info.Params == nil {
		t.Fatal("params is nil")
	}
	if _, ok := info.Params["ip"]; !ok {
		t.Error("params missing 'ip' key")
	}
}

func TestStepTraceJSON(t *testing.T) {
	raw := `{"name":"fetch_data","status":"ok","duration_ms":42}`

	var step StepTrace
	if err := json.Unmarshal([]byte(raw), &step); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if step.Name != "fetch_data" {
		t.Errorf("name = %q, want %q", step.Name, "fetch_data")
	}
	if step.Status != "ok" {
		t.Errorf("status = %q, want %q", step.Status, "ok")
	}
	if step.DurationMs != 42 {
		t.Errorf("duration_ms = %d, want 42", step.DurationMs)
	}
	if step.Error != "" {
		t.Errorf("error = %q, want empty", step.Error)
	}
}

func TestStepTraceErrorJSON(t *testing.T) {
	raw := `{"name":"analyze","status":"error","duration_ms":5,"error":"something broke"}`

	var step StepTrace
	if err := json.Unmarshal([]byte(raw), &step); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if step.Status != "error" {
		t.Errorf("status = %q, want %q", step.Status, "error")
	}
	if step.Error != "something broke" {
		t.Errorf("error = %q, want %q", step.Error, "something broke")
	}
}

func TestRunResultJSON(t *testing.T) {
	raw := `{
		"status": "ok",
		"result": {"message": "Hello, world!"},
		"trace": {
			"steps": [
				{"name": "greet", "status": "ok", "duration_ms": 1},
				{"name": "format", "status": "ok", "duration_ms": 2}
			],
			"total_duration_ms": 3
		}
	}`

	var result RunResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("status = %q, want %q", result.Status, "ok")
	}
	if result.Error != "" {
		t.Errorf("error = %q, want empty", result.Error)
	}
	if len(result.Trace.Steps) != 2 {
		t.Fatalf("trace steps = %d, want 2", len(result.Trace.Steps))
	}
	if result.Trace.TotalDurationMs != 3 {
		t.Errorf("total_duration_ms = %d, want 3", result.Trace.TotalDurationMs)
	}

	// Verify result is a map
	m, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result.Result)
	}
	if m["message"] != "Hello, world!" {
		t.Errorf("result.message = %v, want %q", m["message"], "Hello, world!")
	}
}

func TestRunResultErrorJSON(t *testing.T) {
	raw := `{
		"status": "error",
		"error": "Deliberate failure",
		"result": null,
		"trace": {
			"steps": [
				{"name": "step1", "status": "ok", "duration_ms": 1},
				{"name": "step2", "status": "error", "duration_ms": 0, "error": "Deliberate failure"}
			],
			"total_duration_ms": 1,
			"error": "Deliberate failure"
		}
	}`

	var result RunResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Error != "Deliberate failure" {
		t.Errorf("error = %q, want %q", result.Error, "Deliberate failure")
	}
	if len(result.Trace.Steps) != 2 {
		t.Fatalf("trace steps = %d, want 2", len(result.Trace.Steps))
	}
	if result.Trace.Steps[1].Status != "error" {
		t.Errorf("step[1].status = %q, want %q", result.Trace.Steps[1].Status, "error")
	}
}

func TestRunResultPartialResultsJSON(t *testing.T) {
	raw := `{
		"status": "error",
		"error": "step2 failed",
		"result": null,
		"partial_results": {"step1": {"count": 42}},
		"partial_results_truncated": true,
		"omitted_steps": ["step3"],
		"trace": {
			"steps": [
				{"name": "step1", "status": "ok", "duration_ms": 10},
				{"name": "step2", "status": "error", "duration_ms": 0, "error": "step2 failed"}
			],
			"total_duration_ms": 10,
			"error": "step2 failed"
		}
	}`

	var result RunResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.PartialResults == nil {
		t.Fatal("partial_results is nil")
	}
	pr, ok := result.PartialResults.(map[string]any)
	if !ok {
		t.Fatalf("partial_results type = %T, want map[string]any", result.PartialResults)
	}
	step1, ok := pr["step1"].(map[string]any)
	if !ok {
		t.Fatalf("partial_results.step1 type = %T", pr["step1"])
	}
	if step1["count"] != float64(42) {
		t.Errorf("partial_results.step1.count = %v, want 42", step1["count"])
	}
	if !result.PartialResultsTruncated {
		t.Error("partial_results_truncated = false, want true")
	}
	if len(result.OmittedSteps) != 1 || result.OmittedSteps[0] != "step3" {
		t.Errorf("omitted_steps = %v, want [step3]", result.OmittedSteps)
	}
}

func TestRunResultPartialResultsOmittedJSON(t *testing.T) {
	// When there are no partial results (success case), fields should be omitted
	raw := `{
		"status": "ok",
		"result": {"message": "done"},
		"trace": {"steps": [], "total_duration_ms": 1}
	}`

	var result RunResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.PartialResults != nil {
		t.Errorf("partial_results = %v, want nil", result.PartialResults)
	}
	if result.PartialResultsTruncated {
		t.Error("partial_results_truncated = true, want false")
	}
	if result.OmittedSteps != nil {
		t.Errorf("omitted_steps = %v, want nil", result.OmittedSteps)
	}

	// Verify omitempty works in marshal direction
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw2 map[string]any
	json.Unmarshal(data, &raw2)
	if _, ok := raw2["partial_results"]; ok {
		t.Error("partial_results should be omitted in JSON when nil")
	}
	if _, ok := raw2["omitted_steps"]; ok {
		t.Error("omitted_steps should be omitted in JSON when nil")
	}
}

func TestWithRunTimeout(t *testing.T) {
	s := &Sidecar{}
	WithRunTimeout(120 * time.Second)(s)
	if s.runTimeout != 120*time.Second {
		t.Errorf("runTimeout = %v, want 120s", s.runTimeout)
	}
	if s.RunTimeout() != 120*time.Second {
		t.Errorf("RunTimeout() = %v, want 120s", s.RunTimeout())
	}
}

func TestRunTimeoutDefault(t *testing.T) {
	s := &Sidecar{}
	if s.RunTimeout() != DefaultRunTimeout {
		t.Errorf("RunTimeout() = %v, want %v", s.RunTimeout(), DefaultRunTimeout)
	}
}

func TestDefaultTimeoutValues(t *testing.T) {
	if DefaultRunTimeout != 300*time.Second {
		t.Errorf("DefaultRunTimeout = %v, want 300s", DefaultRunTimeout)
	}
	if DefaultMethodTimeout != 30*time.Second {
		t.Errorf("DefaultMethodTimeout = %v, want 30s", DefaultMethodTimeout)
	}
}

func TestListResponseJSON(t *testing.T) {
	raw := `{"status":"ok","strategies":[{"name":"a","description":"desc","tags":["t"],"params":{},"file":"a.py"}]}`

	var resp listResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if len(resp.Strategies) != 1 {
		t.Fatalf("strategies = %d, want 1", len(resp.Strategies))
	}
	if resp.Strategies[0].Name != "a" {
		t.Errorf("name = %q, want %q", resp.Strategies[0].Name, "a")
	}
}

func TestDescribeResponseJSON(t *testing.T) {
	raw := `{"status":"ok","strategy":{"name":"b","description":"desc b","tags":[],"params":{"x":{"type":"int"}},"file":"b.py"}}`

	var resp describeResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.Strategy.Name != "b" {
		t.Errorf("name = %q, want %q", resp.Strategy.Name, "b")
	}
	if resp.Strategy.Params == nil {
		t.Fatal("params is nil")
	}
}

func TestCreateResponseJSON(t *testing.T) {
	raw := `{"status":"ok"}`
	var resp createResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.Error != "" {
		t.Errorf("error = %q, want empty", resp.Error)
	}
}

func TestCreateResponseErrorJSON(t *testing.T) {
	raw := `{"status":"error","error":"syntax error in line 5"}`
	var resp createResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "error" {
		t.Errorf("status = %q, want %q", resp.Status, "error")
	}
	if resp.Error != "syntax error in line 5" {
		t.Errorf("error = %q", resp.Error)
	}
}

func TestWithGatewayAccess(t *testing.T) {
	s := &Sidecar{}
	WithGatewayAccess("http://gw:8080", "agent-1")(s)
	if s.gatewayURL != "http://gw:8080" {
		t.Errorf("gatewayURL = %q", s.gatewayURL)
	}
	if s.agentTask != "agent-1" {
		t.Errorf("agentTask = %q", s.agentTask)
	}
}

func TestWithGraphAddr(t *testing.T) {
	s := &Sidecar{}
	WithGraphAddr("localhost:6379")(s)
	if s.graphAddr != "localhost:6379" {
		t.Errorf("graphAddr = %q", s.graphAddr)
	}
}

func TestWithUVBin(t *testing.T) {
	s := &Sidecar{}
	WithUVBin("/usr/local/bin/uv")(s)
	if s.uvBin != "/usr/local/bin/uv" {
		t.Errorf("uvBin = %q", s.uvBin)
	}
}

func TestWithStagingDir(t *testing.T) {
	s := &Sidecar{}
	WithStagingDir("/custom/staging")(s)
	if s.stagingDir != "/custom/staging" {
		t.Errorf("stagingDir = %q, want %q", s.stagingDir, "/custom/staging")
	}
}

func TestStagingManifestEntryJSON(t *testing.T) {
	entry := StagingManifestEntry{
		Mode:        "mcp_client",
		Required:    true,
		Staged:      true,
		ParquetPath: "/tmp/fracta-staging/a1b2c3/alerts.parquet",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded StagingManifestEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Mode != "mcp_client" {
		t.Errorf("mode = %q, want %q", decoded.Mode, "mcp_client")
	}
	if !decoded.Required {
		t.Error("required = false, want true")
	}
	if !decoded.Staged {
		t.Error("staged = false, want true")
	}
	if decoded.ParquetPath != "/tmp/fracta-staging/a1b2c3/alerts.parquet" {
		t.Errorf("parquet_path = %q", decoded.ParquetPath)
	}
}

func TestStagingManifestEntryNativeJSON(t *testing.T) {
	entry := StagingManifestEntry{
		Mode:     "native",
		Required: false,
		Staged:   false,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// parquet_path should be omitted for native entries
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["parquet_path"]; ok {
		t.Error("parquet_path should be omitted for native entries")
	}
}

func TestRunRequestIncludesManifest(t *testing.T) {
	// Simulate what Run() builds internally
	manifest := StagingManifest{
		"alerts": StagingManifestEntry{
			Mode:        "mcp_client",
			Required:    true,
			Staged:      true,
			ParquetPath: "/tmp/fracta-staging/a1b2c3/alerts.parquet",
		},
		"enrichment": StagingManifestEntry{
			Mode:     "native",
			Required: false,
			Staged:   false,
		},
	}

	req := map[string]any{
		"action":           "run",
		"strategy":         "test-strategy",
		"params":           map[string]any{"days_back": 7},
		"staging_manifest": manifest,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["action"] != "run" {
		t.Errorf("action = %v", decoded["action"])
	}
	if decoded["strategy"] != "test-strategy" {
		t.Errorf("strategy = %v", decoded["strategy"])
	}

	sm, ok := decoded["staging_manifest"].(map[string]any)
	if !ok {
		t.Fatalf("staging_manifest type = %T, want map", decoded["staging_manifest"])
	}
	if len(sm) != 2 {
		t.Fatalf("staging_manifest len = %d, want 2", len(sm))
	}

	alerts, ok := sm["alerts"].(map[string]any)
	if !ok {
		t.Fatalf("alerts type = %T", sm["alerts"])
	}
	if alerts["mode"] != "mcp_client" {
		t.Errorf("alerts.mode = %v", alerts["mode"])
	}
	if alerts["staged"] != true {
		t.Errorf("alerts.staged = %v", alerts["staged"])
	}
	if alerts["parquet_path"] != "/tmp/fracta-staging/a1b2c3/alerts.parquet" {
		t.Errorf("alerts.parquet_path = %v", alerts["parquet_path"])
	}

	enrichment, ok := sm["enrichment"].(map[string]any)
	if !ok {
		t.Fatalf("enrichment type = %T", sm["enrichment"])
	}
	if enrichment["mode"] != "native" {
		t.Errorf("enrichment.mode = %v", enrichment["mode"])
	}
	if enrichment["staged"] != false {
		t.Errorf("enrichment.staged = %v", enrichment["staged"])
	}
}

func TestRunRequestNilManifest(t *testing.T) {
	// When manifest is nil, staging_manifest should not appear in JSON
	req := map[string]any{
		"action":   "run",
		"strategy": "test-strategy",
		"params":   map[string]any{},
	}
	// manifest is nil — not added to req (mirrors Run() behavior)

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := decoded["staging_manifest"]; ok {
		t.Error("staging_manifest should not be present when nil")
	}
}

// ---------------------------------------------------------------------------
// S1: isTransportError
// ---------------------------------------------------------------------------

func TestIsTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"io.EOF", io.EOF, true},
		{"io.ErrClosedPipe", io.ErrClosedPipe, true},
		{"net.ErrClosed", net.ErrClosed, true},
		{"EPIPE", syscall.EPIPE, true},
		{"ECONNRESET", syscall.ECONNRESET, true},
		{"ECONNREFUSED", syscall.ECONNREFUSED, true},
		{"broken pipe string", errors.New("write: broken pipe"), true},
		{"connection reset string", errors.New("connection reset by peer"), true},
		{"EOF in message", errors.New("read: unexpected EOF"), true},
		{"random error", errors.New("something else"), false},
		{"timeout", errors.New("i/o timeout"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransportError(tt.err)
			if got != tt.want {
				t.Errorf("isTransportError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// S1: Error types
// ---------------------------------------------------------------------------

func TestSidecarTransportError(t *testing.T) {
	orig := errors.New("broken pipe")
	err := &SidecarTransportError{Err: orig, Phase: "write"}

	if !errors.Is(err, orig) {
		t.Error("SidecarTransportError should unwrap to original error")
	}
	if err.Error() != "sidecar transport (write): broken pipe" {
		t.Errorf("unexpected message: %s", err.Error())
	}

	var te *SidecarTransportError
	if !errors.As(err, &te) {
		t.Error("errors.As should match *SidecarTransportError")
	}
}

func TestSidecarRestartedError(t *testing.T) {
	orig := errors.New("broken pipe")
	err := &SidecarRestartedError{Err: orig}

	if !errors.Is(err, orig) {
		t.Error("SidecarRestartedError should unwrap to original error")
	}
	if err.Error() != "sidecar restarted after transport error: broken pipe" {
		t.Errorf("unexpected message: %s", err.Error())
	}

	var re *SidecarRestartedError
	if !errors.As(err, &re) {
		t.Error("errors.As should match *SidecarRestartedError")
	}
}

// ---------------------------------------------------------------------------
// S1: Health field accessors
// ---------------------------------------------------------------------------

func TestSidecar_HealthyDefault(t *testing.T) {
	s := &Sidecar{}
	if s.Healthy() {
		t.Error("zero-value Sidecar should not be healthy")
	}
}

func TestSidecar_HealthTracking(t *testing.T) {
	s := &Sidecar{healthy: true}
	if !s.Healthy() {
		t.Error("expected healthy=true")
	}

	now := time.Now()
	s.mu.Lock()
	s.healthy = false
	s.lastFailure = now
	s.mu.Unlock()

	if s.Healthy() {
		t.Error("expected healthy=false after failure")
	}
	if !s.LastFailure().Equal(now) {
		t.Errorf("lastFailure = %v, want %v", s.LastFailure(), now)
	}
}

func TestSidecar_RestartsCounter(t *testing.T) {
	s := &Sidecar{}
	if s.Restarts() != 0 {
		t.Errorf("restarts = %d, want 0", s.Restarts())
	}
	s.mu.Lock()
	s.restarts = 3
	s.mu.Unlock()
	if s.Restarts() != 3 {
		t.Errorf("restarts = %d, want 3", s.Restarts())
	}
}

// ---------------------------------------------------------------------------
// S1: withRetry
// ---------------------------------------------------------------------------

func TestWithRetry_NoError(t *testing.T) {
	s := &Sidecar{healthy: true}
	called := 0
	err := s.withRetry(func() error {
		called++
		return nil
	}, true, false)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Errorf("called %d times, want 1", called)
	}
}

func TestWithRetry_NonTransportError(t *testing.T) {
	s := &Sidecar{healthy: true}
	orig := errors.New("bad request")
	err := s.withRetry(func() error {
		return orig
	}, true, false)
	if !errors.Is(err, orig) {
		t.Errorf("expected original error, got %v", err)
	}
}

func testSidecar() *Sidecar {
	return &Sidecar{
		healthy: true,
		logger:  fractalog.Component("sidecar-test"),
	}
}

func TestWithRetry_TransportError_ReadOnly_RestartFails(t *testing.T) {
	// When restart fails (no process to restart), the original transport error
	// should be returned.
	s := testSidecar()
	transportErr := &SidecarTransportError{Err: errors.New("broken pipe"), Phase: "write"}
	err := s.withRetry(func() error {
		return transportErr
	}, true, false)

	// restart() will fail because there's no process, so we get the transport error back.
	var te *SidecarTransportError
	if !errors.As(err, &te) {
		t.Errorf("expected SidecarTransportError, got %T: %v", err, err)
	}
}

func TestWithRetry_TransportError_WriteOp_RestartFails(t *testing.T) {
	s := testSidecar()
	transportErr := &SidecarTransportError{Err: errors.New("broken pipe"), Phase: "write"}
	err := s.withRetry(func() error {
		return transportErr
	}, false, false)

	// restart() will fail, so we get transport error (not SidecarRestartedError).
	var te *SidecarTransportError
	if !errors.As(err, &te) {
		t.Errorf("expected SidecarTransportError when restart fails, got %T: %v", err, err)
	}
}

func TestWithRetry_WritePhase_Retries(t *testing.T) {
	// Write-phase transport error + successful restart → fn should be retried.
	sockPath := fmt.Sprintf("/tmp/fracta-test-wr-%d.sock", os.Getpid())
	defer os.Remove(sockPath)
	ln := startMockSocket(t, sockPath)
	defer ln.Close()

	sc, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err != nil {
		t.Fatalf("NewSidecar: %v", err)
	}
	defer sc.Close()

	calls := 0
	err = sc.withRetry(func() error {
		calls++
		if calls == 1 {
			// First call: simulate write-phase transport error.
			return &SidecarTransportError{Err: errors.New("broken pipe"), Phase: "write"}
		}
		// Second call (retry): succeed.
		return nil
	}, false, true) // retryOnWrite=true

	if err != nil {
		t.Errorf("expected nil after write-phase retry, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected fn called 2 times (original + retry), got %d", calls)
	}
}

func TestWithRetry_ReadPhase_NoRetry(t *testing.T) {
	// Read-phase transport error + successful restart → should NOT retry, return SidecarRestartedError.
	sockPath := fmt.Sprintf("/tmp/fracta-test-rd-%d.sock", os.Getpid())
	defer os.Remove(sockPath)
	ln := startMockSocket(t, sockPath)
	defer ln.Close()

	sc, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err != nil {
		t.Fatalf("NewSidecar: %v", err)
	}
	defer sc.Close()

	calls := 0
	err = sc.withRetry(func() error {
		calls++
		return &SidecarTransportError{Err: errors.New("EOF"), Phase: "read"}
	}, false, true) // retryOnWrite=true, but phase is "read"

	var restarted *SidecarRestartedError
	if !errors.As(err, &restarted) {
		t.Errorf("expected SidecarRestartedError on read-phase, got %T: %v", err, err)
	}
	if calls != 1 {
		t.Errorf("expected fn called 1 time (no retry on read-phase), got %d", calls)
	}
}

func TestSidecarTransportError_Phase(t *testing.T) {
	writeErr := &SidecarTransportError{Err: errors.New("broken pipe"), Phase: "write"}
	if got := writeErr.Error(); !strings.Contains(got, "(write)") {
		t.Errorf("expected phase in error string, got: %s", got)
	}
	readErr := &SidecarTransportError{Err: errors.New("EOF"), Phase: "read"}
	if got := readErr.Error(); !strings.Contains(got, "(read)") {
		t.Errorf("expected phase in error string, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// S2: Pool pick() health awareness
// ---------------------------------------------------------------------------

func TestPool_PickSkipsUnhealthy(t *testing.T) {
	s1 := &Sidecar{healthy: false, lastFailure: time.Now()}
	s2 := &Sidecar{healthy: true}
	s3 := &Sidecar{healthy: false, lastFailure: time.Now()}

	pool := &SidecarPool{sidecars: []*Sidecar{s1, s2, s3}}

	for i := 0; i < 10; i++ {
		sc := pool.pick()
		if sc != s2 {
			t.Fatalf("iteration %d: picked unhealthy sidecar", i)
		}
	}
}

func TestPool_PickAllUnhealthy_PicksOldestFailure(t *testing.T) {
	now := time.Now()
	s1 := &Sidecar{healthy: false, lastFailure: now.Add(-10 * time.Minute)}
	s2 := &Sidecar{healthy: false, lastFailure: now.Add(-1 * time.Minute)}
	s3 := &Sidecar{healthy: false, lastFailure: now.Add(-5 * time.Minute)}

	pool := &SidecarPool{sidecars: []*Sidecar{s1, s2, s3}}

	sc := pool.pick()
	if sc != s1 {
		t.Error("when all unhealthy, should pick sidecar with oldest lastFailure")
	}
}

func TestPool_PickAllHealthy_RoundRobin(t *testing.T) {
	s1 := &Sidecar{healthy: true}
	s2 := &Sidecar{healthy: true}
	s3 := &Sidecar{healthy: true}

	pool := &SidecarPool{sidecars: []*Sidecar{s1, s2, s3}}

	seen := map[*Sidecar]bool{}
	for i := 0; i < 6; i++ {
		sc := pool.pick()
		seen[sc] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected all 3 sidecars used in round-robin, got %d", len(seen))
	}
}

// ---------------------------------------------------------------------------
// External mode tests (A6)
// ---------------------------------------------------------------------------

// startMockSocket creates a Unix socket listener at path and returns the
// listener. The caller must close it.
func startMockSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("mock socket listen: %v", err)
	}
	// Accept connections in background (echo server for sendRecv tests).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer c.Close()
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					// Echo back a minimal JSON response.
					c.Write([]byte(`{"status":"ok"}` + "\n"))
				}
			}(conn)
		}
	}()
	return ln
}

func TestSidecar_ExternalSocket(t *testing.T) {
	sockPath := t.TempDir() + "/external.sock"
	ln := startMockSocket(t, sockPath)
	defer ln.Close()

	sc, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err != nil {
		t.Fatalf("NewSidecar external: %v", err)
	}
	defer sc.Close()

	// Verify it connected without spawning a subprocess.
	if sc.cmd != nil {
		t.Error("external mode should not spawn a subprocess")
	}
	if sc.conn == nil {
		t.Fatal("external mode should have a connection")
	}
	if !sc.Healthy() {
		t.Error("newly created external sidecar should be healthy")
	}
	if !sc.externalMode {
		t.Error("externalMode field should be true")
	}
}

func TestSidecar_ExternalRestart(t *testing.T) {
	sockPath := t.TempDir() + "/restart.sock"
	ln := startMockSocket(t, sockPath)

	sc, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err != nil {
		t.Fatalf("NewSidecar external: %v", err)
	}
	defer sc.Close()

	// Simulate socket death: close listener and old connection.
	ln.Close()
	sc.conn.Close()
	sc.mu.Lock()
	sc.healthy = false
	sc.mu.Unlock()

	// Restart a new mock socket (simulates K8s restarting the container).
	ln2 := startMockSocket(t, sockPath)
	defer ln2.Close()

	// Restart should reconnect.
	if err := sc.restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !sc.Healthy() {
		t.Error("sidecar should be healthy after restart")
	}
	if sc.Restarts() != 1 {
		t.Errorf("restarts = %d, want 1", sc.Restarts())
	}
	if sc.conn == nil {
		t.Fatal("should have a new connection after restart")
	}
}

func TestSidecar_ExternalClose(t *testing.T) {
	sockPath := t.TempDir() + "/close.sock"
	ln := startMockSocket(t, sockPath)
	defer ln.Close()

	sc, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err != nil {
		t.Fatalf("NewSidecar external: %v", err)
	}

	// Close should not error and should not remove the socket file.
	if err := sc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Socket file should still exist (K8s owns it).
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Error("external Close should not remove the socket file")
	}
}

func TestSidecar_WithSocketPath(t *testing.T) {
	s := &Sidecar{}
	WithSocketPath("/custom/path.sock")(s)
	if s.socketPath != "/custom/path.sock" {
		t.Errorf("socketPath = %q, want %q", s.socketPath, "/custom/path.sock")
	}
}

func TestSidecar_WithExternalMode(t *testing.T) {
	s := &Sidecar{}
	WithExternalMode()(s)
	if !s.externalMode {
		t.Error("externalMode should be true")
	}
}

func TestSidecarPool_ExternalMode(t *testing.T) {
	// Start mock sockets at the paths NewSidecarPool will generate:
	// /tmp/fracta-strategy-{i}.sock
	n := 3
	listeners := make([]net.Listener, n)
	for i := 0; i < n; i++ {
		sockPath := fmt.Sprintf("/tmp/fracta-strategy-%d.sock", i)
		listeners[i] = startMockSocket(t, sockPath)
		defer listeners[i].Close()
		defer os.Remove(sockPath)
	}

	// Use the real NewSidecarPool constructor to exercise slices.Clone + WithSocketPath.
	pool, err := NewSidecarPool(n, "python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode())
	if err != nil {
		t.Fatalf("NewSidecarPool: %v", err)
	}
	defer pool.Close()

	// Verify all sidecars are external, healthy, with distinct paths.
	paths := make(map[string]bool)
	for i, sc := range pool.sidecars {
		if !sc.externalMode {
			t.Errorf("sidecar %d: externalMode = false", i)
		}
		if !sc.Healthy() {
			t.Errorf("sidecar %d: not healthy", i)
		}
		if sc.cmd != nil {
			t.Errorf("sidecar %d: has subprocess in external mode", i)
		}
		expectedPath := fmt.Sprintf("/tmp/fracta-strategy-%d.sock", i)
		if sc.socketPath != expectedPath {
			t.Errorf("sidecar %d: socketPath = %q, want %q", i, sc.socketPath, expectedPath)
		}
		paths[sc.socketPath] = true
	}
	if len(paths) != n {
		t.Errorf("expected %d distinct socket paths, got %d", n, len(paths))
	}
}

func TestSidecar_ExternalSocket_Timeout(t *testing.T) {
	// Trying to connect to a non-existent socket should timeout.
	sockPath := t.TempDir() + "/nonexistent.sock"
	_, err := NewSidecar("python3", "/fake/runner.py", "/fake/strategies",
		WithExternalMode(), WithSocketPath(sockPath))
	if err == nil {
		t.Fatal("expected error for non-existent socket")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}
