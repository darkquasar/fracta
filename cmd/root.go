// Build directive: go build -o bin/fracta ./cmd/...
// (Required for .mcp.json to find the binary when the host starts the MCP server)
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/spf13/cobra"
)

var (
	projectRoot    string
	configFlag     string // --config: explicit path to fracta.yaml (persistent, all subcommands)
	clientModeFlag string // --client-mode: "auto", "local", "remote" (persistent, all subcommands)
)

var rootCmd = &cobra.Command{
	Use:   "fracta",
	Short: "Local orchestrator for AI agents",
	Long:  "Fracta manages git worktrees and agent sessions so multiple AI agents can work in parallel.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// init, serve, and worker commands bypass the .fracta check
		if cmd.Name() == "init" || cmd.Name() == "serve" || cmd.Name() == "worker" || cmd.Name() == "controlplane" || cmd.Name() == "start" || cmd.Name() == "stop" || cmd.Name() == "status" {
			return nil
		}
		root, err := FindProjectRoot(projectRoot)
		if err != nil {
			return err
		}
		projectRoot = root
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	fractalog.Init()
	rootCmd.PersistentFlags().StringVar(&projectRoot, "root", "", "project root directory (default: current directory)")
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "path to fracta.yaml config file (default: <root>/fracta.yaml)")
	rootCmd.PersistentFlags().StringVar(&clientModeFlag, "client-mode", "", "control-plane client mode: 'auto' (default), 'local' (host-side orchestrator), 'remote' (in-cluster CP API)")
}

func Execute() error {
	return rootCmd.Execute()
}

// SetVersion attaches the binary version to the root command. Called from main.
// The version string is set at build time via:
//
//	go build -ldflags "-X main.version=v1.2.3" .
func SetVersion(v string) {
	rootCmd.Version = v
}

// FindProjectRoot walks up from start looking for a .fracta directory.
func FindProjectRoot(start string) (string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}

	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, model.FractaDir)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not a fracta project (no .fracta directory found); run 'fracta init' first")
}
