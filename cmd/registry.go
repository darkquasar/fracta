package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/darkquasar/fracta/internal/authz"
	"github.com/darkquasar/fracta/internal/controlplane"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/spf13/cobra"
)

var registryCmd = &cobra.Command{
	Use:        "registry",
	Short:      "Manage the MCP server registry (deprecated: use 'fracta debug registry')",
	Hidden:     true,
	Deprecated: "use 'fracta debug registry' instead",
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

var registryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered MCP servers",
	RunE:  runRegistryList,
}

var registryAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Register a new MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runRegistryAdd,
}

var registryRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a registered MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runRegistryRemove,
}

var registryStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show registry status or details for a specific server",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRegistryStatus,
}

var (
	addTransport string
	addCommand   string
	addArgs      []string
)

func init() {
	rootCmd.AddCommand(registryCmd)
	registryCmd.AddCommand(registryListCmd)
	registryCmd.AddCommand(registryAddCmd)
	registryCmd.AddCommand(registryRemoveCmd)
	registryCmd.AddCommand(registryStatusCmd)

	registryAddCmd.Flags().StringVar(&addTransport, "transport", "stdio", "transport type (stdio, http, streamable_http)")
	registryAddCmd.Flags().StringVar(&addCommand, "command", "", "command to launch the server")
	registryAddCmd.Flags().StringSliceVar(&addArgs, "args", nil, "arguments for the command")
}

// buildRegistryService creates a RegistryService for CLI commands.
// CLI operations assume admin role (spec Section 5.3).
func buildRegistryService(root string) (*registry.RegistryService, registry.Store, func(), error) {
	cfg, err := loadConfigOrDefault(root)
	if err != nil {
		return nil, nil, nil, err
	}
	cp, err := controlplane.NewControlPlane(cfg, root)
	if err != nil {
		return nil, nil, nil, err
	}

	svc := registry.NewRegistryService(cp.RegistryStore, &authz.DefaultAuthorizer{})
	cleanup := func() { cp.Close() }
	return svc, cp.RegistryStore, cleanup, nil
}

// adminCtx returns a context with an admin subject for CLI operations.
func adminCtx() context.Context {
	sub := authz.Subject{
		Type:  authz.SubjectAdmin,
		ID:    "cli",
		Roles: []string{"admin"},
	}
	return authz.WithSubject(context.Background(), sub)
}

func runRegistryList(cmd *cobra.Command, args []string) error {
	_, store, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := adminCtx()
	servers, err := store.ListServers(ctx, registry.ServerFilter{})
	if err != nil {
		return fmt.Errorf("listing servers: %w", err)
	}

	if len(servers) == 0 {
		fmt.Println("No registered servers.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRANSPORT\tSTATUS\tPROXY\tCREATED BY")
	for _, s := range servers {
		proxy := "on"
		if !s.ProxyEnabled {
			proxy = "off"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.TransportType, s.Status, proxy, s.CreatedBy)
	}
	return w.Flush()
}

func runRegistryAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	svc, _, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	connCfg := map[string]any{
		"local": map[string]any{
			"command": addCommand,
			"args":    addArgs,
		},
	}
	connJSON, err := json.Marshal(connCfg)
	if err != nil {
		return fmt.Errorf("marshaling connection config: %w", err)
	}

	srv := registry.Server{
		Name:             name,
		TransportType:    addTransport,
		ConnectionConfig: connJSON,
		ProxyEnabled:     true,
		CreatedBy:        "cli",
	}

	ctx := adminCtx()
	if err := svc.RegisterServer(ctx, srv); err != nil {
		return fmt.Errorf("registering server: %w", err)
	}

	fmt.Printf("Registered server %q (transport=%s)\n", name, addTransport)
	return nil
}

func runRegistryRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	svc, _, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := adminCtx()
	if err := svc.DeleteServer(ctx, name); err != nil {
		return fmt.Errorf("removing server: %w", err)
	}

	fmt.Printf("Removed server %q\n", name)
	return nil
}

func runRegistryStatus(cmd *cobra.Command, args []string) error {
	_, store, cleanup, err := buildRegistryService(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := adminCtx()

	if len(args) == 1 {
		return showServerDetail(ctx, store, args[0])
	}
	return showRegistrySummary(ctx, store)
}

func showServerDetail(ctx context.Context, store registry.Store, name string) error {
	srv, err := store.GetServer(ctx, name)
	if err != nil {
		return fmt.Errorf("getting server: %w", err)
	}
	if srv == nil {
		return fmt.Errorf("server %q not found", name)
	}

	fmt.Printf("Name:            %s\n", srv.Name)
	fmt.Printf("Transport:       %s\n", srv.TransportType)
	fmt.Printf("Status:          %s\n", srv.Status)
	fmt.Printf("Proxy enabled:   %v\n", srv.ProxyEnabled)
	fmt.Printf("Health message:  %s\n", srv.HealthMessage)
	fmt.Printf("Created by:      %s\n", srv.CreatedBy)
	fmt.Printf("Created at:      %s\n", srv.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("Updated at:      %s\n", srv.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
	if srv.LastDiscoveredAt != nil {
		fmt.Printf("Last discovered: %s\n", srv.LastDiscoveredAt.Format("2006-01-02 15:04:05 MST"))
	}

	tools, err := store.ListTools(ctx, registry.ToolFilter{ServerName: name})
	if err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}
	if len(tools) > 0 {
		fmt.Printf("\nTools (%d):\n", len(tools))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  TOOL\tENABLED\tDESCRIPTION")
		for _, t := range tools {
			enabled := "yes"
			if !t.Enabled {
				enabled = "no"
			}
			desc := t.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", t.ToolName, enabled, desc)
		}
		w.Flush()
	}

	return nil
}

func showRegistrySummary(ctx context.Context, store registry.Store) error {
	servers, err := store.ListServers(ctx, registry.ServerFilter{})
	if err != nil {
		return fmt.Errorf("listing servers: %w", err)
	}

	tools, err := store.ListTools(ctx, registry.ToolFilter{})
	if err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	statusCounts := make(map[registry.ServerStatus]int)
	for _, s := range servers {
		statusCounts[s.Status]++
	}

	enabledTools := 0
	for _, t := range tools {
		if t.Enabled {
			enabledTools++
		}
	}

	fmt.Printf("Registry Summary\n")
	fmt.Printf("  Servers: %d total\n", len(servers))
	for status, count := range statusCounts {
		fmt.Printf("    %s: %d\n", status, count)
	}
	fmt.Printf("  Tools: %d total (%d enabled)\n", len(tools), enabledTools)

	return nil
} 
