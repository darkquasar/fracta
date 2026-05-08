package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/orchestrator"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/spf13/cobra"
)

var controlplaneCmd = &cobra.Command{
	Use:   "controlplane",
	Short: "Manage the fracta control plane daemon",
	Long:  "Start, stop, or check status of the local fracta control plane daemon.",
}

var controlplaneStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the control plane daemon",
	Long: `Start a local control plane daemon with full lifecycle management.

The daemon runs:
  - Full ControlPlane (LocalBackend, SQLite, MemoryQueue, in-process workers)
  - HTTP API on :9090 (or configured listen address)
  - Gateway subprocess on :8080 (or configured gateway.listen address)

The gateway runs as a supervised child process. If it crashes, the daemon
automatically restarts it. The PID is written to .fracta/controlplane.pid
for lifecycle management.`,
	RunE: runControlPlaneStart,
}

var controlplaneStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the control plane daemon",
	RunE:  runControlPlaneStop,
}

var controlplaneStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report control plane daemon status",
	RunE:  runControlPlaneStatus,
}

const (
	gatewayHealthTimeout = 90 * time.Second
	daemonHealthTimeout  = 90 * time.Second
	healthPollInterval   = 250 * time.Millisecond
)

var (
	cpStartForeground bool
	cpStartConfig     string
)

func init() {
	controlplaneStartCmd.Flags().BoolVar(&cpStartForeground, "foreground", false, "run in foreground (default: daemonize)")
	controlplaneStartCmd.Flags().StringVar(&cpStartConfig, "config", "", "path to fracta.yaml config file")

	controlplaneCmd.AddCommand(controlplaneStartCmd)
	controlplaneCmd.AddCommand(controlplaneStopCmd)
	controlplaneCmd.AddCommand(controlplaneStatusCmd)
	rootCmd.AddCommand(controlplaneCmd)
}

// pidFilePath returns the path to the controlplane PID file.
func pidFilePath(root string) string {
	return filepath.Join(root, ".fracta", "controlplane.pid")
}

// writePIDFile writes the current process PID to the PID file.
func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating PID file directory: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

// readPIDFile reads the PID from the PID file.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in %s: %w", path, err)
	}
	return pid, nil
}

// removePIDFile removes the PID file.
func removePIDFile(path string) {
	os.Remove(path)
}

// isProcessRunning checks if a process with the given PID is still alive.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Use Signal(0) to test.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// isDaemonRunning checks if a controlplane daemon is already running.
func isDaemonRunning(root string) (bool, int) {
	pidPath := pidFilePath(root)
	pid, err := readPIDFile(pidPath)
	if err != nil {
		return false, 0
	}
	if isProcessRunning(pid) {
		return true, pid
	}
	// Stale PID file — clean up.
	removePIDFile(pidPath)
	return false, 0
}

// gatewaySupervisor manages the gateway subprocess lifecycle.
// It starts `fracta serve --gateway-mode --transport http --listen <addr>`,
// monitors for crashes, and restarts automatically.
type gatewaySupervisor struct {
	fractaBin    string // path to fracta binary (os.Args[0])
	listenAddr string // gateway listen address (e.g. ":8080")
	configPath string // path to fracta.yaml (passed to gateway via --config)

	log    *slog.Logger
	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{} // closed when supervision goroutine exits
}

func newGatewaySupervisor(listenAddr, configPath string) *gatewaySupervisor {
	return &gatewaySupervisor{
		fractaBin:    os.Args[0],
		listenAddr: listenAddr,
		configPath: configPath,
		log:        fractalog.Component("gateway-supervisor"),
		done:       make(chan struct{}),
	}
}

// Start launches the gateway subprocess and begins supervision.
// Returns after the first successful health check or after a timeout.
func (gs *gatewaySupervisor) Start(ctx context.Context) error {
	superviseCtx, cancel := context.WithCancel(ctx)
	gs.cancel = cancel

	// Launch initial process.
	if err := gs.startProcess(); err != nil {
		cancel()
		close(gs.done)
		return fmt.Errorf("starting gateway process: %w", err)
	}

	// Wait for health check.
	if err := gs.waitHealthy(ctx); err != nil {
		cancel()
		// Kill the process and reap it before returning.
		gs.signalProcess(syscall.SIGKILL)
		gs.mu.Lock()
		cmd := gs.cmd
		gs.cmd = nil
		gs.mu.Unlock()
		if cmd != nil {
			cmd.Wait()
		}
		close(gs.done)
		return fmt.Errorf("gateway health check failed: %w", err)
	}

	// Start supervision goroutine for crash restarts.
	go gs.supervise(superviseCtx)

	return nil
}

