package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	listTargetDeploymentFlag string
	listRemoteFlag           bool
	listFilterFlag           string
	listOutputFlag           string
	listNoImageStateFlag     bool
	listRenderTableFlag      bool
	listSimpleFlag           bool
)

var configMcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every MCP server in the local catalog.",
	Long: `List every MCP server in <root>/mcp-servers/, with a column per
deployment mode showing whether the server is currently wired up locally.

If <root>/mcp-servers/ is missing or empty, errors with remediation pointing
at 'fracta config mcp fetch'.`,
	Args: cobra.NoArgs,
	RunE: runConfigMcpList,
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

func init() {
	configMcpListCmd.Flags().StringVar(&listTargetDeploymentFlag, "target-deployment", "",
		"Filter to one mode column: local | docker-compose | k8s | all. Default: only-enabled-mode if exactly one is scaffolded, else 'all'.")
	configMcpListCmd.Flags().BoolVar(&listRemoteFlag, "remote", false,
		"Compare local catalog against the canonical remote (read-only diff).")
	configMcpListCmd.Flags().StringVar(&listFilterFlag, "filter", "",
		"Filter expression, e.g. 'status=tested,category=knowledge' (AND-combined keys).")
	configMcpListCmd.Flags().StringVar(&listOutputFlag, "output", "table",
		"Output format: table | json | yaml")
	configMcpListCmd.Flags().BoolVar(&listNoImageStateFlag, "no-image-state", false,
		"Skip docker/podman image presence inspection (faster).")
	configMcpListCmd.Flags().BoolVar(&listRenderTableFlag, "render-table", false,
		"Force horizontal table output (all columns).")
	configMcpListCmd.Flags().BoolVar(&listSimpleFlag, "simple", false,
		"Simplified output: name, transport, status, modes only.")

	configMcpCmd.AddCommand(configMcpListCmd)
}

// listRow is one row in the local-list table/JSON output.
type listRow struct {
	ID         string   `json:"id" yaml:"id"`
	Name       string   `json:"name" yaml:"name"`
	Status     string   `json:"status" yaml:"status"`
	Category   string   `json:"category" yaml:"category"`
	AuthModes  []string `json:"auth_modes,omitempty" yaml:"auth_modes,omitempty"`
	Transport  string   `json:"transport,omitempty" yaml:"transport,omitempty"`
	Image      string   `json:"image,omitempty" yaml:"image,omitempty"`
	ImageState string   `json:"image_state,omitempty" yaml:"image_state,omitempty"`
	// Configured[mode-string] is true when the server is wired up in that mode.
	Configured map[string]bool `json:"configured" yaml:"configured"`
}

func runConfigMcpList(cmd *cobra.Command, _ []string) error {
	if listRemoteFlag {
		return runConfigMcpListRemote(cmd)
	}

	cat, err := mcpcatalog.LoadProjectCatalog(projectRoot)
	if err != nil {
		if errors.Is(err, mcpcatalog.ErrNoCatalog) {
			return errNoCatalogRemediation
		}
		return err
	}

	state, err := mcpcatalog.LoadProjectState(projectRoot)
	if err != nil {
		return fmt.Errorf("read project state: %w", err)
	}

	flt, err := mcpcatalog.ParseFilter(listFilterFlag)
	if err != nil {
		return err
	}

	wanted, err := resolveTargetDeploymentFilter(listTargetDeploymentFlag, state)
	if err != nil {
		return err
	}

	var inspector mcpcatalog.ImageInspector
	if !listNoImageStateFlag {
		inspector = mcpcatalog.DetectImageInspector()
	}

	rows := buildListRows(cat, state, inspector, flt)

	switch listOutputFlag {
	case "table", "":
		if listSimpleFlag {
			return renderListSimple(cmd.OutOrStdout(), rows, wanted)
		}
		return renderListTable(cmd.OutOrStdout(), rows, wanted)
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "yaml":
		enc := yaml.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(rows)
	default:
		return fmt.Errorf("unknown --output %q (supported: table, json, yaml)", listOutputFlag)
	}
}

// resolveTargetDeploymentFilter picks the column set the operator wants.
// "" → only-enabled-mode if exactly one is scaffolded; else all.
// "all" → all three modes.
// "local" | "docker-compose" | "k8s" → that single mode.
//
// Returns a slice of kinds in display order; for "all" the slice has three
// entries.
func resolveTargetDeploymentFilter(flag string, state *mcpcatalog.ProjectState) ([]scaffolds.Kind, error) {
	switch flag {
	case "":
		if state != nil {
			if only, ok := state.OnlyEnabled(); ok {
				return []scaffolds.Kind{only}, nil
			}
		}
		return scaffolds.AllKinds(), nil
	case "all":
		return scaffolds.AllKinds(), nil
	case "local":
		return []scaffolds.Kind{scaffolds.KindLocal}, nil
	case "docker-compose":
		return []scaffolds.Kind{scaffolds.KindDockerCompose}, nil
	case "k8s":
		return []scaffolds.Kind{scaffolds.KindK8s}, nil
	default:
		return nil, fmt.Errorf("unknown --target-deployment %q (supported: local, docker-compose, k8s, all)", flag)
	}
}

