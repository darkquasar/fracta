package cmd

import "github.com/spf13/cobra"

var debugRegistryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Manage the MCP server registry (debug/admin).",
}

func init() {
	debugCmd.AddCommand(debugRegistryCmd)
	debugRegistryCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered MCP servers",
		RunE:  runRegistryList,
	})
	debugRegistryCmd.AddCommand(&cobra.Command{
		Use:   "add <name>",
		Short: "Register a new MCP server",
		Args:  cobra.ExactArgs(1),
		RunE:  runRegistryAdd,
	})
	debugRegistryCmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a registered MCP server",
		Args:  cobra.ExactArgs(1),
		RunE:  runRegistryRemove,
	})
	debugRegistryCmd.AddCommand(&cobra.Command{
		Use:   "status [name]",
		Short: "Show registry status or details for a specific server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runRegistryStatus,
	})
}
