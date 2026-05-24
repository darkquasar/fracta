package controlplane

import (
	"path/filepath"

	"github.com/darkquasar/fracta/internal/config"
)

// Profile bundles the default settings for a deployment mode.
type Profile struct {
	Name                   string
	StateDriver            string                  // "sqlite" or "postgres"
	StatePath              string                  // SQLite DB path (resolved to absolute)
	PostgresConfig         config.PostgresConfig   // Postgres connection settings (when driver=postgres)
	WorkspaceType          string                  // "git" or "directory"
	WorkspaceBase          string                  // base directory for workspaces (only used by directory workspace)
	BackendType            string                  // "local" or "kubernetes"
	ProjectRoot            string                  // project root for git workspace
	ClientMode             string                  // "local" (in-process client) or "remote" (HTTP client to in-cluster API)
	RegistryDriver         string                  // "sqlite" or "postgres" — defaults to StateDriver if empty
	RegistryPostgresConfig config.PostgresConfig   // Postgres config for registry; explicit override or inherited from state
	RegistryBootstrap      bool                    // gateway-only: seed registry from mcp_servers config on startup
	MCPServers             config.MCPServersConfig // MCP server config for gateway bootstrap seeding
}

var profiles = map[string]Profile{
	"local": {
		Name:          "local",
		StatePath:     ".fracta/state.db",
		WorkspaceType: "git",
		ProjectRoot:   ".",
		BackendType:   "local",
	},
	"kubernetes": {
		Name:          "kubernetes",
		StatePath:     "/workspace/.fracta/state.db",
		WorkspaceType: "directory",
		WorkspaceBase: "/workspace/agents",
		BackendType:   "kubernetes",
	},
}

// ResolveProfile returns the effective Profile from config, applying overrides.
// root is the project root directory — relative paths in the profile are resolved against it.
// clientModeOverride, if non-empty, takes highest precedence for ClientMode
// (from CLI --client-mode flag). Precedence: CLI > config client_mode > auto-detection.
func ResolveProfile(cfg *config.Config, root string, clientModeOverride ...string) Profile {
	name := cfg.ResolvedProfile()
	p, ok := profiles[name]
	if !ok {
		p = profiles["local"]
	}
	// Apply state driver and path from config.
	p.StateDriver = cfg.Runtime.State.EffectiveDriver()
	p.StatePath = cfg.Runtime.State.EffectiveSQLitePath()
	if p.StateDriver == "postgres" {
		p.PostgresConfig = cfg.Runtime.State.Postgres
	}
	if cfg.Runtime.Backend != "" {
		p.BackendType = cfg.Runtime.Backend
	}
	// For K8s: workspace base derives from pvc_mount_path (pod-side).
	if p.WorkspaceType == "directory" && cfg.Runtime.Kubernetes.PVCMountPath != "" {
		p.WorkspaceBase = cfg.Runtime.Kubernetes.PVCMountPath + "/agents"
	}
	// Client mode: CLI override > config client_mode > auto-detection.
	explicitMode := ""
	if len(clientModeOverride) > 0 && clientModeOverride[0] != "" {
		explicitMode = clientModeOverride[0]
	}
	if explicitMode == "" && cfg.ControlPlaneAPI.ClientMode != "" && cfg.ControlPlaneAPI.ClientMode != "auto" {
		explicitMode = cfg.ControlPlaneAPI.ClientMode
	}
	switch explicitMode {
	case "local":
		p.ClientMode = "local"
	case "remote":
		p.ClientMode = "remote"
	default: // "auto" or ""
		switch {
		case cfg.ControlPlaneAPI.URL != "":
			p.ClientMode = "remote"
		case p.BackendType == "kubernetes":
			p.ClientMode = "remote"
		default:
			p.ClientMode = "local"
		}
	}

	// Registry driver: explicit override, or inherit from state driver.
	p.RegistryDriver = cfg.Registry.Driver
	if p.RegistryDriver == "" {
		p.RegistryDriver = p.StateDriver
	}
	// Registry Postgres config: explicit registry.postgres wins, else fall back
	// to runtime.state.postgres. This ensures registry.driver: postgres works
	// even when the state driver is SQLite.
	if cfg.Registry.Postgres.DSN != "" {
		p.RegistryPostgresConfig = cfg.Registry.Postgres
	} else {
		p.RegistryPostgresConfig = cfg.Runtime.State.Postgres
	}
	// Bootstrap defaults to true. If the registry block is explicitly present
	// in config (any field non-zero), use the explicit BootstrapFromConfig value.
	p.RegistryBootstrap = true
	if cfg.Registry != (config.RegistryConfig{}) {
		p.RegistryBootstrap = cfg.Registry.BootstrapFromConfig
	}
	p.MCPServers = cfg.MCPServers
	// Resolve relative paths against project root.
	if root != "" {
		if !filepath.IsAbs(p.StatePath) {
			p.StatePath = filepath.Join(root, p.StatePath)
		}
		if !filepath.IsAbs(p.ProjectRoot) {
			p.ProjectRoot = filepath.Join(root, p.ProjectRoot)
		}
		if p.WorkspaceBase != "" && !filepath.IsAbs(p.WorkspaceBase) {
			p.WorkspaceBase = filepath.Join(root, p.WorkspaceBase)
		}
	}
	return p
}