func buildListRows(cat *mcpcatalog.Catalog, state *mcpcatalog.ProjectState, inspector mcpcatalog.ImageInspector, flt mcpcatalog.Filter) []listRow {
	rows := make([]listRow, 0, len(cat.Entries))
	for _, id := range cat.SortedIDs() {
		entry := cat.Entries[id]
		if !flt.Match(entry) {
			continue
		}
		r := listRow{
			ID:        entry.ID,
			Name:      entry.Name,
			Status:    entry.Status,
			Category:  entry.Category,
			AuthModes: entry.Auth.Modes,
			Image:     entry.ImageRef(),
			Configured: map[string]bool{
				scaffolds.KindLocal.String():         false,
				scaffolds.KindDockerCompose.String(): false,
				scaffolds.KindK8s.String():           false,
			},
		}
		r.Transport = primaryTransport(entry)
		if state != nil {
			if modes, ok := state.Configured[id]; ok {
				for k, v := range modes {
					r.Configured[k.String()] = v
				}
			}
		}
		if inspector != nil && r.Image != "" {
			st, _ := inspector.HasImage(context.Background(), r.Image)
			switch st {
			case mcpcatalog.ImageStatePresent:
				r.ImageState = "present (" + inspector.Name() + ")"
			case mcpcatalog.ImageStateAbsent:
				r.ImageState = "absent (" + inspector.Name() + ")"
			default:
				r.ImageState = "unknown"
			}
		} else if r.Image == "" {
			r.ImageState = "n/a"
		}
		rows = append(rows, r)
	}
	return rows
}

// primaryTransport picks the most operator-relevant transport string for the
// list table: prefer container.transport, else local.transport.
func primaryTransport(e *mcpcatalog.Entry) string {
	if c, ok := e.Variants["container"]; ok && c.Transport != "" {
		return c.Transport
	}
	if l, ok := e.Variants["local"]; ok && l.Transport != "" {
		return l.Transport
	}
	if lp, ok := e.Variants["local_proxy"]; ok && lp.Transport != "" {
		return lp.Transport
	}
	if r, ok := e.Variants["remote"]; ok && r.Transport != "" {
		return r.Transport
	}
	return ""
}

func renderListTable(w io.Writer, rows []listRow, wanted []scaffolds.Kind) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := []string{"SERVER", "MODES", "AUTH", "TRANSPORT", "IMAGE", "IMAGE STATE", "STATUS"}
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		modes := formatModesCell(r, wanted)
		auth := strings.Join(r.AuthModes, ",")
		if auth == "" {
			auth = "-"
		}
		transport := r.Transport
		if transport == "" {
			transport = "-"
		}
		image := r.Image
		if image == "" {
			image = "-"
		}
		state := r.ImageState
		if state == "" {
			state = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, modes, auth, transport, image, state, r.Status)
	}
	return tw.Flush()
}

// formatModesCell renders the per-mode tick marks for one row.
func formatModesCell(r listRow, wanted []scaffolds.Kind) string {
	parts := make([]string, 0, len(wanted))
	for _, k := range wanted {
		short := modeShort(k)
		if r.Configured[k.String()] {
			parts = append(parts, short+"yes")
		} else {
			parts = append(parts, short+"no")
		}
	}
	return strings.Join(parts, " ")
}

// modeShort returns a compact prefix for the per-mode cell.
func modeShort(k scaffolds.Kind) string {
	switch k {
	case scaffolds.KindLocal:
		return "local:"
	case scaffolds.KindDockerCompose:
		return "compose:"
	case scaffolds.KindK8s:
		return "k8s:"
	default:
		return k.String() + ":"
	}
}

// errNoCatalogRemediation is the user-visible message printed when the local
// catalog is missing. Phrased so that main.go's "Error: <msg>" prefix produces
// the spec §5.3 remediation text.
var errNoCatalogRemediation = errors.New(
	"no catalog found at <root>/mcp-servers/\n" +
		"run 'fracta config mcp fetch' to populate it (default source: github:darkquasar/fracta@main)",
)

