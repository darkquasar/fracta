package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/worker"
	"github.com/spf13/cobra"
)

var (
	workerConfigPath    string
	workerCount         int
	workerID            string
	workerWorkspaceBase string
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start a queue worker that dequeues and executes missions",
	Long:  "Runs one or more worker loops that pull missions from the configured queue and execute them using the host registry.",
	RunE:  runWorker,
}

func init() {
	workerCmd.Flags().StringVar(&workerConfigPath, "config", "", "Path to fracta.yaml config file (required)")
	workerCmd.Flags().IntVar(&workerCount, "workers", 0, "Number of concurrent workers (default: from config or 2)")
	workerCmd.Flags().StringVar(&workerID, "id", "", "Worker ID prefix (default: hostname)")
	workerCmd.Flags().StringVar(&workerWorkspaceBase, "workspace-base", "", "Base directory for ephemeral workspaces")
	rootCmd.AddCommand(workerCmd)
}

func runWorker(cmd *cobra.Command, args []string) error {
	if workerConfigPath == "" {
		return fmt.Errorf("--config is required for fracta worker")
	}

	cfg, err := config.LoadConfig(workerConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Attach log file if configured.
	if cfg.Logging.File != "" {
		if err := fractalog.AttachFile(cfg.Logging.File, cfg.Logging.Level); err != nil {
			return fmt.Errorf("attaching log file: %w", err)
		}
	}

	log := fractalog.Component("worker")

	if cfg.Runtime.Queue.Backend == "" {
		return fmt.Errorf("queue backend not configured in %s (set runtime.queue.backend)", workerConfigPath)
	}

	// Resolve worker count: CLI flag > config > default.
	numWorkers := workerCount
	if numWorkers <= 0 {
		numWorkers = cfg.Runtime.Queue.Workers
	}
	if numWorkers <= 0 {
		numWorkers = 2
	}

	// Resolve worker ID prefix.
	idPrefix := workerID
	if idPrefix == "" {
		idPrefix, _ = os.Hostname()
		if idPrefix == "" {
			idPrefix = "worker"
		}
	}

	// Resolve workspace base.
	wsBase := workerWorkspaceBase
	if wsBase == "" {
		wsBase = cfg.Runtime.Queue.WorkspaceBase
	}

	// Build the same host registry as cmd/serve.go.
	reg := buildRuntimeRegistry()

	// Build control plane (provides Store, Queue, etc.) — single initialization.
	cp, err := controlplane.NewControlPlane(cfg, "")
	if err != nil {
		return fmt.Errorf("creating control plane: %w", err)
	}
	defer cp.Close()

	if cp.Queue == nil {
		return fmt.Errorf("queue not initialized (check runtime.queue config)")
	}

	// Set up graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("starting workers",
		"count", numWorkers,
		"queue_backend", cfg.Runtime.Queue.Backend,
		"workspace_base", wsBase,
		"id_prefix", idPrefix,
	)

	// Standalone workers are separate processes — they cannot share an in-memory
	// stager with the CP API process. Use RemoteCredentialStager when CP API URL
	// is configured (split-deployment mode). Otherwise nil — worker will fail
	// clearly if staged refs appear without a configured stager.
	var stager credentials.CredentialStager
	if cfg.ControlPlaneAPI.URL != "" {
		stager = cpapi.NewRemoteCredentialStager(cfg.ControlPlaneAPI.URL)
	}

	// Start workers.
	errCh := make(chan error, numWorkers)
	for i := 0; i < numWorkers; i++ {
		id := fmt.Sprintf("%s-%d", idPrefix, i)
		workerOpts := []worker.Option{
			worker.WithConfig(cfg),
			worker.WithBackend(cp.Backend),
			worker.WithEvents(cp.Events),
			worker.WithLifecycle(cp.Lifecycle),
			worker.WithStager(stager),
		}
		if cfg.ControlPlaneAPI.URL != "" {
			workerOpts = append(workerOpts, worker.WithRemoteBusURL(cfg.ControlPlaneAPI.URL))
		}
		w := worker.New(id, cp.Queue, cp.Store, reg, wsBase, workerOpts...)
		go func() {
			errCh <- w.Run(ctx)
		}()
	}

	// Wait for context cancellation (signal) then drain workers.
	<-ctx.Done()
	log.Info("shutting down workers")

	// Workers will exit when their context is cancelled.
	for i := 0; i < numWorkers; i++ {
		<-errCh
	}

	log.Info("all workers stopped")
	return nil
}