// Stop gracefully shuts down the gateway subprocess.
// It cancels the supervision context (preventing restarts), signals the
// process to terminate, and waits for the supervision goroutine to exit.
func (gs *gatewaySupervisor) Stop() {
	if gs.cancel != nil {
		gs.cancel()
	}
	gs.signalProcess(syscall.SIGTERM)
	// Wait for supervision goroutine to exit (it owns cmd.Wait).
	<-gs.done
}

func (gs *gatewaySupervisor) startProcess() error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	args := []string{"serve", "--gateway-mode", "--transport", "http", "--listen", gs.listenAddr}
	if gs.configPath != "" {
		args = append(args, "--config", gs.configPath)
	}

	cmd := exec.Command(gs.fractaBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Set process group so we can clean up child processes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	gs.cmd = cmd
	gs.log.Info("gateway process started", "pid", cmd.Process.Pid, "listen", gs.listenAddr)
	return nil
}

// signalProcess sends a signal to the gateway process (if running).
func (gs *gatewaySupervisor) signalProcess(sig os.Signal) {
	gs.mu.Lock()
	cmd := gs.cmd
	gs.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(sig)
	}
}

func (gs *gatewaySupervisor) waitHealthy(ctx context.Context) error {
	// Resolve the health URL from the listen address.
	host := "localhost"
	port := gs.listenAddr
	if port[0] == ':' {
		port = port[1:]
	}
	healthURL := fmt.Sprintf("http://%s:%s/healthz", host, port)

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.After(gatewayHealthTimeout)

	for {
		select {
		case <-deadline:
			return fmt.Errorf("gateway did not become healthy within %s at %s", gatewayHealthTimeout, healthURL)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPollInterval):
			resp, err := client.Get(healthURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					gs.log.Info("gateway health check passed", "url", healthURL)
					return nil
				}
			}
		}
	}
}

func (gs *gatewaySupervisor) supervise(ctx context.Context) {
	defer close(gs.done)

	for {
		gs.mu.Lock()
		cmd := gs.cmd
		gs.mu.Unlock()

		if cmd == nil {
			return
		}

		// Wait for the process to exit. This goroutine is the sole caller of
		// cmd.Wait() — stopProcess/signalProcess only sends signals.
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()

		select {
		case err := <-waitDone:
			// Process exited. Check if we're shutting down.
			select {
			case <-ctx.Done():
				return
			default:
			}

			gs.log.Warn("gateway process exited unexpectedly", "error", err)

			// Back off before restart.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}

			gs.log.Info("restarting gateway process")
			if err := gs.startProcess(); err != nil {
				gs.log.Error("failed to restart gateway", "error", err)
				return
			}

			// Wait for health before considering it alive.
			healthCtx, healthCancel := context.WithTimeout(ctx, gatewayHealthTimeout)
			if err := gs.waitHealthy(healthCtx); err != nil {
				healthCancel()
				gs.log.Error("restarted gateway failed health check", "error", err)
				gs.signalProcess(syscall.SIGKILL)
				gs.mu.Lock()
				restartCmd := gs.cmd
				gs.cmd = nil
				gs.mu.Unlock()
				if restartCmd != nil {
					restartCmd.Wait()
				}
				return
			}
			healthCancel()
			gs.log.Info("gateway restarted successfully")

		case <-ctx.Done():
			// Shutdown requested. Wait for process to exit with timeout,
			// escalate to SIGKILL if needed.
			select {
			case <-waitDone:
				gs.log.Info("gateway process exited gracefully")
			case <-time.After(5 * time.Second):
				gs.signalProcess(syscall.SIGKILL)
				<-waitDone
				gs.log.Warn("gateway process killed after timeout")
			}
			return
		}
	}
}

