// Build directive: go build -o bin/fracta ./cmd/...
// (Required for .mcp.json to find the binary when the host starts the MCP server)
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/spf13/cobra"
)

var (
	projectRoot    string
	configFlag     string // --config: explicit path to fracta.yaml (persistent, all subcommands)
	clientModeFlag string // --client-mode: "auto", "local", "remote" (persistent, all subcommands)
)

// Command annotation keys. Commands set these in their init() to declare what
// they need from the surrounding environment; the persistent root hook reads
// them and gates project resolution accordingly. See spec-49 §1.
//
// Commands that need neither (daemons like serve/worker/controlplane, and
// general utilities like debug …) leave their Annotations map empty.
const (
	// RequiresFractaYAMLAnnotation: command needs a loaded fracta.yaml. The
	// root hook walks up from cwd (or --root) for a .fracta/ marker; failure
	// is a hard error.
	RequiresFractaYAMLAnnotation = "fracta:requires_fracta_yaml"

	// RequiresGitWorktreeAnnotation: command will spawn agent worktrees, so
	// the resolved project root must also be a .git repository. The root hook
	// asserts this only when the resolved fracta.yaml declares a local-mode
	// deployment (runtime.backend == "local"). In kubernetes or docker-compose
	// projects, agents run as Jobs/services and worktrees are irrelevant.
	RequiresGitWorktreeAnnotation = "fracta:requires_git_worktree"
)

// commandHasAnnotation walks cmd and its parents looking for key=="true".
// Annotations applied to a parent command propagate to all subcommands.
func commandHasAnnotation(cmd *cobra.Command, key string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations != nil && c.Annotations[key] == "true" {
			return true
		}
	}
	return false
}

var rootCmd = &cobra.Command{
	Use:   "fracta",
	Short: "Local orchestrator for AI agents",
	Long:  "Fracta manages git worktrees and agent sessions so multiple AI agents can work in parallel.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !commandHasAnnotation(cmd, RequiresFractaYAMLAnnotation) {
			return nil
		}
		root, err := FindProjectRoot(projectRoot)
		if err != nil {
			return err
		}
		projectRoot = root

		if commandHasAnnotation(cmd, RequiresGitWorktreeAnnotation) {
			cfg, _ := loadConfigOrDefault(root)
			profile := "local"
			if cfg != nil {
				profile = cfg.ResolvedProfile()
				if cfg.IsDockerCompose(root) {
					profile = "docker-compose"
				}
			}
			if profile == "local" {
				if err := assertGitWorktree(root); err != nil {
					return err
				}
			}
		}
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

// binaryVersion is the same string SetVersion attached to rootCmd; subcommands
// (e.g. init's source-description line) read it via Version().
var binaryVersion = "dev"

// SetVersion attaches the binary version to the root command. Called from main.
// The version string is set at build time via:
//
//	go build -ldflags "-X main.version=v1.2.3" .
func SetVersion(v string) {
	rootCmd.Version = v
	binaryVersion = v
}

// Version returns the binary's compiled version. "dev" for local builds.
func Version() string {
	return binaryVersion
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

// assertGitWorktree returns an error when root is not a git repository or
// worktree. fracta itself uses worktrees, so .git can be either a directory
// (main checkout) or a file (worktree pointer to a gitdir).
func assertGitWorktree(root string) error {
	info, err := os.Stat(filepath.Join(root, ".git"))
	if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
		return nil
	}
	return fmt.Errorf("local-process deployments require a git repository at the project root "+
		"(no .git found in %s). Initialise one with 'git init' or switch the project to "+
		"runtime.backend: kubernetes (or scaffold as docker-compose).", root)
}
