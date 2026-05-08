package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/darkquasar/fracta/internal/host"
	"github.com/darkquasar/fracta/internal/host/claude"
	"github.com/darkquasar/fracta/internal/host/codex"
	"github.com/darkquasar/fracta/internal/host/opencode"
	"github.com/darkquasar/fracta/internal/fractalog"
)

// buildRuntimeRegistry creates the standard RuntimeRegistry with claude as the default.
func buildRuntimeRegistry() *host.MapRegistry {
	reg := host.NewMapRegistry("claude")
	reg.Register("claude", claude.Host{})
	reg.Register("codex", codex.Host{})
	reg.Register("opencode", opencode.Host{})
	return reg
}

// configRuntimeKeys returns the runtime type names from a config for diagnostic logging.
func configRuntimeKeys(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	runtimes := cfg.EffectiveRuntimes()
	keys := make([]string, 0, len(runtimes))
	for k := range runtimes {
		keys = append(keys, k)
	}
	return keys
}

// loadConfigOrDefault loads fracta.yaml from the --config flag path if set,
// otherwise from the project root. Returns sensible defaults if neither exists.
//
// When --config is explicitly set, missing file or parse errors return an error.
// When discovered from root, failures fall back to defaults.
func loadConfigOrDefault(root string) (*config.Config, error) {
	if configFlag != "" {
		return loadConfigStrict(configFlag)
	}
	path := filepath.Join(root, "fracta.yaml")
	if _, err := os.Stat(path); err == nil {
		cfg, err := config.LoadConfig(path)
		if err == nil {
			return cfg, nil
		}
		fractalog.Component("config").Warn("fracta.yaml found but failed to parse, using defaults", "path", path, "error", err)
	} else {
		fractalog.Component("config").Warn("no fracta.yaml found, using default config (no queue, no logging, no connections)", "expected_path", path)
	}
	return &config.Config{
		Runtime: config.DefaultRuntime(),
	}, nil
}

// loadConfigStrict loads a config file and returns an error if it doesn't exist
// or fails to parse. Used when --config is explicitly provided.
func loadConfigStrict(path string) (*config.Config, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("config file not found: %s", path)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("loading config %s: %w", path, err)
	}
	return cfg, nil
}

// buildCLIClient constructs a RemoteControlPlaneClient for CLI commands.
// When control_plane_api.url is set, it connects directly to the remote CP.
// Otherwise it auto-starts a local daemon and connects to localhost.
// The returned cleanup function is a no-op (kept for call-site compatibility).
//
// An optional config override can be passed (e.g. from --config flag);
// when nil, falls back to loadConfigOrDefault(root).
func buildCLIClient(root string, cfgOverride ...*config.Config) (cpapi.ControlPlaneClient, func(), error) {
	var cfg *config.Config
	if len(cfgOverride) > 0 && cfgOverride[0] != nil {
		cfg = cfgOverride[0]
	} else {
		var err error
		cfg, err = loadConfigOrDefault(root)
		if err != nil {
			return nil, nil, err
		}
	}

	cpURL := cfg.ControlPlaneAPI.URL
	if cpURL == "" {
		// No explicit URL — local mode. Auto-start daemon.
		listenAddr := cfg.ControlPlaneAPI.Listen
		if listenAddr == "" {
			listenAddr = ":9090"
		}
		cpURL = "http://localhost" + listenAddr

		if err := ensureDaemonRunning(root, configFlag); err != nil {
			return nil, nil, fmt.Errorf("ensuring control plane daemon: %w", err)
		}
	}

	client := cpapi.NewRemoteControlPlaneClient(cpURL)
	if err := client.Validate(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("control plane at %s not reachable: %w", cpURL, err)
	}

	// Wrap with staging decorator for host-edge credential materialization.
	stager := cpapi.NewRemoteCredentialStager(cpURL)
	staged := cpapi.NewStagingSpawnClient(client, stager, cfg)
	return staged, func() {}, nil
}

// ensureDaemonRunning checks if the control plane daemon is already running
// (via PID file + healthz). If not, starts it as a background process.
// Returns once the daemon's healthz endpoint responds OK or after timeout.
func ensureDaemonRunning(root string, configPathOverride ...string) error {
	log := fractalog.Component("serve")

	// Check if daemon is already running.
	if running, _ := isDaemonRunning(root); running {
		if len(configPathOverride) > 0 && configPathOverride[0] != "" {
			log.Warn("control plane daemon already running (may have been started with a different config)", "requested_config", configPathOverride[0])
		} else {
			log.Info("control plane daemon already running")
		}
		return nil
	}

	log.Info("auto-starting control plane daemon")

	// Build args for daemon start.
	args := []string{"controlplane", "start", "--foreground"}

	// Pass the explicit config from the caller when present. Otherwise fall
	// back to the project-root fracta.yaml convention.
	var configPath string
	explicit := len(configPathOverride) > 0 && configPathOverride[0] != ""
	if explicit {
		configPath = configPathOverride[0]
	} else {
		configPath = filepath.Join(root, "fracta.yaml")
	}
	if configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}
		if _, err := os.Stat(configPath); err != nil {
			if explicit {
				return fmt.Errorf("config file not found for daemon: %s", configPath)
			}
			configPath = ""
		}
	}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = root
	cmd.Stdout = nil // daemon runs silently in background
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	// Detach — we don't wait for the daemon process.
	go cmd.Wait()

	// Resolve listen address for health polling. Load from the resolved
	// configPath directly (not loadConfigOrDefault) to avoid re-reading
	// configFlag when we already know which config the daemon was given.
	var cfg *config.Config
	if configPath != "" {
		if loaded, err := config.LoadConfig(configPath); err == nil {
			cfg = loaded
		}
	}
	if cfg == nil {
		cfg = &config.Config{Runtime: config.DefaultRuntime()}
	}
	listenAddr := cfg.ControlPlaneAPI.Listen
	if listenAddr == "" {
		listenAddr = ":9090"
	}
	healthURL := "http://localhost" + listenAddr + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}

	deadline := time.After(daemonHealthTimeout)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("daemon did not become healthy within %s at %s", daemonHealthTimeout, healthURL)
		case <-time.After(healthPollInterval):
			resp, err := client.Get(healthURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					log.Info("control plane daemon started and healthy")
					return nil
				}
			}
		}
	}
}
