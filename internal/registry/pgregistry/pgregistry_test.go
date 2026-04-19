//go:build postgres

package pgregistry_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/registry/pgregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupStore(t *testing.T) *pgregistry.PgRegistry {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("fracta_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	store, err := pgregistry.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	return store
}

func TestNew_PingAndMigrate(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	servers, err := store.ListServers(ctx, registry.ServerFilter{})
	require.NoError(t, err)
	assert.Empty(t, servers)
}

func TestUpsertAndGetServer(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	srv := registry.Server{
		Name:               "test-server",
		TransportType:      "stdio",
		ConnectionConfig:   json.RawMessage(`{"command":"echo"}`),
		SecretRefs:         json.RawMessage(`{}`),
		ServerCapabilities: json.RawMessage(`{"tools":true}`),
		ProxyEnabled:       true,
		Status:             registry.StatusPending,
		HealthMessage:      "",
		CreatedBy:          "test",
	}

	require.NoError(t, store.UpsertServer(ctx, srv))

	got, err := store.GetServer(ctx, "test-server")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "test-server", got.Name)
	assert.Equal(t, "stdio", got.TransportType)
	assert.JSONEq(t, `{"command":"echo"}`, string(got.ConnectionConfig))
	assert.True(t, got.ProxyEnabled)
	assert.Equal(t, registry.StatusPending, got.Status)
	assert.Equal(t, "test", got.CreatedBy)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestUpsertServer_Update(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	srv := registry.Server{Name: "srv1", ProxyEnabled: true, CreatedBy: "test"}
	require.NoError(t, store.UpsertServer(ctx, srv))

	srv.HealthMessage = "updated"
	srv.Status = registry.StatusActive
	require.NoError(t, store.UpsertServer(ctx, srv))

	got, err := store.GetServer(ctx, "srv1")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusActive, got.Status)
	assert.Equal(t, "updated", got.HealthMessage)
}

func TestGetServer_NotFound(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	got, err := store.GetServer(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDeleteServer(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "del-me", CreatedBy: "test"}))
	require.NoError(t, store.DeleteServer(ctx, "del-me"))

	got, err := store.GetServer(ctx, "del-me")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDeleteServer_NotFound(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.DeleteServer(ctx, "nonexistent")
	assert.Error(t, err)
}

func TestDeleteServer_CascadesTools(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "cascade-srv", CreatedBy: "test"}))
	require.NoError(t, store.ReplaceDiscoveredTools(ctx, "cascade-srv", []registry.Tool{
		{ServerName: "cascade-srv", ToolName: "tool1", Enabled: true},
	}))

	require.NoError(t, store.DeleteServer(ctx, "cascade-srv"))

	tools, err := store.ListTools(ctx, registry.ToolFilter{ServerName: "cascade-srv"})
	require.NoError(t, err)
	assert.Empty(t, tools)
}

func TestListServers_Filter(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "a", ProxyEnabled: true, Status: registry.StatusActive, CreatedBy: "test"}))
	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "b", ProxyEnabled: false, Status: registry.StatusPaused, CreatedBy: "test"}))

	// Filter by proxy_enabled
	enabled := true
	servers, err := store.ListServers(ctx, registry.ServerFilter{ProxyEnabled: &enabled})
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "a", servers[0].Name)

	// Filter by status
	status := registry.StatusPaused
	servers, err = store.ListServers(ctx, registry.ServerFilter{Status: &status})
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "b", servers[0].Name)

	// No filter
	servers, err = store.ListServers(ctx, registry.ServerFilter{})
	require.NoError(t, err)
	assert.Len(t, servers, 2)
}

func TestUpdateServerHealth(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "health-srv", CreatedBy: "test"}))

	disc := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, store.UpdateServerHealth(ctx, "health-srv", registry.StatusActive, "all good", &disc))

	got, err := store.GetServer(ctx, "health-srv")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusActive, got.Status)
	assert.Equal(t, "all good", got.HealthMessage)
	require.NotNil(t, got.LastDiscoveredAt)
	assert.WithinDuration(t, disc, *got.LastDiscoveredAt, time.Microsecond)
}

func TestUpdateServerHealth_NoDiscoveredAt(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "h2", CreatedBy: "test"}))
	require.NoError(t, store.UpdateServerHealth(ctx, "h2", registry.StatusError, "fail", nil))

	got, err := store.GetServer(ctx, "h2")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusError, got.Status)
	assert.Nil(t, got.LastDiscoveredAt)
}

