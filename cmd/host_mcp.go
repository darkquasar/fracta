package cmd

import (
	"github.com/darkquasar/fracta/internal/hostmcp"
	"github.com/spf13/cobra"
)

var hostMCPCmd = &cobra.Command{
	Use:   "host-mcp",
	Short: "Start the host-facing MCP server for lifecycle operations",
	Long: `Start an MCP server on stdio that exposes lifecycle tools (spawn, list,
peek, say, kill, logs) through the ControlPlaneClient abstraction.

This is designed for use as an MCP server in host tool configurations
(e.g. Claude Desktop, IDE plugins). It provides the same lifecycle
semantics as the CLI but via MCP tool calls.`,
	RunE: runHostMCP,
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

func init() {
	rootCmd.AddCommand(hostMCPCmd)
}

func runHostMCP(cmd *cobra.Command, args []string) error {
	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := hostmcp.New(client)
	return srv.Serve()
}