// runConfigMcpListRemote handles `list --remote`. Reads the local catalog,
// fetches the canonical remote into a throwaway temp directory (not the
// project tree), and renders a marketplace-style diff table.
//
// Offline / fetch failures: degrade gracefully — write a "remote unavailable"
// warning to stderr and fall back to the local-only table. Exit 0.
func runConfigMcpListRemote(cmd *cobra.Command) error {
	// Marketplace-browser behaviour: an absent local catalog is fine. We
	// substitute an empty Catalog so every remote entry shows up as "available"
	// and the LOCAL column renders as "not fetched". This lets operators run
	// `fracta config mcp list --remote` immediately after `fracta init`
	// to see what's available before deciding whether to fetch.
	localCat, err := mcpcatalog.LoadProjectCatalog(projectRoot)
	if err != nil {
		if errors.Is(err, mcpcatalog.ErrNoCatalog) {
			localCat = &mcpcatalog.Catalog{Entries: map[string]*mcpcatalog.Entry{}}
		} else {
			return err
		}
	}

	source, err := mcpcatalog.ResolveFetchSource(projectRoot, "")
	if err != nil {
		return err
	}

	flt, err := mcpcatalog.ParseFilter(listFilterFlag)
	if err != nil {
		return err
	}

	// Fetch into a temporary "project root" so we never touch <root>/mcp-servers/.
	tmpRoot, err := os.MkdirTemp("", "fracta-remote-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)

	result, err := mcpcatalog.Fetch(context.Background(), tmpRoot, mcpcatalog.FetchOpts{
		Source: source,
		Filter: flt,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote unavailable: %v\n", err)
		// Degrade to local view.
		state, _ := mcpcatalog.LoadProjectState(projectRoot)
		wanted, _ := resolveTargetDeploymentFilter(listTargetDeploymentFlag, state)
		rows := buildListRows(localCat, state, nil, flt)
		return renderListTable(cmd.OutOrStdout(), rows, wanted)
	}

	delta := mcpcatalog.Diff(localCat, result.RemoteCatalog)
	state, _ := mcpcatalog.LoadProjectState(projectRoot)
	return renderRemoteTable(cmd.OutOrStdout(), localCat, result.RemoteCatalog, delta, state, result.CatalogVersion)
}

// renderListSimple shows a condensed view: name, transport, status, modes.
func renderListSimple(w io.Writer, rows []listRow, wanted []scaffolds.Kind) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVER\tTRANSPORT\tSTATUS\tMODES")
	for _, r := range rows {
		transport := r.Transport
		if transport == "" {
			transport = "-"
		}
		modes := formatModesCell(r, wanted)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, transport, r.Status, modes)
	}
	return tw.Flush()
}

// renderRemoteTable prints the marketplace-style diff between the local
// catalog and the remote. Columns: SERVER, LOCAL, REMOTE, DELTA, AUTH,
// DESCRIPTION. LOCAL describes whether the operator has wired up the server
// for any mode; REMOTE shows the catalog.yaml version; DELTA names the
// difference bucket (up-to-date | available | local-only | changed).
func renderRemoteTable(w io.Writer, local, remote *mcpcatalog.Catalog, delta mcpcatalog.Delta, state *mcpcatalog.ProjectState, version string) error {
	// An empty local catalog means the operator hasn't run `fracta config mcp
	// fetch` yet. Render LOCAL as "not fetched" rather than "not configured"
	// so the meaning is unambiguous.
	localEmpty := local == nil || len(local.Entries) == 0

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join([]string{"SERVER", "LOCAL", "REMOTE", "DELTA", "AUTH", "DESCRIPTION"}, "\t"))

	// Build a single sorted list of every id we know about (union of local + remote).
	ids := map[string]bool{}
	if local != nil {
		for id := range local.Entries {
			ids[id] = true
		}
	}
	if remote != nil {
		for id := range remote.Entries {
			ids[id] = true
		}
	}
	allIDs := make([]string, 0, len(ids))
	for id := range ids {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)

	changed := map[string]bool{}
	for _, p := range delta.Changed {
		if p.Remote != nil {
			changed[p.Remote.ID] = true
		}
	}
	added := map[string]bool{}
	for _, e := range delta.Added {
		added[e.ID] = true
	}
	removed := map[string]bool{}
	for _, e := range delta.Removed {
		removed[e.ID] = true
	}

	for _, id := range allIDs {
		var le, re *mcpcatalog.Entry
		if local != nil {
			le = local.Entries[id]
		}
		if remote != nil {
			re = remote.Entries[id]
		}
		// Prefer the remote entry for descriptive fields (auth, description)
		// since it's the authoritative source. Fall back to local for
		// removed entries.
		entry := re
		if entry == nil {
			entry = le
		}
		desc := ""
		auth := "-"
		if entry != nil {
			desc = entry.Description
			if len(entry.Auth.Modes) > 0 {
				auth = strings.Join(entry.Auth.Modes, ",")
			}
		}

		localCol := "not configured"
		if localEmpty {
			localCol = "not fetched"
		}
		if state != nil && state.Configured[id] != nil {
			modes := []string{}
			for k, on := range state.Configured[id] {
				if on {
					modes = append(modes, k.String())
				}
			}
			if len(modes) > 0 {
				sort.Strings(modes)
				localCol = "configured (" + strings.Join(modes, "+") + ")"
			}
		}

		remoteCol := "v" + version
		if re == nil {
			remoteCol = "—"
		}

		deltaCol := "up-to-date"
		switch {
		case added[id]:
			deltaCol = "available"
		case removed[id]:
			deltaCol = "local-only"
		case changed[id]:
			deltaCol = "changed"
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", id, localCol, remoteCol, deltaCol, auth, desc)
	}
	return tw.Flush()
}