func TestReplaceDiscoveredTools(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "tool-srv", CreatedBy: "test"}))

	tools := []registry.Tool{
		{ServerName: "tool-srv", ToolName: "read", Description: "Read files", SchemaHash: "abc", Enabled: true, InputSchema: json.RawMessage(`{"type":"object"}`)},
		{ServerName: "tool-srv", ToolName: "write", Description: "Write files", SchemaHash: "def", Enabled: true},
	}
	require.NoError(t, store.ReplaceDiscoveredTools(ctx, "tool-srv", tools))

	got, err := store.ListTools(ctx, registry.ToolFilter{ServerName: "tool-srv"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "read", got[0].ToolName)
	assert.Equal(t, "write", got[1].ToolName)
	assert.JSONEq(t, `{"type":"object"}`, string(got[0].InputSchema))

	// Replace with different set
	tools2 := []registry.Tool{
		{ServerName: "tool-srv", ToolName: "exec", Description: "Execute", SchemaHash: "ghi", Enabled: true},
	}
	require.NoError(t, store.ReplaceDiscoveredTools(ctx, "tool-srv", tools2))

	got, err = store.ListTools(ctx, registry.ToolFilter{ServerName: "tool-srv"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "exec", got[0].ToolName)
}

func TestListTools_Filter(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "filter-srv", CreatedBy: "test"}))
	require.NoError(t, store.ReplaceDiscoveredTools(ctx, "filter-srv", []registry.Tool{
		{ServerName: "filter-srv", ToolName: "on", Enabled: true},
		{ServerName: "filter-srv", ToolName: "off", Enabled: false},
	}))

	enabled := true
	got, err := store.ListTools(ctx, registry.ToolFilter{ServerName: "filter-srv", Enabled: &enabled})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "on", got[0].ToolName)
}

func TestSetToolEnabled(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "toggle-srv", CreatedBy: "test"}))
	require.NoError(t, store.ReplaceDiscoveredTools(ctx, "toggle-srv", []registry.Tool{
		{ServerName: "toggle-srv", ToolName: "mytool", Enabled: true},
	}))

	require.NoError(t, store.SetToolEnabled(ctx, "toggle-srv", "mytool", false))

	disabled := false
	got, err := store.ListTools(ctx, registry.ToolFilter{ServerName: "toggle-srv", Enabled: &disabled})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "mytool", got[0].ToolName)
}

func TestSetToolEnabled_NotFound(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	err := store.SetToolEnabled(ctx, "no-srv", "no-tool", true)
	assert.Error(t, err)
}

func TestLogAudit_And_ListAuditLog(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	entries := []registry.AuditEntry{
		{Actor: "admin", ActorType: "admin", Action: "RegisterServer", ResourceType: "server", ResourceName: "srv1", Detail: json.RawMessage(`{"reason":"new"}`)},
		{Actor: "admin", ActorType: "admin", Action: "DeleteServer", ResourceType: "server", ResourceName: "srv2"},
		{Actor: "system", ActorType: "service", Action: "SchemaChanged", ResourceType: "tool", ResourceName: "srv1/read"},
	}
	for _, e := range entries {
		require.NoError(t, store.LogAudit(ctx, e))
	}

	// List all
	all, err := store.ListAuditLog(ctx, registry.AuditFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Filter by actor
	byActor, err := store.ListAuditLog(ctx, registry.AuditFilter{Actor: "system"})
	require.NoError(t, err)
	require.Len(t, byActor, 1)
	assert.Equal(t, "SchemaChanged", byActor[0].Action)

	// Filter by action
	byAction, err := store.ListAuditLog(ctx, registry.AuditFilter{Action: "RegisterServer"})
	require.NoError(t, err)
	require.Len(t, byAction, 1)
	assert.Equal(t, "srv1", byAction[0].ResourceName)

	// Filter by limit
	limited, err := store.ListAuditLog(ctx, registry.AuditFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestSyncConfigServers_NewServer(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	// Pre-insert an API-managed server.
	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "api-srv", CreatedBy: "api", ProxyEnabled: true}))

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
			"github":  {Local: config.MCPServerLocal{Command: "github-mcp"}},
		},
	}

	require.NoError(t, store.SyncConfigServers(ctx, servers))

	got, err := store.ListServers(ctx, registry.ServerFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 3)

	for _, s := range got {
		if s.Name == "elastic" || s.Name == "github" {
			assert.Equal(t, "config", s.CreatedBy)
			assert.Equal(t, registry.StatusPending, s.Status)
			assert.Equal(t, "stdio", s.TransportType)
			assert.True(t, s.ProxyEnabled)
		}
	}
}

