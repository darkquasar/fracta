package strategy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/staging"
)

// SidecarTransportError indicates the sidecar socket/process is broken.
// The sidecar has been marked unhealthy but has NOT been restarted yet.
// Phase indicates where the failure occurred: "write" (request never sent)
// or "read" (request may have been processed).
type SidecarTransportError struct {
	Err   error
	Phase string // "write" or "read"
}

func (e *SidecarTransportError) Error() string {
	return fmt.Sprintf("sidecar transport (%s): %v", e.Phase, e.Err)
}

func (e *SidecarTransportError) Unwrap() error { return e.Err }

// SidecarRestartedError indicates the sidecar was restarted after a transport
// failure, but the caller should NOT auto-retry because the request may have
// been processed (read-phase failure) or has non-idempotent side effects (Create).
type SidecarRestartedError struct {
	Err error // the original transport error
}

func (e *SidecarRestartedError) Error() string {
	return fmt.Sprintf("sidecar restarted after transport error: %v", e.Err)
}

func (e *SidecarRestartedError) Unwrap() error { return e.Err }

// isTransportError returns true if err indicates the socket/process is dead.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	// Check for common transport failure patterns.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF")
}

const DefaultSockPath = "/tmp/fracta-strategy.sock"

// DefaultRunTimeout is the socket deadline for Run calls (strategy execution).
const DefaultRunTimeout = 300 * time.Second

// DefaultMethodTimeout is the socket deadline for non-Run calls (list, describe, stage).
const DefaultMethodTimeout = 30 * time.Second

// StrategyInfo describes a discovered strategy.
type StrategyInfo struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Version      string         `json:"version,omitempty"`
	Tags         []string       `json:"tags"`
	Params       map[string]any `json:"params"`
	Requires     map[string]any `json:"requires,omitempty"`
	File         string         `json:"file"`
	ContractPath string         `json:"contract_path,omitempty"`
}

