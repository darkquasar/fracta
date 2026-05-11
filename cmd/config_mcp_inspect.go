package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
)

var configMcpInspectCmd = &cobra.Command{
	Use:   "inspect <server>",
	Short: "Show full per-server metadata from the local catalog.",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigMcpInspect,
}

func init() {
	configMcpCmd.AddCommand(configMcpInspectCmd)
}

func runConfigMcpInspect(cmd *cobra.Command, args []string) error {
	serverID := args[0]

	cat, err := mcpcatalog.LoadProjectCatalog(projectRoot)
	if err != nil {
		if errors.Is(err, mcpcatalog.ErrNoCatalog) {
			return errNoCatalogRemediation
		}
		return err
	}

	entry, ok := cat.Get(serverID)
	if !ok {
		return fmt.Errorf("server %q not found in catalog at <root>/mcp-servers/", serverID)
	}

	state, err := mcpcatalog.LoadProjectState(projectRoot)
	if err != nil {
		return fmt.Errorf("read project state: %w", err)
	}

	inspector := mcpcatalog.DetectImageInspector()

	return renderInspect(cmd.OutOrStdout(), entry, state, inspector)
}

func renderInspect(w io.Writer, e *mcpcatalog.Entry, state *mcpcatalog.ProjectState, inspector mcpcatalog.ImageInspector) error {
	displayName := e.Name
	if displayName == "" {
		displayName = e.ID
	}
	fmt.Fprintf(w, "Server: %s (%s)\n", e.ID, displayName)
	fmt.Fprintf(w, "Category: %s    Status: %s    Upstream: %s",
		valOrDash(e.Category), valOrDash(e.Status), valOrDash(e.Upstream.Type))
	if e.Upstream.URL != "" {
		fmt.Fprintf(w, " (%s)", e.Upstream.URL)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)

	if len(e.Auth.Modes) > 0 {
		fmt.Fprintf(w, "Auth modes: %s\n", strings.Join(e.Auth.Modes, ", "))
	} else {
		fmt.Fprintln(w, "Auth modes: -")
	}
	if len(e.Auth.EnvRequired) > 0 {
		fmt.Fprintf(w, "Required env: %s\n", strings.Join(e.Auth.EnvRequired, ", "))
	}
	if e.Auth.Notes != "" {
		fmt.Fprintf(w, "Auth notes: %s\n", e.Auth.Notes)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Variants:")
	variantNames := make([]string, 0, len(e.Variants))
	for n := range e.Variants {
		variantNames = append(variantNames, n)
	}
	sort.Strings(variantNames)
	for _, n := range variantNames {
		v := e.Variants[n]
		fmt.Fprintf(w, "  %-14s %s\n", n, formatVariant(v))
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Per-mode support:")
	fmt.Fprintf(w, "  local-process    %s\n", valOrDash(e.Support.LocalProcess))
	fmt.Fprintf(w, "  docker-compose   %s\n", valOrDash(e.Support.DockerCompose))
	fmt.Fprintf(w, "  kubernetes       %s\n", valOrDash(e.Support.Kubernetes))
	fmt.Fprintln(w)

	if e.RequiresImageBuild() {
		fmt.Fprintf(w, "Container build: required (fracta-owned image, Dockerfile=%s)\n", e.Docker.Dockerfile)
	} else if e.ImageRef() != "" {
		fmt.Fprintln(w, "Container build: not required (public image, owner=external)")
	}

	if imgRef := e.ImageRef(); imgRef != "" && inspector != nil {
		st, _ := inspector.HasImage(context.Background(), imgRef)
		fmt.Fprintf(w, "Image state (local %s daemon): %s\n", inspector.Name(), st.String())
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Configured in this project:")
	configured := configuredModes(state, e.ID)
	fmt.Fprintf(w, "  local           %s\n", configuredCellInspect(configured, scaffolds.KindLocal, e.ID, "fracta.yaml mcp_servers.servers.%s.local"))
	fmt.Fprintf(w, "  docker-compose  %s\n", configuredCellInspect(configured, scaffolds.KindDockerCompose, e.ID, "fracta/docker-compose.yml service:%s-mcp"))
	fmt.Fprintf(w, "  kubernetes      %s\n", configuredCellInspect(configured, scaffolds.KindK8s, e.ID, "fracta/k8s/manifests/%s-mcp.yaml"))

	return nil
}

func configuredModes(state *mcpcatalog.ProjectState, id string) map[scaffolds.Kind]bool {
	if state == nil || state.Configured == nil {
		return nil
	}
	return state.Configured[id]
}

func configuredCellInspect(modes map[scaffolds.Kind]bool, k scaffolds.Kind, id, locFmt string) string {
	if modes != nil && modes[k] {
		return "yes (" + fmt.Sprintf(locFmt, id) + ")"
	}
	return "no"
}

func formatVariant(v mcpcatalog.VariantSpec) string {
	var parts []string
	if v.Transport != "" {
		parts = append(parts, "transport="+v.Transport)
	}
	if v.Image != "" {
		owner := v.ImageOwner
		if owner == "" {
			owner = "external"
		}
		parts = append(parts, fmt.Sprintf("image=%s (image_owner=%s)", v.Image, owner))
	}
	if v.Command != "" {
		full := v.Command
		if len(v.Args) > 0 {
			full = full + " " + strings.Join(v.Args, " ")
		}
		parts = append(parts, "command="+full)
	}
	if v.URL != "" {
		parts = append(parts, "url="+v.URL)
	}
	if v.ServiceURL != "" {
		parts = append(parts, "service_url="+v.ServiceURL)
	}
	if v.Auth != "" {
		parts = append(parts, "auth="+v.Auth)
	}
	if v.FractaNative != "" {
		parts = append(parts, "fracta_native="+v.FractaNative)
	}
	return strings.Join(parts, "    ")
}

func valOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
