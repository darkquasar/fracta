package sqliteregistry

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry"
)

func newTestStore(t *testing.T) *SQLiteRegistry {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestServerCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert
	srv := registry.Server{
		Name:             "test-server",
		TransportType:    "stdio",
		ConnectionConfig: json.RawMessage(`{"command":"echo"}`),
		ProxyEnabled:     true,
		Status:           registry.StatusPending,
		CreatedBy:        "test",
	}
	if err := s.UpsertServer(ctx, srv); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Get
	got, err := s.GetServer(ctx, "test-server")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected server, got nil")
	}
	if got.Name != "test-server" {
		t.Errorf("name = %q, want %q", got.Name, "test-server")
	}
	if got.TransportType != "stdio" {
		t.Errorf("transport = %q, want %q", got.TransportType, "stdio")
	}
	if !got.ProxyEnabled {
		t.Error("proxy_enabled should be true")
	}
	if got.Status != registry.StatusPending {
		t.Errorf("status = %q, want %q", got.Status, registry.StatusPending)
	}

	// Get non-existent
	missing, err := s.GetServer(ctx, "no-such-server")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Error("expected nil for missing server")
	}

	// List
	servers, err := s.ListServers(ctx, registry.ServerFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("list count = %d, want 1", len(servers))
	}

	// Update (upsert existing)
	srv.HealthMessage = "all good"
	srv.Status = registry.StatusActive
	if err := s.UpsertServer(ctx, srv); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ = s.GetServer(ctx, "test-server")
	if got.Status != registry.StatusActive {
		t.Errorf("updated status = %q, want %q", got.Status, registry.StatusActive)
	}

	// Delete
	if err := s.DeleteServer(ctx, "test-server"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = s.GetServer(ctx, "test-server")
	if got != nil {
		t.Error("expected nil after delete")
	}

	// Delete non-existent
	if err := s.DeleteServer(ctx, "test-server"); err == nil {
		t.Error("expected error deleting non-existent server")
	}
}

func TestListServersFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert two servers with different statuses and proxy settings
	s.UpsertServer(ctx, registry.Server{Name: "a", ProxyEnabled: true, Status: registry.StatusActive, CreatedBy: "test"})
	s.UpsertServer(ctx, registry.Server{Name: "b", ProxyEnabled: false, Status: registry.StatusPaused, CreatedBy: "test"})

	// Filter by status
	active := registry.StatusActive
	servers, _ := s.ListServers(ctx, registry.ServerFilter{Status: &active})
	if len(servers) != 1 || servers[0].Name != "a" {
		t.Errorf("status filter: got %d servers, want 1 (a)", len(servers))
	}

	// Filter by proxy_enabled
	enabled := true
	servers, _ = s.ListServers(ctx, registry.ServerFilter{ProxyEnabled: &enabled})
	if len(servers) != 1 || servers[0].Name != "a" {
		t.Errorf("proxy filter: got %d servers, want 1 (a)", len(servers))
	}

	disabled := false
	servers, _ = s.ListServers(ctx, registry.ServerFilter{ProxyEnabled: &disabled})
	if len(servers) != 1 || servers[0].Name != "b" {
		t.Errorf("proxy disabled filter: got %d servers, want 1 (b)", len(servers))
	}
}

func TestHealthUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertServer(ctx, registry.Server{Name: "srv", ProxyEnabled: true, CreatedBy: "test"})

	// Update without discoveredAt
	if err := s.UpdateServerHealth(ctx, "srv", registry.StatusActive, "healthy", nil); err != nil {
		t.Fatalf("update health: %v", err)
	}
	got, _ := s.GetServer(ctx, "srv")
	if got.Status != registry.StatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	if got.HealthMessage != "healthy" {
		t.Errorf("health_message = %q, want healthy", got.HealthMessage)
	}

	// Update with discoveredAt
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateServerHealth(ctx, "srv", registry.StatusDegraded, "drift", &now); err != nil {
		t.Fatalf("update health with discovered: %v", err)
	}
	got, _ = s.GetServer(ctx, "srv")
	if got.Status != registry.StatusDegraded {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if got.LastDiscoveredAt == nil {
		t.Fatal("expected last_discovered_at to be set")
	}
}

func TestToolsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertServer(ctx, registry.Server{Name: "srv", ProxyEnabled: true, CreatedBy: "test"})

	// Replace tools
	tools := []registry.Tool{
		{ServerName: "srv", ToolName: "tool-a", Description: "first", SchemaHash: "abc", Enabled: true},
		{ServerName: "srv", ToolName: "tool-b", Description: "second", SchemaHash: "def", Enabled: true},
	}
	if err := s.ReplaceDiscoveredTools(ctx, "srv", tools); err != nil {
		t.Fatalf("replace tools: %v", err)
	}

	// List all tools
	all, err := s.ListTools(ctx, registry.ToolFilter{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("tool count = %d, want 2", len(all))
	}

	// List by server
	byServer, _ := s.ListTools(ctx, registry.ToolFilter{ServerName: "srv"})
	if len(byServer) != 2 {
		t.Errorf("tools by server = %d, want 2", len(byServer))
	}

	// Disable a tool
	if err := s.SetToolEnabled(ctx, "srv", "tool-a", false); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	enabled := true
	enabledTools, _ := s.ListTools(ctx, registry.ToolFilter{Enabled: &enabled})
	if len(enabledTools) != 1 {
		t.Errorf("enabled tools = %d, want 1", len(enabledTools))
	}

	// Re-enable
	if err := s.SetToolEnabled(ctx, "srv", "tool-a", true); err != nil {
		t.Fatalf("enable tool: %v", err)
	}

	// SetToolEnabled on non-existent tool
	if err := s.SetToolEnabled(ctx, "srv", "no-such", true); err == nil {
		t.Error("expected error for non-existent tool")
	}

	// Replace with new set (should remove old tools)
	newTools := []registry.Tool{
		{ServerName: "srv", ToolName: "tool-c", Description: "third", SchemaHash: "ghi", Enabled: true},
	}
	if err := s.ReplaceDiscoveredTools(ctx, "srv", newTools); err != nil {
		t.Fatalf("replace tools again: %v", err)
	}
	all, _ = s.ListTools(ctx, registry.ToolFilter{ServerName: "srv"})
	if len(all) != 1 || all[0].ToolName != "tool-c" {
		t.Errorf("after replace: got %d tools, want 1 (tool-c)", len(all))
	}
}

func TestToolsCascadeDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.UpsertServer(ctx, registry.Server{Name: "srv", ProxyEnabled: true, CreatedBy: "test"})
	s.ReplaceDiscoveredTools(ctx, "srv", []registry.Tool{
		{ServerName: "srv", ToolName: "tool-a", SchemaHash: "abc", Enabled: true},
	})

	// Deleting server should cascade-delete tools
	s.DeleteServer(ctx, "srv")
	tools, _ := s.ListTools(ctx, registry.ToolFilter{ServerName: "srv"})
	if len(tools) != 0 {
		t.Errorf("expected 0 tools after cascade delete, got %d", len(tools))
	}
}

func TestAuditLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entry := registry.AuditEntry{
		Actor:        "admin",
		ActorType:    "user",
		Action:       "register_server",
		ResourceType: "server",
		ResourceName: "test-srv",
		Detail:       json.RawMessage(`{"transport":"stdio"}`),
	}
	if err := s.LogAudit(ctx, entry); err != nil {
		t.Fatalf("log audit: %v", err)
	}

	// List all
	entries, err := s.ListAuditLog(ctx, registry.AuditFilter{})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit count = %d, want 1", len(entries))
	}
	if entries[0].Actor != "admin" {
		t.Errorf("actor = %q, want admin", entries[0].Actor)
	}
	if entries[0].Action != "register_server" {
		t.Errorf("action = %q, want register_server", entries[0].Action)
	}

	// Filter by action
	filtered, _ := s.ListAuditLog(ctx, registry.AuditFilter{Action: "register_server"})
	if len(filtered) != 1 {
		t.Errorf("filtered count = %d, want 1", len(filtered))
	}

	// Filter with no match
	empty, _ := s.ListAuditLog(ctx, registry.AuditFilter{Action: "no_such_action"})
	if len(empty) != 0 {
		t.Errorf("expected 0 entries, got %d", len(empty))
	}
}

func TestSyncConfigServers_NewServer(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Pre-insert an existing API-managed server to prove new config servers
	// are inserted even when other rows exist.
	s.UpsertServer(ctx, registry.Server{Name: "api-srv", CreatedBy: "api", ProxyEnabled: true})

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
			"vendor":  {Local: config.MCPServerLocal{Command: "vendor-mcp"}},
		},
	}

	if err := s.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("sync: %v", err)
	}
	all, _ := s.ListServers(ctx, registry.ServerFilter{})
	if len(all) != 3 {
		t.Fatalf("after sync: %d servers, want 3 (2 config + 1 api)", len(all))
	}
	for _, srv := range all {
		if srv.Name == "elastic" || srv.Name == "vendor" {
			if srv.CreatedBy != "config" {
				t.Errorf("server %q created_by = %q, want config", srv.Name, srv.CreatedBy)
			}
			if !srv.ProxyEnabled {
				t.Errorf("server %q should be proxy_enabled", srv.Name)
			}
			if srv.Status != registry.StatusPending {
				t.Errorf("server %q status = %q, want pending", srv.Name, srv.Status)
			}
		}
	}
}

