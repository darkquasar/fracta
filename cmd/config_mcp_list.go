package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	configMcpCmd.AddCommand(configMcpListCmd)
}

// listRow is one row in the local-list table/JSON output.
type listRow struct {
	ID         string                 `json:"id" yaml:"id"`
	Name       string                 `json:"name" yaml:"name"`
	Status     string                 `json:"status" yaml:"status"`
	Category   string                 `json:"category" yaml:"category"`
	AuthModes  []string               `json:"auth_modes,omitempty" yaml:"auth_modes,omitempty"`
	Transport  string                 `json:"transport,omitempty" yaml:"transport,omitempty"`
	Image      string                 `json:"image,omitempty" yaml:"image,omitempty"`
	ImageState string                 `json:"image_state,omitempty" yaml:"image_state,omitempty"`
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

// runConfigMcpListRemote handles `list --remote`. Implemented as a stub here
// because I1 (Fetch) and I2 (Diff) are pending. Once those land, this function
// is replaced with the canonical fetch-to-temp + Diff render.
func runConfigMcpListRemote(cmd *cobra.Command) error {
	return errors.New("'fracta config mcp list --remote' is not yet implemented; pending fetch + diff machinery (spec-43 §5.4)")
}