func runControlPlaneStart(cmd *cobra.Command, args []string) error {
	root, _ := FindProjectRoot("")

	// Check if already running.
	if running, pid := isDaemonRunning(root); running {
		return fmt.Errorf("control plane daemon already running (PID %d)", pid)
	}

	// Load config.
	var (
		cfg *config.Config
		err error
	)
	if cpStartConfig != "" {
		cfg, err = config.LoadConfig(cpStartConfig)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
	} else {
		cfg, err = loadConfigOrDefault(root)
		if err != nil {
			return err
		}
	}

	// Attach log file if configured.
	if cfg.Logging.File != "" {
		if err := fractalog.AttachFile(cfg.Logging.File, cfg.Logging.Level); err != nil {
			return fmt.Errorf("attaching log file: %w", err)
		}
	}
	log := fractalog.Component("controlplane-cmd")

	// Determine listen address.
	listenAddr := cfg.ControlPlaneAPI.Listen
	if listenAddr == "" {
		listenAddr = ":9090"
	}

	// Build control plane.
	cp, err := controlplane.NewControlPlane(cfg, root)
	if err != nil {
		return fmt.Errorf("creating control plane: %w", err)
	}
	defer cp.Close()

	// Reconcile orphaned queued agents from previous crashes.
	if cp.Queue != nil {
		reconcileOrphanedQueuedAgents(context.Background(), cp.Store, cp.Queue)
	}

	// Build LocalControlPlaneClient.
	sharedRegistry := orchestrator.NewProcessRegistry()
	clientOpts := []cpapi.LocalClientOption{
		cpapi.WithProcessRegistry(sharedRegistry),
		cpapi.WithRuntimeRegistry(buildRuntimeRegistry()),
	}
	// Thread agent-mode wiring paths.
	var configPath, graphAddr, graphName, strategyDir string
	if cpStartConfig != "" {
		configPath = cpStartConfig
	} else if configFlag != "" {
		configPath = configFlag
	} else {
		p := filepath.Join(root, "fracta.yaml")
		if _, err := os.Stat(p); err == nil {
			configPath = p
		}
	}
	if cfg != nil {
		if fdb, ok := cfg.Connections["falkordb"]; ok && fdb.URL != "" {
			addr := fdb.URL
			for _, prefix := range []string{"redis://", "rediss://"} {
				if len(addr) > len(prefix) && addr[:len(prefix)] == prefix {
					addr = addr[len(prefix):]
					break
				}
			}
			graphAddr = addr
		}
		if fdb, ok := cfg.Connections["falkordb"]; ok && fdb.GraphName != "" {
			graphName = fdb.GraphName
		}
		if cfg.Strategy.Dir != "" {
			strategyDir = cfg.Strategy.Dir
		}
	}
	clientOpts = append(clientOpts, cpapi.WithAgentWiring(configPath, graphAddr, strategyDir))

	// Wire GraphClient into LocalControlPlaneClient when FalkorDB is configured.
	if graphAddr != "" {
		var gcOpts []graph.FalkorDBOption
		if graphName != "" {
			gcOpts = append(gcOpts, graph.WithGraphName(graphName))
		}
		gc := graph.NewFalkorDBClient(graphAddr, gcOpts...)
		clientOpts = append(clientOpts, cpapi.WithGraphClient(gc))
		defer gc.Close()
	}

	if cp.ObjectiveStore != nil {
		clientOpts = append(clientOpts, cpapi.WithObjectiveStore(cp.ObjectiveStore))
	}
	if cp.SnapshotStore != nil {
		clientOpts = append(clientOpts, cpapi.WithSnapshotStore(cp.SnapshotStore))
	}
	if cp.EventStore != nil {
		clientOpts = append(clientOpts, cpapi.WithEventStore(cp.EventStore))
	}
	if er, ok := cp.Store.(state.EventReader); ok {
		clientOpts = append(clientOpts, cpapi.WithEventReader(er))
	}

	cpClient := cpapi.NewLocalControlPlaneClient(cp, root, clientOpts...)

	// Wire event bus into runtime backend.
	wireBackendEvents(cp.Backend, cp.Events)

	// Shared credential stager.
	sharedStager := credentials.NewInMemoryCredentialStager()

	// Start CP API HTTP server.
	cpAPIServerOpts := []cpapi.HTTPServerOption{
		cpapi.WithCredentialStager(sharedStager),
	}
	if cp.SSEHub != nil && cp.EventStore != nil {
		var er state.EventReader
		if r, ok := cp.Store.(state.EventReader); ok {
			er = r
		}
		cpAPIServerOpts = append(cpAPIServerOpts, cpapi.WithSSE(cp.SSEHub, cp.EventStore, er, cp.SnapshotStore))
	}
	cpAPIServer := cpapi.NewHTTPServer(listenAddr, cpClient, cpAPIServerOpts...)
	if err := cpAPIServer.Start(); err != nil {
		return fmt.Errorf("starting control-plane API: %w", err)
	}
	defer cpAPIServer.Shutdown(context.Background())

	// Start in-process workers when queue is configured.
	if cp.Queue != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		startInProcessWorkers(workerCtx, cp, buildRuntimeRegistry(), cfg, sharedStager)
	}

	// Supervise gateway subprocess.
	gwListenAddr := cfg.Gateway.Listen
	if gwListenAddr == "" {
		gwListenAddr = ":8080"
	}
	gwSupervisor := newGatewaySupervisor(gwListenAddr, configPath)
	if err := gwSupervisor.Start(context.Background()); err != nil {
		log.Error("gateway supervision failed (degraded mode — no gateway)", "error", err)
	} else {
		defer gwSupervisor.Stop()
		log.Info("gateway subprocess supervised", "listen", gwListenAddr)
	}

	// Start SIGHUP handler for config hot-reload.
	if configPath != "" {
		stop := make(chan struct{})
		go controlplane.StartSignalHandler(cp, configPath, stop)
		defer close(stop)
	}

	// Write PID file.
	pidPath := pidFilePath(root)
	if err := writePIDFile(pidPath); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer removePIDFile(pidPath)

	log.Info("control plane daemon started",
		"listen", listenAddr,
		"gateway_listen", gwListenAddr,
		"pid", os.Getpid(),
		"root", root,
	)
	fmt.Printf("Control plane daemon started (PID %d, CP on %s, gateway on %s)\n", os.Getpid(), listenAddr, gwListenAddr)

	// Block on signal.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("control plane daemon shutting down")
	fmt.Println("Control plane daemon shutting down...")
	return nil
}

