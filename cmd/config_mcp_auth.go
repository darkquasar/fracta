package cmd

import "github.com/spf13/cobra"

var configMcpAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage credentials for OAuth-protected MCP servers.",
	Long: `Manage credentials for OAuth-protected MCP servers.

OAuth tokens and dynamic client registrations are stored in the OS keyring
(per token_store config). 'login' starts the browser OAuth flow; 'logout'
removes stored credentials; 'status' shows token validity; 'export' renders
credentials in env/k8s-secret/files formats for deployment.

These verbs moved from 'fracta mcp <verb>' to 'fracta config mcp auth <verb>'
in spec-43. The old top-level form is preserved as a deprecation alias for
one minor release. The hyphenated 'auth-status' is renamed to 'status' on
the new path.`,
}

var configMcpAuthLoginCmd = &cobra.Command{
	Use:   "login <server>",
	Short: "Authenticate with an OAuth-enabled MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPLogin,
}

var configMcpAuthLogoutCmd = &cobra.Command{
	Use:   "logout <server>",
	Short: "Remove stored credentials for an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPLogout,
}

var configMcpAuthStatusCmd = &cobra.Command{
	Use:   "status [server]",
	Short: "Show authentication status for MCP servers",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runMCPAuthStatus,
}

var configMcpAuthExportCmd = &cobra.Command{
	Use:   "export <server>",
	Short: "Export OAuth credentials in various formats",
	Long:  "Export stored credentials for deployment. Formats: k8s-secret, env, files",
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPExport,
}

func init() {
	configMcpAuthLoginCmd.Flags().BoolVar(&mcpLoginDeviceCode, "device-code", false, "use device code flow (for headless servers)")
	configMcpAuthExportCmd.Flags().StringVar(&mcpExportFormat, "format", "env", "output format: k8s-secret, env, files")
	configMcpAuthExportCmd.Flags().StringVar(&mcpExportOutputDir, "output-dir", "", "directory to write files (files format only)")

	configMcpAuthCmd.AddCommand(configMcpAuthLoginCmd)
	configMcpAuthCmd.AddCommand(configMcpAuthLogoutCmd)
	configMcpAuthCmd.AddCommand(configMcpAuthStatusCmd)
	configMcpAuthCmd.AddCommand(configMcpAuthExportCmd)
	configMcpCmd.AddCommand(configMcpAuthCmd)
}