// StepTrace records the execution of a single strategy step.
type StepTrace struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int    `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// TraceInfo records the execution of an entire strategy run.
type TraceInfo struct {
	Steps           []StepTrace `json:"steps"`
	TotalDurationMs int         `json:"total_duration_ms"`
	Error           string      `json:"error,omitempty"`
}

// RunResult is the response from executing a strategy.
type RunResult struct {
	Status                 string    `json:"status"`
	Result                 any       `json:"result"`
	PartialResults         any       `json:"partial_results,omitempty"`
	PartialResultsTruncated bool     `json:"partial_results_truncated,omitempty"`
	OmittedSteps           []string  `json:"omitted_steps,omitempty"`
	Trace                  TraceInfo `json:"trace"`
	Error                  string    `json:"error,omitempty"`
}

// StagingManifestEntry describes one table in the staging manifest sent to the runner.
type StagingManifestEntry struct {
	Mode        string   `json:"mode"`                   // fetch mode: fracta_mcp_gateway, mcp, native
	Required    bool     `json:"required"`               // whether the strategy fails without this table
	Staged      bool     `json:"staged"`                 // whether data has been staged
	ParquetPath string   `json:"parquet_path,omitempty"` // path to staged Parquet file (if staged)
	Columns     []string `json:"columns,omitempty"`      // expected column names from contract
	Partial     bool     `json:"partial,omitempty"`      // true if staged with incomplete data (S5)
}

// StagingManifest maps table names to their staging metadata.
type StagingManifest map[string]StagingManifestEntry

// listResponse is the wire format for a list response.
type listResponse struct {
	Status     string         `json:"status"`
	Strategies []StrategyInfo `json:"strategies"`
	Error      string         `json:"error,omitempty"`
}

// describeResponse is the wire format for a describe response.
type describeResponse struct {
	Status   string       `json:"status"`
	Strategy StrategyInfo `json:"strategy"`
	Error    string       `json:"error,omitempty"`
}

// SidecarOption configures a Sidecar.
type SidecarOption func(*Sidecar)

// WithGraphAddr passes a FalkorDB address to the Python runner as a CLI arg.
func WithGraphAddr(addr string) SidecarOption {
	return func(s *Sidecar) {
		s.graphAddr = addr
	}
}

// WithGraphName passes a FalkorDB graph name to the Python runner as a CLI arg.
func WithGraphName(name string) SidecarOption {
	return func(s *Sidecar) {
		s.graphName = name
	}
}

// WithElasticURL passes an Elasticsearch URL to the Python runner as a CLI arg.
func WithElasticURL(url string) SidecarOption {
	return func(s *Sidecar) {
		s.elasticURL = url
	}
}

// WithElasticAPIKey passes an Elasticsearch API key to the Python runner as a CLI arg.
func WithElasticAPIKey(key string) SidecarOption {
	return func(s *Sidecar) {
		s.elasticAPIKey = key
	}
}

// WithUVBin sets the path to the uv binary. When set, start() uses
// "uv run --project <strategyDir>" instead of invoking python3 directly.
func WithUVBin(bin string) SidecarOption {
	return func(s *Sidecar) {
		s.uvBin = bin
	}
}

// WithStagingDir sets the directory where Parquet staging files are written.
func WithStagingDir(dir string) SidecarOption {
	return func(s *Sidecar) {
		s.stagingDir = dir
	}
}

// WithSocketPath sets the Unix socket path. Defaults to DefaultSockPath.
// Used by the pool to assign per-sidecar paths.
func WithSocketPath(path string) SidecarOption {
	return func(s *Sidecar) {
		s.socketPath = path
	}
}

// WithExternalMode connects to a pre-existing socket without spawning a
// subprocess. In this mode the process lifecycle is managed externally
// (e.g. by K8s) and restart/close only affect the connection.
func WithExternalMode() SidecarOption {
	return func(s *Sidecar) {
		s.externalMode = true
	}
}

// WithRunTimeout sets the socket deadline for Run calls. Defaults to 300s.
// Other sidecar methods (list, describe, stage) use a shorter 30s deadline.
func WithRunTimeout(d time.Duration) SidecarOption {
	return func(s *Sidecar) {
		s.runTimeout = d
	}
}

// Sidecar manages a long-lived Python strategy runner subprocess
// communicating over a Unix socket with newline-delimited JSON.
type Sidecar struct {
	pythonBin     string
	runnerPath    string
	socketPath    string // Unix socket path (default: DefaultSockPath)
	strategyDir   string
	graphAddr     string
	graphName     string
	elasticURL    string
	elasticAPIKey string
	uvBin         string
	stagingDir    string
	runTimeout    time.Duration
	externalMode  bool // true: connect to existing socket, no subprocess

	cmd    *exec.Cmd
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex // serializes sendRecv calls
	logger *slog.Logger

	// Health tracking (S1/S2).
	healthy     bool      // false after transport error, true after start/restart
	restarts    int       // cumulative restart count for observability
	lastFailure time.Time // when the last transport error occurred
}

// StrategyDir returns the base directory for strategies.
func (s *Sidecar) StrategyDir() string {
	return s.strategyDir
}

// StagingDir returns the staging directory for Parquet files.
// Falls back to staging.DefaultStagingDir if not explicitly set.
func (s *Sidecar) StagingDir() string {
	if s.stagingDir != "" {
		return s.stagingDir
	}
	return staging.DefaultStagingDir
}

// NewSidecar creates a strategy runner sidecar. In local mode (default) it
// spawns the Python subprocess, waits for READY, and connects. In external
// mode (WithExternalMode) it connects to a pre-existing socket managed by
// an external process (e.g. K8s sidecar container).
// The caller must call Close() when done.
func NewSidecar(pythonBin, runnerPath, strategyDir string, opts ...SidecarOption) (*Sidecar, error) {
	s := &Sidecar{
		pythonBin:   pythonBin,
		runnerPath:  runnerPath,
		socketPath:  DefaultSockPath,
		strategyDir: strategyDir,
		logger:      fractalog.Component("sidecar"),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.externalMode {
		if err := s.connectExternal(); err != nil {
			return nil, fmt.Errorf("connecting to external sidecar: %w", err)
		}
	} else {
		if err := s.startLocal(); err != nil {
			return nil, fmt.Errorf("starting sidecar: %w", err)
		}
	}
	s.healthy = true
	s.logger.Info("started", "socket", s.socketPath, "strategies", strategyDir, "external", s.externalMode)
	return s, nil
}

// connectExternal waits for an externally-managed socket to appear and connects.
// No subprocess is spawned — the process lifecycle is owned by K8s or similar.
// Retries both file existence and Dial to handle stale sockets during runner restarts
// (the runner unlinks the old socket on startup, so the file may exist briefly before
// the new listener is bound).
func (s *Sidecar) connectExternal() error {
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(s.socketPath); err == nil {
			conn, err := net.Dial("unix", s.socketPath)
			if err == nil {
				s.conn = conn
				s.reader = bufio.NewReader(conn)
				return nil
			}
			// Socket file exists but Dial failed (stale socket, runner restarting).
			// Fall through to retry.
		}
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for external socket %s", s.socketPath)
		case <-time.After(200 * time.Millisecond):
		}

	}
}

// startLocal spawns the subprocess, waits for READY, and connects.
func (s *Sidecar) startLocal() error {
	// Clean up stale socket
	os.Remove(s.socketPath)

	runnerArgs := []string{"--socket", s.socketPath, "--strategy-dir", s.strategyDir}
	if s.graphAddr != "" {
		runnerArgs = append(runnerArgs, "--graph-addr", s.graphAddr)
	}
	if s.graphName != "" {
		runnerArgs = append(runnerArgs, "--graph-name", s.graphName)
	}
	if s.stagingDir != "" {
		runnerArgs = append(runnerArgs, "--staging-dir", s.stagingDir)
	}
	if s.uvBin != "" {
		// uv run --project <dir> <script> <args...>
		args := append([]string{"run", "--project", s.strategyDir, s.runnerPath}, runnerArgs...)
		s.cmd = exec.Command(s.uvBin, args...)
	} else {
		args := append([]string{s.runnerPath}, runnerArgs...)
		s.cmd = exec.Command(s.pythonBin, args...)
	}
	// Use a process group so Close() can kill the entire tree (uv + python child).
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	s.cmd.Stderr = os.Stderr

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// Wait for READY signal (with timeout)
	readyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			readyCh <- scanner.Text()
		} else {
			errCh <- fmt.Errorf("runner exited without READY signal")
		}
	}()

	select {
	case line := <-readyCh:
		if !strings.HasPrefix(line, "READY") {
			s.cmd.Process.Kill()
			s.cmd.Wait()
			return fmt.Errorf("unexpected first line from runner: %q", line)
		}
	case err := <-errCh:
		s.cmd.Process.Kill()
		s.cmd.Wait()
		return err
	case <-time.After(10 * time.Second):
		s.cmd.Process.Kill()
		s.cmd.Wait()
		return fmt.Errorf("timeout waiting for READY signal")
	}

	// Connect to the Unix socket
	conn, err := net.Dial("unix", s.socketPath)
	if err != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
		return fmt.Errorf("connecting to socket %s: %w", s.socketPath, err)
	}
	s.conn = conn
	s.reader = bufio.NewReader(conn)

	return nil
}

// sendRecv sends a JSON request and reads a JSON response over the socket
// using the given deadline duration. On transport errors, it marks the sidecar
// unhealthy and returns a *SidecarTransportError.
func (s *Sidecar) sendRecv(req any, resp any, deadline time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	s.conn.SetDeadline(time.Now().Add(deadline))
	if _, err := s.conn.Write(data); err != nil {
		if isTransportError(err) {
			s.healthy = false
			s.lastFailure = time.Now()
			return &SidecarTransportError{Err: fmt.Errorf("write: %w", err), Phase: "write"}
		}
		return fmt.Errorf("write: %w", err)
	}

	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		if isTransportError(err) {
			s.healthy = false
			s.lastFailure = time.Now()
			return &SidecarTransportError{Err: fmt.Errorf("read: %w", err), Phase: "read"}
		}
		return fmt.Errorf("read: %w", err)
	}

	return json.Unmarshal(line, resp)
}

// restart closes the dead sidecar and reconnects or respawns depending on mode.
// Must be called WITHOUT mu held.
func (s *Sidecar) restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("restarting sidecar", "restarts", s.restarts+1, "external", s.externalMode)

	// Close dead connection (ignore errors).
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}

	if s.externalMode {
		// External mode: wait for K8s to restart the container and recreate the socket.
		if err := s.waitAndReconnect(30 * time.Second); err != nil {
			return fmt.Errorf("restart: %w", err)
		}
	} else {
		// Local mode: kill, remove socket, respawn.
		if s.cmd != nil && s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			s.cmd.Wait()
		}
		os.Remove(s.socketPath)

		if err := s.startLocal(); err != nil {
			return fmt.Errorf("restart: %w", err)
		}
	}

	s.healthy = true
	s.restarts++
	s.logger.Info("sidecar restarted", "restarts", s.restarts, "socket", s.socketPath)
	return nil
}

// waitAndReconnect waits for the external socket to reappear and reconnects.
// Retries both file existence and Dial to handle stale sockets during runner restarts.
func (s *Sidecar) waitAndReconnect(timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		if _, err := os.Stat(s.socketPath); err == nil {
			conn, err := net.Dial("unix", s.socketPath)
			if err == nil {
				s.conn = conn
				s.reader = bufio.NewReader(conn)
				break
			}
			// Stale socket — runner hasn't re-bound yet. Retry.
		}
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for external socket %s to reappear", s.socketPath)
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil
}

// Healthy returns whether the sidecar is currently healthy.
func (s *Sidecar) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// LastFailure returns the time of the last transport failure.
func (s *Sidecar) LastFailure() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFailure
}

// Restarts returns the cumulative restart count.
func (s *Sidecar) Restarts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restarts
}

// withRetry wraps a sendRecv-based call with transport error detection and restart.
//   - readOnly=true (List, Describe): always retry after restart — no side effects.
//   - retryOnWrite=true (Run): retry only if the failure was on write phase
//     (request never reached Python, safe to resend).
//   - otherwise (Create): return SidecarRestartedError without retrying.
func (s *Sidecar) withRetry(fn func() error, readOnly bool, retryOnWrite bool) error {
	err := fn()
	if err == nil {
		return nil
	}

	var transportErr *SidecarTransportError
	if !errors.As(err, &transportErr) {
		return err // not a transport error, return as-is
	}

	// Attempt restart.
	if restartErr := s.restart(); restartErr != nil {
		return transportErr // restart failed, return original transport error
	}

	if readOnly {
		// Safe to retry once for read-only operations.
		return fn()
	}

	// Write-phase failure: request never reached Python — safe to retry.
	if retryOnWrite && transportErr.Phase == "write" {
		return fn()
	}

	// Read-phase failure or non-retryable write: return SidecarRestartedError
	// so caller knows the sidecar is healthy but should not auto-retry.
	return &SidecarRestartedError{Err: transportErr.Err}
}

// RunTimeout returns the configured run timeout, falling back to DefaultRunTimeout.
func (s *Sidecar) RunTimeout() time.Duration {
	if s.runTimeout > 0 {
		return s.runTimeout
	}
	return DefaultRunTimeout
}

// List returns all discovered strategies, optionally filtered by tags.
// On transport error, restarts the sidecar and retries once (read-only).
func (s *Sidecar) List(tags ...string) ([]StrategyInfo, error) {
	req := map[string]any{"action": "list"}
	if len(tags) > 0 {
		req["tags"] = strings.Join(tags, ",")
	}

	var resp listResponse
	err := s.withRetry(func() error {
		resp = listResponse{} // reset for retry
		return s.sendRecv(req, &resp, DefaultMethodTimeout)
	}, true, false)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("list: %s", resp.Error)
	}

	// Client-side tag filter (in case runner doesn't support it yet)
	if len(tags) > 0 {
		tagSet := make(map[string]bool, len(tags))
		for _, t := range tags {
			tagSet[t] = true
		}
		var filtered []StrategyInfo
		for _, si := range resp.Strategies {
			for _, t := range si.Tags {
				if tagSet[t] {
					filtered = append(filtered, si)
					break
				}
			}
		}
		return filtered, nil
	}

	return resp.Strategies, nil
}

// Describe returns full metadata for a single strategy.
// On transport error, restarts the sidecar and retries once (read-only).
func (s *Sidecar) Describe(name string) (*StrategyInfo, error) {
	req := map[string]string{"action": "describe", "strategy": name}

	var resp describeResponse
	err := s.withRetry(func() error {
		resp = describeResponse{} // reset for retry
		return s.sendRecv(req, &resp, DefaultMethodTimeout)
	}, true, false)
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("describe: %s", resp.Error)
	}

	return &resp.Strategy, nil
}

// Run executes a strategy by name with the given parameters.
// If manifest is non-nil, it is included as staging_manifest in the request
// so the runner can load tables from explicit paths and validate requirements.
// On write-phase transport error, restarts the sidecar and retries once
// (request never reached Python). On read-phase error, does NOT retry
// (request may have been processed).
func (s *Sidecar) Run(name string, params map[string]any, manifest StagingManifest) (*RunResult, error) {
	req := map[string]any{
		"action":      "run",
		"strategy":    name,
		"params":      params,
		"staging_dir": s.StagingDir(),
	}
	if manifest != nil {
		req["staging_manifest"] = manifest
	}

	var resp RunResult
	err := s.withRetry(func() error {
		resp = RunResult{} // reset for retry
		return s.sendRecv(req, &resp, s.RunTimeout())
	}, false, true) // retry on write failure (request never reached Python)
	if err != nil {
		return nil, fmt.Errorf("run: %w", err)
	}
	// Don't treat status=="error" as a Go error — the caller may want
	// to inspect the partial trace. Return the full result.
	return &resp, nil
}

// createResponse is the wire format for a create response.
type createResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Create registers a new strategy by writing its Python source file and
// validating its METADATA via the sidecar (legacy JSON metadata path).
// When force is true, overwrites an existing strategy at the same version.
// On transport error, restarts the sidecar but does NOT retry (side effects).
func (s *Sidecar) Create(name, code, metadata string, force bool) error {
	req := map[string]any{
		"action":   "create",
		"name":     name,
		"code":     code,
		"metadata": metadata,
		"force":    force,
	}

	var resp createResponse
	err := s.withRetry(func() error {
		resp = createResponse{} // reset for retry
		return s.sendRecv(req, &resp, DefaultMethodTimeout)
	}, false, false) // NOT read-only, not retryable
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if resp.Status != "ok" {
		return fmt.Errorf("create: %s", resp.Error)
	}
	return nil
}

// CreateWithContract registers a new strategy using a contract YAML string.
// The sidecar creates the directory structure with contract.yaml + strategy.py.
// When force is true, overwrites an existing strategy at the same version.
// On transport error, restarts the sidecar but does NOT retry (side effects).
func (s *Sidecar) CreateWithContract(name, code, contractYAML string, force bool) error {
	req := map[string]any{
		"action":   "create",
		"name":     name,
		"code":     code,
		"contract": contractYAML,
		"force":    force,
	}

	var resp createResponse
	err := s.withRetry(func() error {
		resp = createResponse{} // reset for retry
		return s.sendRecv(req, &resp, DefaultMethodTimeout)
	}, false, false) // NOT read-only, not retryable
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if resp.Status != "ok" {
		return fmt.Errorf("create: %s", resp.Error)
	}
	return nil
}

// Close shuts down the sidecar. In external mode, only closes the socket
// connection (the process is managed by K8s). In local mode, kills the
// subprocess and removes the socket file.
func (s *Sidecar) Close() error {
	var errs []string

	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("close conn: %v", err))
		}
	}

	if !s.externalMode {
		if s.cmd != nil && s.cmd.Process != nil {
			// Kill the entire process group (handles uv -> python child tree).
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			s.cmd.Wait()
		}
		os.Remove(s.socketPath)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