func runControlPlaneStop(cmd *cobra.Command, args []string) error {
	root, _ := FindProjectRoot("")

	pidPath := pidFilePath(root)
	pid, err := readPIDFile(pidPath)
	if err != nil {
		return fmt.Errorf("control plane daemon not running (no PID file)")
	}

	if !isProcessRunning(pid) {
		removePIDFile(pidPath)
		return fmt.Errorf("control plane daemon not running (stale PID %d)", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	// Send SIGTERM for graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to PID %d: %w", pid, err)
	}

	// Wait briefly for process to exit.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if !isProcessRunning(pid) {
			removePIDFile(pidPath)
			fmt.Printf("Control plane daemon stopped (PID %d)\n", pid)
			return nil
		}
	}

	// Force kill if still running.
	_ = proc.Signal(syscall.SIGKILL)
	removePIDFile(pidPath)
	fmt.Printf("Control plane daemon killed (PID %d)\n", pid)
	return nil
}

func runControlPlaneStatus(cmd *cobra.Command, args []string) error {
	root, _ := FindProjectRoot("")

	running, pid := isDaemonRunning(root)
	if !running {
		fmt.Println("Control plane daemon: stopped")
		return nil
	}

	// Try to hit the healthz endpoint.
	cfg, err := loadConfigOrDefault(root)
	if err != nil {
		return err
	}
	url := cfg.ControlPlaneAPI.URL
	if url == "" {
		listenAddr := cfg.ControlPlaneAPI.Listen
		if listenAddr == "" {
			listenAddr = ":9090"
		}
		url = "http://localhost" + listenAddr
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		fmt.Printf("Control plane daemon: running (PID %d), but API unreachable (%v)\n", pid, err)
		return nil
	}
	defer resp.Body.Close()

	cpStatus := "unhealthy"
	if resp.StatusCode == http.StatusOK {
		cpStatus = "healthy"
	}

	// Check gateway health.
	gwListenAddr := cfg.Gateway.Listen
	if gwListenAddr == "" {
		gwListenAddr = ":8080"
	}
	gwURL := "http://localhost" + gwListenAddr
	gwStatus := "unreachable"
	gwResp, gwErr := client.Get(gwURL + "/healthz")
	if gwErr == nil {
		gwResp.Body.Close()
		if gwResp.StatusCode == http.StatusOK {
			gwStatus = "healthy"
		} else {
			gwStatus = fmt.Sprintf("unhealthy (status %d)", gwResp.StatusCode)
		}
	}

	fmt.Printf("Control plane daemon: running (PID %d)\n  CP API: %s (%s)\n  Gateway: %s (%s)\n",
		pid, cpStatus, url, gwStatus, gwURL)
	return nil
}
