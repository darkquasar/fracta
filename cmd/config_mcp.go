package cmd

import "github.com/spf13/cobra"

var configMcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Fetch the MCP server catalog and inject servers into deployment configs.",
	Long: `Manage MCP servers in this fracta project.

The catalog at <root>/mcp-servers/ is first-class checked-in config (operators
commit it and review changes). 'fracta config mcp fetch' populates it from a
catalog source. 'list', 'inspect', 'add', and 'remove' then operate on the
local catalog. 'auth' manages OAuth credentials for OAuth-protected servers.`,
}

func init() {
	configCmd.AddCommand(configMcpCmd)
}
