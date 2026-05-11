package cmd

import "github.com/spf13/cobra"

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage project configuration files in this fracta project.",
	Long: `Manage project configuration files in this fracta project.

Subcommands group related project-config operations. Today this is the home of
'fracta config mcp ...' (MCP server catalog management). Future siblings
('validate', 'show', 'migrate') will land here without further top-level
CLI churn.`,
}

func init() {
	rootCmd.AddCommand(configCmd)
}