func TestSyncConfigServers_ChangedConfig(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Seed with initial config.
	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp", Args: []string{"--port=9200"}}},
		},
	}
	if err := s.SyncConfigServers(ctx, initial); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	srv1, _ := s.GetServer(ctx, "elastic")

	// Backdated updated_at to ensure the next sync produces a different timestamp.
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	s.db.ExecContext(ctx, `UPDATE registered_servers SET updated_at = ? WHERE name = 'elastic'`, past)
	srv1, _ = s.GetServer(ctx, "elastic")

	// Change the config.
	changed := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp", Args: []string{"--port=9300"}}},
		},
	}
	if err := s.SyncConfigServers(ctx, changed); err != nil {
		t.Fatalf("changed sync: %v", err)
	}
	srv2, _ := s.GetServer(ctx, "elastic")

	// connection_config should be updated.
	if string(srv1.ConnectionConfig) == string(srv2.ConnectionConfig) {
		t.Error("connection_config should have changed")
	}
	// updated_at should have advanced.
	if !srv2.UpdatedAt.After(srv1.UpdatedAt) {
		t.Errorf("updated_at should have advanced after config change: %v -> %v", srv1.UpdatedAt, srv2.UpdatedAt)
	}
}

func TestSyncConfigServers_UnchangedConfig(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}

	if err := s.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	srv1, _ := s.GetServer(ctx, "elastic")

	time.Sleep(10 * time.Millisecond)

	// Re-sync with same config.
	if err := s.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	srv2, _ := s.GetServer(ctx, "elastic")

	// updated_at should NOT change when config is identical.
	if srv2.UpdatedAt != srv1.UpdatedAt {
		t.Errorf("updated_at changed without config change: %v -> %v", srv1.UpdatedAt, srv2.UpdatedAt)
	}
}

func TestSyncConfigServers_APIServerNotOverwritten(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Pre-insert an API-managed server with the same name as a config server.
	s.UpsertServer(ctx, registry.Server{
		Name:             "shared-name",
		CreatedBy:        "api",
		ProxyEnabled:     true,
		ConnectionConfig: json.RawMessage(`{"command":"api-managed"}`),
	})

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"shared-name": {Local: config.MCPServerLocal{Command: "config-managed"}},
		},
	}
	if err := s.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("sync: %v", err)
	}

	got, _ := s.GetServer(ctx, "shared-name")
	if got.CreatedBy != "api" {
		t.Errorf("created_by = %q, want api (should not be overwritten)", got.CreatedBy)
	}
	// Connection config should remain the API-managed one.
	if string(got.ConnectionConfig) != `{"command":"api-managed"}` {
		t.Errorf("connection_config = %s, want api-managed config preserved", got.ConnectionConfig)
	}
}

func TestSyncConfigServers_RemovedServerDisabled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
			"vendor":  {Local: config.MCPServerLocal{Command: "vendor-mcp"}},
		},
	}
	if err := s.SyncConfigServers(ctx, initial); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// Remove "vendor" from config.
	reduced := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}
	if err := s.SyncConfigServers(ctx, reduced); err != nil {
		t.Fatalf("reduced sync: %v", err)
	}

	// vendor should still exist but be disabled.
	vendor, _ := s.GetServer(ctx, "vendor")
	if vendor == nil {
		t.Fatal("vendor should still exist (row preserved)")
	}
	if vendor.ProxyEnabled {
		t.Error("vendor should have proxy_enabled=false after removal")
	}

	// elastic should remain enabled.
	elastic, _ := s.GetServer(ctx, "elastic")
	if !elastic.ProxyEnabled {
		t.Error("elastic should remain proxy_enabled=true")
	}
}

func TestSyncConfigServers_ReAddedServerReEnabled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Seed, then remove, then re-add.
	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}
	s.SyncConfigServers(ctx, initial)

	// Remove.
	s.SyncConfigServers(ctx, config.MCPServersConfig{Servers: map[string]config.MCPServerEntry{}})
	elastic, _ := s.GetServer(ctx, "elastic")
	if elastic.ProxyEnabled {
		t.Fatal("elastic should be disabled after removal")
	}

	// Re-add.
	if err := s.SyncConfigServers(ctx, initial); err != nil {
		t.Fatalf("re-add sync: %v", err)
	}
	elastic, _ = s.GetServer(ctx, "elastic")
	if !elastic.ProxyEnabled {
		t.Error("elastic should be re-enabled after re-adding to config")
	}
}

