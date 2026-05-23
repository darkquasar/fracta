package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mcpAliasCmd = &cobra.Command{
	Use:   "mcp",
	Short: "[DEPRECATED] Use 'fracta config mcp' instead.",
	Long: `[DEPRECATED] 'fracta mcp <verb>' has moved to 'fracta config mcp auth <verb>'.

This top-level path is a deprecation alias and will be removed in a future
minor release. The hyphenated 'auth-status' subcommand is renamed to 'status'
on the new path; the hyphenated form is preserved here as part of the alias.

Replacement table:
  fracta mcp login <server>       -> fracta config mcp auth login <server>
  fracta mcp logout <server>      -> fracta config mcp auth logout <server>
  fracta mcp auth-status [server] -> fracta config mcp auth status [server]
  fracta mcp export <server>      -> fracta config mcp auth export <server>`,
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

var mcpAliasLoginCmd = &cobra.Command{
	Use:   "login <server>",
	Short: "[DEPRECATED] Authenticate with an OAuth-enabled MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  deprecatedAlias("login", runMCPLogin),
}

var mcpAliasLogoutCmd = &cobra.Command{
	Use:   "logout <server>",
	Short: "[DEPRECATED] Remove stored credentials for an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  deprecatedAlias("logout", runMCPLogout),
}

// auth-status keeps its hyphenated name on the alias path. The deprecation
// warning emits the renamed verb ("status") for the new path so operators
// don't have to guess at the rename.
var mcpAliasAuthStatusCmd = &cobra.Command{
	Use:   "auth-status [server]",
	Short: "[DEPRECATED] Show authentication status for MCP servers",
	Args:  cobra.MaximumNArgs(1),
	RunE:  deprecatedAlias("status", runMCPAuthStatus),
}

var mcpAliasExportCmd = &cobra.Command{
	Use:   "export <server>",
	Short: "[DEPRECATED] Export OAuth credentials in various formats",
	Long:  "Export stored credentials for deployment. Formats: k8s-secret, env, files",
	Args:  cobra.ExactArgs(1),
	RunE:  deprecatedAlias("export", runMCPExport),
}

func init() {
	mcpAliasLoginCmd.Flags().BoolVar(&mcpLoginDeviceCode, "device-code", false, "use device code flow (for headless servers)")
	mcpAliasExportCmd.Flags().StringVar(&mcpExportFormat, "format", "env", "output format: k8s-secret, env, files")
	mcpAliasExportCmd.Flags().StringVar(&mcpExportOutputDir, "output-dir", "", "directory to write files (files format only)")

	mcpAliasCmd.AddCommand(mcpAliasLoginCmd)
	mcpAliasCmd.AddCommand(mcpAliasLogoutCmd)
	mcpAliasCmd.AddCommand(mcpAliasAuthStatusCmd)
	mcpAliasCmd.AddCommand(mcpAliasExportCmd)
	rootCmd.AddCommand(mcpAliasCmd)
}

// deprecatedAlias wraps a runner with a stderr warning that names the new
// command path verbatim (so operators can copy-paste the fix). The newPath
// argument is the subcommand under 'fracta config mcp auth' — i.e. "login",
// "logout", "status", or "export". The wrapper reads the alias's own
// invocation name from cmd.Use so the warning shows the actual deprecated
// verb (e.g. "auth-status") rather than parroting newPath back.
func deprecatedAlias(newPath string, runner func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		oldVerb := cmd.Name()
		fmt.Fprintf(os.Stderr,
			"warning: 'fracta mcp %s' is deprecated; use 'fracta config mcp auth %s'. This alias will be removed in a future minor release.\n",
			oldVerb, newPath)
		return runner(cmd, args)
	}
}
