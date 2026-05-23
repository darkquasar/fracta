package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
)

var (
	manifestVariant    string
	manifestNamespace  string
	manifestImage      string
	manifestCatalogDir string
	manifestOutput     string
)

var configMcpManifestCmd = &cobra.Command{
	Use:   "manifest <server>",
	Short: "Emit deployment artifacts for an MCP backend from the catalog.",
	Long: `Render the manifest snippet for an MCP backend without touching any project.

The default output is a Deployment + Service pair suitable for kubectl apply.
Other modes emit a docker-compose service block or a fracta.yaml
mcp_servers.servers.<id> snippet.

The server id matches the entry in mcp-servers/catalog.yaml (flat — there is
no vendor prefix). Use --catalog-dir to point at a catalog outside the
current project (e.g. when running from a non-fracta directory):

  fracta config mcp manifest fracta-test-server \
      --catalog-dir ~/GitHub/fracta/mcp-servers \
      | kubectl apply -f -

This command does NOT require a fracta project directory.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigMcpManifest,
}

func init() {
	configMcpManifestCmd.Flags().StringVar(&manifestVariant, "variant", "", "variant name (default: first variant supporting the chosen output)")
	configMcpManifestCmd.Flags().StringVar(&manifestNamespace, "namespace", "", "kubernetes namespace (k8s output only; default: fracta)")
	configMcpManifestCmd.Flags().StringVar(&manifestImage, "image", "", "override the variant's image (k8s/compose output only)")
	configMcpManifestCmd.Flags().StringVar(&manifestCatalogDir, "catalog-dir", "", "path to mcp-servers/ catalog (default: project's mcp-servers/, else cwd/mcp-servers)")
	configMcpManifestCmd.Flags().StringVarP(&manifestOutput, "output", "o", "k8s", "output format: k8s | compose | fracta-yaml")
	configMcpCmd.AddCommand(configMcpManifestCmd)
}

func runConfigMcpManifest(cmd *cobra.Command, args []string) error {
	serverID := args[0]

	cat, err := loadManifestCatalog()
	if err != nil {
		return err
	}

	entry, ok := cat.Get(serverID)
	if !ok {
		return fmt.Errorf("server %q not found in catalog", serverID)
	}

	opts := mcpcatalog.RenderOpts{
		Namespace: manifestNamespace,
		Variant:   manifestVariant,
		ImageTag:  manifestImage,
	}

	var out []byte
	switch manifestOutput {
	case "k8s":
		out, err = entry.RenderK8sManifest(opts)
	case "compose":
		out, err = entry.RenderComposeService(opts)
	case "fracta-yaml":
		// Pick the most-supported mode for the YAML snippet; prefer k8s, then
		// compose, then local. Operators usually want the snippet that
		// matches their deployment, so they can also pass --variant to nail
		// it down.
		mode := pickFractaYAMLMode(entry)
		out, err = entry.RenderFractaYAMLBlock(mode, opts)
	default:
		return fmt.Errorf("unknown --output %q (want: k8s | compose | fracta-yaml)", manifestOutput)
	}
	if err != nil {
		return err
	}

	_, werr := cmd.OutOrStdout().Write(out)
	return werr
}

// loadManifestCatalog resolves the catalog source per the precedence:
//  1. --catalog-dir flag (explicit)
//  2. project's mcp-servers/ (if we can find a project root)
//  3. cwd/mcp-servers/
//
// Failure to load the chosen catalog is a hard error; the precedence only
// chooses what to try, not what to tolerate.
func loadManifestCatalog() (*mcpcatalog.Catalog, error) {
	if manifestCatalogDir != "" {
		return mcpcatalog.LoadCatalog(os.DirFS(manifestCatalogDir))
	}
	// Best-effort project lookup; no error if missing.
	if root, err := FindProjectRoot(""); err == nil {
		cat, err := mcpcatalog.LoadProjectCatalog(root)
		if err == nil {
			return cat, nil
		}
		if !errors.Is(err, mcpcatalog.ErrNoCatalog) {
			return nil, err
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return mcpcatalog.LoadCatalog(os.DirFS(filepath.Join(cwd, "mcp-servers")))
}

// pickFractaYAMLMode returns the most-deployable mode an entry supports for
// the fracta-yaml output. k8s wins, then compose, then local.
func pickFractaYAMLMode(e *mcpcatalog.Entry) scaffolds.Kind {
	if e.SupportsMode(scaffolds.KindK8s) {
		return scaffolds.KindK8s
	}
	if e.SupportsMode(scaffolds.KindDockerCompose) {
		return scaffolds.KindDockerCompose
	}
	return scaffolds.KindLocal
}
