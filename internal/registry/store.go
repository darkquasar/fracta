package registry

import (
	"context"
	"time"

	"github.com/darkquasar/fracta/internal/config"
)

// Store is the interface for persisting MCP server registrations, discovered tools,
// and audit log entries. Separate from state.Store — different domain, different interface.
type Store interface {
	// Servers
	ListServers(ctx context.Context, filter ServerFilter) ([]Server, error)
	GetServer(ctx context.Context, name string) (*Server, error)
	UpsertServer(ctx context.Context, s Server) error
	DeleteServer(ctx context.Context, name string) error
	UpdateServerHealth(ctx context.Context, name string, status ServerStatus, healthMessage string, discoveredAt *time.Time) error

	// Tools
	ReplaceDiscoveredTools(ctx context.Context, server string, tools []Tool) error
	ListTools(ctx context.Context, filter ToolFilter) ([]Tool, error)
	SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error

	// Audit
	LogAudit(ctx context.Context, entry AuditEntry) error
	ListAuditLog(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)

	// SyncConfigServers performs a per-server config-aware upsert at startup.
	// Only touches rows with created_by='config'. API-managed servers are never modified.
	// Step 1: Upsert present servers (insert new, update changed, re-enable disabled).
	// Step 2: Disable absent config-owned servers (set proxy_enabled=false, preserve row).
	SyncConfigServers(ctx context.Context, servers config.MCPServersConfig) error

	Close() error
}
