package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkquasar/fracta/internal/contract"
	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	spawnTask     string
	spawnContract string
	spawnBase     string
	spawnModel    string
	spawnTier     string
	spawnMode     string
	spawnRuntime string
	spawnDryRun   bool
	spawnFormat   string
)

var spawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Spawn a new agent with a dedicated worktree",
	RunE:  runSpawn,
}

func init() {
	spawnCmd.Flags().StringVar(&spawnTask, "task", "", "task name (required for spawn, optional for dry-run)")
	spawnCmd.Flags().StringVar(&spawnContract, "contract", "", "contract text or path to contract file (optional)")
	spawnCmd.Flags().StringVar(&spawnBase, "base", "", "base branch (default: from config)")
	spawnCmd.Flags().StringVar(&spawnModel, "model", "", "model to use (overrides config and tier)")
	spawnCmd.Flags().StringVar(&spawnTier, "tier", "", "model tier: heavy, medium, or light (mapped via config model_tiers)")
	spawnCmd.Flags().StringVar(&spawnMode, "mode", "", "agent mode: batch (default) or stream (MCP-only)")
	spawnCmd.Flags().StringVar(&spawnRuntime, "runtime", "", "runtime implementation (default: registry default)")
	spawnCmd.Flags().StringVar(&spawnRuntime, "host-type", "", "deprecated: use --runtime instead")
	_ = spawnCmd.Flags().MarkDeprecated("host-type", "use --runtime instead")
	spawnCmd.Flags().BoolVar(&spawnDryRun, "dry-run", false, "resolve the full spawn chain without creating an agent")
	spawnCmd.Flags().StringVar(&spawnFormat, "format", "yaml", "output format for dry-run: yaml or json")
	rootCmd.AddCommand(spawnCmd)
}

func runSpawn(cmd *cobra.Command, args []string) error {
	if spawnDryRun {
		return runDryRun(cmd)
	}

	if spawnTask == "" {
		return fmt.Errorf("required flag \"task\" not set")
	}

	if spawnMode == "stream" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: streaming mode is MCP-only; falling back to batch mode for CLI")
		spawnMode = "batch"
	}

	// Resolve base branch from unified config (fracta.yaml).
	baseBranch := spawnBase
	if baseBranch == "" {
		cfg, err := loadConfigOrDefault(projectRoot)
		if err != nil {
			return err
		}
		baseBranch = cfg.Project.DefaultBaseBranch
	}

	content, err := contract.ResolveContract(spawnContract)
	if err != nil {
		return fmt.Errorf("resolving contract: %w", err)
	}

	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.Spawn(context.Background(), cpapi.SpawnRequest{
		Task:        spawnTask,
		Contract:    content,
		BaseBranch:  baseBranch,
		Model:       spawnModel,
		Tier:        spawnTier,
		RuntimeType: spawnRuntime,
		Mode:        spawnMode,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Agent %q spawned successfully.\n", resp.Agent)
	return nil
}

func runDryRun(cmd *cobra.Command) error {
	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.DryRunSpawn(context.Background(), cpapi.DryRunRequest{
		Task:        spawnTask,
		RuntimeType: spawnRuntime,
		Model:       spawnModel,
		Tier:        spawnTier,
		Format:      spawnFormat,
	})
	if err != nil {
		return err
	}

	switch spawnFormat {
	case "json":
		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling response: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	default:
		data, err := yaml.Marshal(resp)
		if err != nil {
			return fmt.Errorf("marshaling response: %w", err)
		}
		fmt.Fprint(cmd.OutOrStdout(), string(data))
	}

	return nil
}
