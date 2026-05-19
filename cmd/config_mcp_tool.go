package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/spf13/cobra"
)

var configMcpToolCmd = &cobra.Command{
	Use:   "tool",
	Short: "Manage individual MCP server tools (enable, disable, list, policy).",
}

var configMcpToolEnableCmd = &cobra.Command{
	Use:   "enable <server> <tool>",
	Short: "Enable a tool on a server.",
	Args:  cobra.ExactArgs(2),
	RunE:  runToolEnable,
}

var configMcpToolDisableCmd = &cobra.Command{
	Use:   "disable <server> <tool>",
	Short: "Disable a tool on a server.",
	Args:  cobra.ExactArgs(2),
	RunE:  runToolDisable,
}

var toolListServer string

var configMcpToolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tools with their enabled/policy/visible status.",
	RunE:  runToolList,
}

var toolPolicyServer string

var configMcpToolPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Show effective tool policy from fracta.yaml.",
	RunE:  runToolPolicy,
}

func init() {
	configMcpCmd.AddCommand(configMcpToolCmd)
	configMcpToolCmd.AddCommand(configMcpToolEnableCmd)
	configMcpToolCmd.AddCommand(configMcpToolDisableCmd)
	configMcpToolCmd.AddCommand(configMcpToolListCmd)
	configMcpToolCmd.AddCommand(configMcpToolPolicyCmd)

	configMcpToolListCmd.Flags().StringVar(&toolListServer, "server", "", "filter by server name")
	configMcpToolPolicyCmd.Flags().StringVar(&toolPolicyServer, "server", "", "filter by server name")
}

func runToolEnable(cmd *cobra.Command, args []string) error {
	server, tool := args[0], args[1]
	svc, _, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := adminCtx()
	if err := svc.SetToolEnabled(ctx, server, tool, true); err != nil {
		return fmt.Errorf("enabling tool: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Enabled tool %q on server %q\n", tool, server)
	return nil
}

func runToolDisable(cmd *cobra.Command, args []string) error {
	server, tool := args[0], args[1]
	svc, _, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := adminCtx()
	if err := svc.SetToolEnabled(ctx, server, tool, false); err != nil {
		return fmt.Errorf("disabling tool: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Disabled tool %q on server %q\n", tool, server)
	return nil
}

func runToolList(cmd *cobra.Command, _ []string) error {
	_, store, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	cfg, err := loadConfigOrDefault(projectRoot)
	if err != nil {
		return err
	}

	ctx := context.Background()
	filter := registry.ToolFilter{}
	if toolListServer != "" {
		filter.ServerName = toolListServer
	}
	tools, err := store.ListTools(ctx, filter)
	if err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	if len(tools) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No tools registered.")
		return nil
	}

	policies := extractToolPolicies(cfg)

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tSERVER\tENABLED\tPOLICY\tVISIBLE")
	for _, t := range tools {
		policy := policies[t.ServerName]
		policyAllowed := gateway.PolicyAllowed(policy, t.ToolName)

		enabled := "yes"
		if !t.Enabled {
			enabled = "no"
		}
		policyStr := "allowed"
		if !policyAllowed {
			policyStr = "denied"
		}
		visible := "yes"
		if !t.Enabled || !policyAllowed {
			visible = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ToolName, t.ServerName, enabled, policyStr, visible)
	}
	w.Flush()
	return nil
}

func runToolPolicy(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfigOrDefault(projectRoot)
	if err != nil {
		return err
	}

	policies := extractToolPolicies(cfg)
	if len(policies) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No tool policies configured.")
		return nil
	}

	printed := false
	for name, policy := range policies {
		if toolPolicyServer != "" && name != toolPolicyServer {
			continue
		}
		if policy == nil {
			continue
		}
		if printed {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Server: %s\n", name)

		denyStr := "(not set)"
		if len(policy.Deny) > 0 {
			denyStr = strings.Join(policy.Deny, ", ")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  deny: %s\n", denyStr)

		allowStr := "(not set)"
		if len(policy.AllowOnly) > 0 {
			allowStr = strings.Join(policy.AllowOnly, ", ")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  allow_only: %s\n", allowStr)

		fmt.Fprintf(cmd.OutOrStdout(), "  Effect: %s\n", describePolicyEffect(policy))
		printed = true
	}

	if !printed && toolPolicyServer != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "No tool policy configured for server %q.\n", toolPolicyServer)
	}
	return nil
}

func extractToolPolicies(cfg *config.Config) map[string]*config.ToolPolicy {
	if cfg == nil {
		return nil
	}
	policies := make(map[string]*config.ToolPolicy)
	for name, entry := range cfg.MCPServers.Servers {
		if entry.ToolPolicy != nil {
			policies[name] = entry.ToolPolicy
		}
	}
	return policies
}

func describePolicyEffect(p *config.ToolPolicy) string {
	if p == nil {
		return "all tools visible"
	}
	if len(p.AllowOnly) > 0 && len(p.Deny) > 0 {
		return fmt.Sprintf("only [%s] visible, minus [%s]", strings.Join(p.AllowOnly, ", "), strings.Join(p.Deny, ", "))
	}
	if len(p.AllowOnly) > 0 {
		return fmt.Sprintf("only [%s] visible", strings.Join(p.AllowOnly, ", "))
	}
	if len(p.Deny) > 0 {
		return fmt.Sprintf("all tools visible except [%s]", strings.Join(p.Deny, ", "))
	}
	return "all tools visible"
} 
