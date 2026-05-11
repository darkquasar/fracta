package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/spf13/cobra"
)

var (
	fetchMergeFlag          bool
	fetchFilterFlag         string
	fetchSourceChecksumFlag string
	fetchYesFlag            bool
)

var configMcpFetchCmd = &cobra.Command{
	Use:   "fetch [<source>]",
	Short: "Populate <root>/mcp-servers/ from a catalog source.",
	Long: `Fetches the MCP server catalog into <root>/mcp-servers/. Operators
commit the result.

Source positional argument (optional; default: github:darkquasar/fracta@main):
  github:owner/repo@ref                       GitHub repo at tag, branch, or sha
  https://github.com/owner/repo[@ref]         Browser URL form (trailing slash and
                                              .git suffix tolerated)
  git@github.com:owner/repo[.git][@ref]       SSH form (fetched over HTTPS codeload)
  https://example.com/cat.tar.gz              Raw HTTPS tarball
  ./local/path  or  /abs/path                 Local directory with mcp-servers/ inside

Without --merge, fetch wholesale-replaces <root>/mcp-servers/ (existing entries
that aren't in the new source are removed). With --merge, local-only entries
are preserved; entries present in both are replaced by the remote.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runConfigMcpFetch,
}

func init() {
	configMcpFetchCmd.Flags().BoolVar(&fetchMergeFlag, "merge", false,
		"Preserve local-only entries by id; replace entries that exist on both sides.")
	configMcpFetchCmd.Flags().StringVar(&fetchFilterFlag, "filter", "",
		"Filter expression, e.g. 'status=tested,category=knowledge'. Entries failing the filter are not written.")
	configMcpFetchCmd.Flags().StringVar(&fetchSourceChecksumFlag, "source-checksum", "",
		"sha256:<hex> for raw https://...tar.gz sources. Silently ignored for github / git@ / path sources.")
	configMcpFetchCmd.Flags().BoolVar(&fetchYesFlag, "yes", false,
		"Skip the 'this will overwrite N entries' confirmation prompt.")

	configMcpCmd.AddCommand(configMcpFetchCmd)
}

func runConfigMcpFetch(cmd *cobra.Command, args []string) error {
	explicit := ""
	if len(args) == 1 {
		explicit = args[0]
	}

	source, err := mcpcatalog.ResolveFetchSource(projectRoot, explicit)
	if err != nil {
		return err
	}

	flt, err := mcpcatalog.ParseFilter(fetchFilterFlag)
	if err != nil {
		return err
	}

	opts := mcpcatalog.FetchOpts{
		Source:         source,
		Merge:          fetchMergeFlag,
		Filter:         flt,
		SourceChecksum: fetchSourceChecksumFlag,
	}

	// Pre-flight: if there's an existing catalog, show what will change.
	localBefore, _ := mcpcatalog.LoadProjectCatalog(projectRoot)
	writeFetchPreflight(cmd.OutOrStdout(), source, fetchMergeFlag, localBefore)

	if !fetchYesFlag && localBefore != nil {
		fmt.Fprint(cmd.OutOrStdout(), "Proceed? [y/N] ")
		if !promptYes(cmd.InOrStdin()) {
			return errors.New("aborted")
		}
	}

	result, err := mcpcatalog.Fetch(context.Background(), projectRoot, opts)
	if err != nil {
		if errors.Is(err, mcpcatalog.ErrEmptyFetchSource) {
			return fmt.Errorf("fetch: empty source spec (pass a positional argument or rely on default %q)",
				mcpcatalog.DefaultFetchSourceSpec)
		}
		return fmt.Errorf("fetch: %w", err)
	}

	writeFetchSummary(cmd.OutOrStdout(), result, fetchMergeFlag)
	return nil
}

func writeFetchPreflight(w io.Writer, source string, merge bool, local *mcpcatalog.Catalog) {
	fmt.Fprintf(w, "Fetching catalog from %s\n", source)
	if local == nil {
		fmt.Fprintln(w, "  Local catalog state:    not present (will be created)")
		return
	}
	fmt.Fprintf(w, "  Local catalog state:    %d entries\n", len(local.Entries))
	if merge {
		fmt.Fprintln(w, "  Mode:                   merge (preserve local-only entries; .fracta-source unchanged)")
	} else {
		fmt.Fprintln(w, "  Mode:                   replace (use --merge to preserve local-only entries)")
	}
}

func writeFetchSummary(w io.Writer, r *mcpcatalog.FetchResult, merge bool) {
	if r == nil {
		return
	}
	verb := "Replaced"
	if merge {
		verb = "Merged"
	}
	entries := 0
	if r.RemoteCatalog != nil {
		entries = len(r.RemoteCatalog.Entries)
	}
	fmt.Fprintf(w, "%s catalog at <root>/mcp-servers/ from %s (catalog.yaml version: %s, %d entries)\n",
		verb, r.SourceUsed, valOrDash(r.CatalogVersion), entries)
	if len(r.Added) > 0 {
		fmt.Fprintf(w, "  added: %s\n", strings.Join(r.Added, ", "))
	}
	if len(r.Removed) > 0 {
		fmt.Fprintf(w, "  removed: %s\n", strings.Join(r.Removed, ", "))
	}
	if len(r.Changed) > 0 {
		fmt.Fprintf(w, "  changed: %s\n", strings.Join(r.Changed, ", "))
	}
	if !merge {
		fmt.Fprintln(w, "  recorded source in mcp-servers/.fracta-source")
	}
}