func TestSyncConfigServers_ChangedConfig(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp", Args: []string{"--port=9200"}}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, initial))

	srv1, err := store.GetServer(ctx, "elastic")
	require.NoError(t, err)
	origUpdatedAt := srv1.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	changed := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp", Args: []string{"--port=9300"}}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, changed))

	srv2, err := store.GetServer(ctx, "elastic")
	require.NoError(t, err)
	assert.NotEqual(t, string(srv1.ConnectionConfig), string(srv2.ConnectionConfig))
	assert.True(t, srv2.UpdatedAt.After(origUpdatedAt))
}

func TestSyncConfigServers_UnchangedConfig(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, servers))

	srv1, err := store.GetServer(ctx, "elastic")
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, store.SyncConfigServers(ctx, servers))

	srv2, err := store.GetServer(ctx, "elastic")
	require.NoError(t, err)
	assert.Equal(t, srv1.UpdatedAt, srv2.UpdatedAt, "updated_at should not change when config is identical")
}

func TestSyncConfigServers_APIServerNotOverwritten(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertServer(ctx, registry.Server{
		Name:             "shared-name",
		CreatedBy:        "api",
		ProxyEnabled:     true,
		ConnectionConfig: json.RawMessage(`{"command":"api-managed"}`),
	}))

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"shared-name": {Local: config.MCPServerLocal{Command: "config-managed"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, servers))

	got, err := store.GetServer(ctx, "shared-name")
	require.NoError(t, err)
	assert.Equal(t, "api", got.CreatedBy)
	assert.JSONEq(t, `{"command":"api-managed"}`, string(got.ConnectionConfig))
}

func TestSyncConfigServers_RemovedServerDisabled(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
			"vendor":  {Local: config.MCPServerLocal{Command: "vendor-mcp"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, initial))

	reduced := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, reduced))

	vendor, err := store.GetServer(ctx, "vendor")
	require.NoError(t, err)
	require.NotNil(t, vendor, "vendor should still exist (row preserved)")
	assert.False(t, vendor.ProxyEnabled, "vendor should be disabled")

	elastic, err := store.GetServer(ctx, "elastic")
	require.NoError(t, err)
	assert.True(t, elastic.ProxyEnabled, "elastic should remain enabled")
}

func TestSyncConfigServers_ReAddedServerReEnabled(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"elastic": {Local: config.MCPServerLocal{Command: "elastic-mcp"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, initial))

	// Remove.
	require.NoError(t, store.SyncConfigServers(ctx, config.MCPServersConfig{Servers: map[string]config.MCPServerEntry{}}))
	elastic, _ := store.GetServer(ctx, "elastic")
	require.False(t, elastic.ProxyEnabled)

	// Re-add.
	require.NoError(t, store.SyncConfigServers(ctx, initial))
	elastic, _ = store.GetServer(ctx, "elastic")
	assert.True(t, elastic.ProxyEnabled, "elastic should be re-enabled after re-add")
}

func TestSyncConfigServers_EmptyConfigDisablesAll(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	initial := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"a": {Local: config.MCPServerLocal{Command: "a-mcp"}},
			"b": {Local: config.MCPServerLocal{Command: "b-mcp"}},
		},
	}
	require.NoError(t, store.SyncConfigServers(ctx, initial))

	// Also add API-managed server.
	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "api-srv", CreatedBy: "api", ProxyEnabled: true}))

	require.NoError(t, store.SyncConfigServers(ctx, config.MCPServersConfig{}))

	a, _ := store.GetServer(ctx, "a")
	b, _ := store.GetServer(ctx, "b")
	assert.False(t, a.ProxyEnabled, "config-owned 'a' should be disabled")
	assert.False(t, b.ProxyEnabled, "config-owned 'b' should be disabled")

	api, _ := store.GetServer(ctx, "api-srv")
	assert.True(t, api.ProxyEnabled, "API-managed server should be untouched")
}

func TestUpsertServer_Defaults(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()

	// Upsert with zero-value fields to test defaults
	require.NoError(t, store.UpsertServer(ctx, registry.Server{Name: "defaults"}))

	got, err := store.GetServer(ctx, "defaults")
	require.NoError(t, err)
	assert.Equal(t, "stdio", got.TransportType)
	assert.Equal(t, registry.StatusPending, got.Status)
	assert.Equal(t, "api", got.CreatedBy)
	assert.JSONEq(t, `{}`, string(got.ConnectionConfig))
	assert.JSONEq(t, `{}`, string(got.SecretRefs))
	assert.JSONEq(t, `{}`, string(got.ServerCapabilities))
}