func TestSyncConfigServers_EmptyConfigDisablesAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"a": {Local: config.MCPServerLocal{Command: "a-mcp"}},
			"b": {Local: config.MCPServerLocal{Command: "b-mcp"}},
		},
	}
	s.SyncConfigServers(ctx, initial)

	// Also add an API-managed server.
	s.UpsertServer(ctx, registry.Server{Name: "api-srv", CreatedBy: "api", ProxyEnabled: true})

	// Sync with empty config.
	if err := s.SyncConfigServers(ctx, config.MCPServersConfig{}); err != nil {
		t.Fatalf("empty sync: %v", err)
	}

	// Config-owned servers disabled.
	a, _ := s.GetServer(ctx, "a")
	b, _ := s.GetServer(ctx, "b")
	if a.ProxyEnabled || b.ProxyEnabled {
		t.Error("all config-owned servers should be disabled with empty config")
	}

	// API-managed server untouched.
	api, _ := s.GetServer(ctx, "api-srv")
	if !api.ProxyEnabled {
		t.Error("API-managed server should not be affected by config sync")
	}
}

func TestSyncConfigServers_NilServersMapDisablesAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"a": {Local: config.MCPServerLocal{Command: "a-mcp"}},
		},
	}
	s.SyncConfigServers(ctx, initial)

	// Sync with nil servers map.
	if err := s.SyncConfigServers(ctx, config.MCPServersConfig{Servers: nil}); err != nil {
		t.Fatalf("nil sync: %v", err)
	}

	a, _ := s.GetServer(ctx, "a")
	if a.ProxyEnabled {
		t.Error("config-owned server should be disabled with nil servers map")
	}
}

func TestSyncConfigServers_KubernetesTransportType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"local-srv": {Local: config.MCPServerLocal{Command: "local-mcp"}},
			"k8s-srv":   {Kubernetes: config.MCPServerKubernetes{URL: "http://mcp:8080", Transport: "streamable_http"}},
			"k8s-sse":   {Kubernetes: config.MCPServerKubernetes{URL: "http://mcp:8081", Transport: "sse"}},
		},
	}

	if err := s.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("sync: %v", err)
	}

	local, _ := s.GetServer(ctx, "local-srv")
	if local.TransportType != "stdio" {
		t.Errorf("local transport = %q, want stdio", local.TransportType)
	}

	k8s, _ := s.GetServer(ctx, "k8s-srv")
	if k8s.TransportType != "streamable_http" {
		t.Errorf("k8s transport = %q, want streamable_http", k8s.TransportType)
	}

	sse, _ := s.GetServer(ctx, "k8s-sse")
	if sse.TransportType != "http" {
		t.Errorf("k8s-sse transport = %q, want http", sse.TransportType)
	}
}

func TestSyncConfigServers_TransportTypeUpdatedOnChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Start with a local server (stdio).
	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: "local-mcp"}},
		},
	}
	if err := s.SyncConfigServers(ctx, initial); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	got, _ := s.GetServer(ctx, "srv")
	if got.TransportType != "stdio" {
		t.Fatalf("initial transport = %q, want stdio", got.TransportType)
	}

	// Change to K8s (streamable_http).
	changed := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Kubernetes: config.MCPServerKubernetes{URL: "http://mcp:8080"}},
		},
	}
	if err := s.SyncConfigServers(ctx, changed); err != nil {
		t.Fatalf("changed sync: %v", err)
	}
	got, _ = s.GetServer(ctx, "srv")
	if got.TransportType != "streamable_http" {
		t.Errorf("changed transport = %q, want streamable_http", got.TransportType)
	}
}

func TestSyncConfigServers_NoSyncWithoutCall(t *testing.T) {
	// When SyncConfigServers is NOT called (bootstrap_from_config: false),
	// the DB stays empty.
	s := newTestStore(t)
	ctx := context.Background()

	all, err := s.ListServers(ctx, registry.ServerFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 servers without sync, got %d", len(all))
	}
}

func TestAllServerStatuses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	statuses := []registry.ServerStatus{
		registry.StatusPending,
		registry.StatusActive,
		registry.StatusDegraded,
		registry.StatusPaused,
		registry.StatusError,
		registry.StatusAuthFailed,
		registry.StatusUnreachable,
	}

	for _, status := range statuses {
		name := "srv-" + string(status)
		s.UpsertServer(ctx, registry.Server{Name: name, ProxyEnabled: true, Status: status, CreatedBy: "test"})
		got, err := s.GetServer(ctx, name)
		if err != nil {
			t.Fatalf("get %q: %v", name, err)
		}
		if got.Status != status {
			t.Errorf("server %q status = %q, want %q", name, got.Status, status)
		}
	}

	all, _ := s.ListServers(ctx, registry.ServerFilter{})
	if len(all) != len(statuses) {
		t.Errorf("total servers = %d, want %d", len(all), len(statuses))
	}
}
