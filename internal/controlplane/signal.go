package controlplane

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/fractalog"
)

// StartSignalHandler listens for SIGHUP and hot-reloads config into the
// ControlPlane. It blocks until ctx is done or the stop channel is closed.
// Typically run in a goroutine.
func StartSignalHandler(cp *ControlPlane, configPath string, stop <-chan struct{}) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	logger := fractalog.Component("signal-handler")

	for {
		select {
		case <-stop:
			return
		case <-sigCh:
			logger.Info("received SIGHUP, reloading config", "path", configPath)

			newCfg, err := config.LoadConfig(configPath)
			if err != nil {
				logger.Error("failed to reload config", "error", err)
				continue
			}

			if err := cp.Reconfigure(newCfg); err != nil {
				logger.Error("failed to reconfigure controlplane", "error", err)
				continue
			}

			logger.Info("config reloaded successfully")
		}
	}
}
