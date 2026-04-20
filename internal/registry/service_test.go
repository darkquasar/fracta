package registry_test

import (
	"context"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/authz"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry"
)

// mockStore is a minimal Store implementation for testing RegistryService enforcement.
type mockStore struct {
	upserted    []registry.Server
	deleted     []string
	toolEnabled []toolEnabledCall
	audits      []registry.AuditEntry
}

type toolEnabledCall struct {
	server, tool string
	enabled      bool
}

func (m *mockStore) ListServers(context.Context, registry.ServerFilter) ([]registry.Server, error) {
	return nil, nil
}
func (m *mockStore) GetServer(context.Context, string) (*registry.Server, error) { return nil, nil }
func (m *mockStore) UpsertServer(_ context.Context, s registry.Server) error {
	m.upserted = append(m.upserted, s)
	return nil
}
func (m *mockStore) DeleteServer(_ context.Context, name string) error {
	m.deleted = append(m.deleted, name)
	return nil
}
func (m *mockStore) UpdateServerHealth(context.Context, string, registry.ServerStatus, string, *time.Time) error {
	return nil
}
func (m *mockStore) ReplaceDiscoveredTools(context.Context, string, []registry.Tool) error {
	return nil
}
func (m *mockStore) ListTools(context.Context, registry.ToolFilter) ([]registry.Tool, error) {
	return nil, nil
}
func (m *mockStore) SetToolEnabled(_ context.Context, server, tool string, enabled bool) error {
	m.toolEnabled = append(m.toolEnabled, toolEnabledCall{server, tool, enabled})
	return nil
}
func (m *mockStore) LogAudit(_ context.Context, entry registry.AuditEntry) error {
	m.audits = append(m.audits, entry)
	return nil
}
func (m *mockStore) ListAuditLog(context.Context, registry.AuditFilter) ([]registry.AuditEntry, error) {
	return nil, nil
}
func (m *mockStore) SyncConfigServers(context.Context, config.MCPServersConfig) error { return nil }
func (m *mockStore) Close() error                                             { return nil }

func adminCtx() context.Context {
	return authz.WithSubject(context.Background(), authz.Subject{
		Type: authz.SubjectAdmin, ID: "admin-user", Roles: []string{"admin"},
	})
}

func viewerCtx() context.Context {
	return authz.WithSubject(context.Background(), authz.Subject{
		Type: authz.SubjectViewer, ID: "viewer-user", Roles: []string{"viewer"},
	})
}

func operatorCtx() context.Context {
	return authz.WithSubject(context.Background(), authz.Subject{
		Type: authz.SubjectOperator, ID: "operator-user", Roles: []string{"operator"},
	})
}

func agentCtx() context.Context {
	return authz.WithSubject(context.Background(), authz.Subject{
		Type: authz.SubjectAgent, ID: "agent-task-1", Roles: []string{"agent"},
	})
}

func TestRegistryService_RegisterServer_AdminAllowed(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.RegisterServer(adminCtx(), registry.Server{Name: "test-server"})
	if err != nil {
		t.Fatalf("admin should be allowed to register: %v", err)
	}
	if len(store.upserted) != 1 || store.upserted[0].Name != "test-server" {
		t.Error("expected server to be upserted in store")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "RegisterServer" {
		t.Error("expected audit log entry for RegisterServer")
	}
}

func TestRegistryService_RegisterServer_ViewerDenied(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.RegisterServer(viewerCtx(), registry.Server{Name: "test-server"})
	if err == nil {
		t.Fatal("viewer should be denied")
	}
	if _, ok := err.(*authz.ForbiddenError); !ok {
		t.Errorf("expected *ForbiddenError, got %T", err)
	}
	if len(store.upserted) != 0 {
		t.Error("store should not have been called")
	}
}

func TestRegistryService_DeleteServer_AdminAllowed(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.DeleteServer(adminCtx(), "my-server")
	if err != nil {
		t.Fatalf("admin should be allowed to delete: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "my-server" {
		t.Error("expected server to be deleted from store")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "DeleteServer" {
		t.Error("expected audit log entry for DeleteServer")
	}
}

func TestRegistryService_DeleteServer_OperatorDenied(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.DeleteServer(operatorCtx(), "my-server")
	if err == nil {
		t.Fatal("operator should be denied from deleting servers")
	}
	if len(store.deleted) != 0 {
		t.Error("store should not have been called")
	}
}

func TestRegistryService_UpdateServer_AdminAllowed(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.UpdateServer(adminCtx(), registry.Server{Name: "srv"})
	if err != nil {
		t.Fatalf("admin should be allowed to update: %v", err)
	}
	if len(store.upserted) != 1 {
		t.Error("expected server to be upserted")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "UpdateServer" {
		t.Error("expected audit log entry for UpdateServer")
	}
}

func TestRegistryService_UpdateServer_AgentDenied(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.UpdateServer(agentCtx(), registry.Server{Name: "srv"})
	if err == nil {
		t.Fatal("agent should be denied from updating servers")
	}
}

func TestRegistryService_SetToolEnabled_OperatorAllowed(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.SetToolEnabled(operatorCtx(), "srv", "my-tool", true)
	if err != nil {
		t.Fatalf("operator should be allowed to enable tools: %v", err)
	}
	if len(store.toolEnabled) != 1 {
		t.Fatal("expected SetToolEnabled call")
	}
	if store.toolEnabled[0].server != "srv" || store.toolEnabled[0].tool != "my-tool" || !store.toolEnabled[0].enabled {
		t.Error("unexpected SetToolEnabled arguments")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "EnableTool" {
		t.Error("expected audit log entry for EnableTool")
	}
}

func TestRegistryService_SetToolEnabled_DisableAuditsCorrectly(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.SetToolEnabled(operatorCtx(), "srv", "my-tool", false)
	if err != nil {
		t.Fatalf("operator should be allowed to disable tools: %v", err)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "DisableTool" {
		t.Error("expected audit log entry for DisableTool")
	}
}

func TestRegistryService_SetToolEnabled_ViewerDenied(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.SetToolEnabled(viewerCtx(), "srv", "tool", true)
	if err == nil {
		t.Fatal("viewer should be denied from enabling tools")
	}
}

func TestRegistryService_SetToolEnabled_AgentDenied(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.SetToolEnabled(agentCtx(), "srv", "tool", false)
	if err == nil {
		t.Fatal("agent should be denied from disabling tools")
	}
}

func TestRegistryService_NoSubject(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	// No subject in context -> UnauthorizedError
	ctx := context.Background()

	if err := svc.RegisterServer(ctx, registry.Server{Name: "s"}); err == nil {
		t.Error("expected error with no subject")
	} else if _, ok := err.(*authz.UnauthorizedError); !ok {
		t.Errorf("expected *UnauthorizedError, got %T", err)
	}

	if err := svc.UpdateServer(ctx, registry.Server{Name: "s"}); err == nil {
		t.Error("expected error with no subject")
	}

	if err := svc.DeleteServer(ctx, "s"); err == nil {
		t.Error("expected error with no subject")
	}

	if err := svc.SetToolEnabled(ctx, "s", "t", true); err == nil {
		t.Error("expected error with no subject")
	}
}

func TestRegistryService_AuditEntryFields(t *testing.T) {
	store := &mockStore{}
	svc := registry.NewRegistryService(store, &authz.DefaultAuthorizer{})

	err := svc.RegisterServer(adminCtx(), registry.Server{Name: "audit-test"})
	if err != nil {
		t.Fatal(err)
	}

	if len(store.audits) != 1 {
		t.Fatal("expected 1 audit entry")
	}
	a := store.audits[0]
	if a.Actor != "admin-user" {
		t.Errorf("expected actor=admin-user, got %s", a.Actor)
	}
	if a.ActorType != "admin" {
		t.Errorf("expected actor_type=admin, got %s", a.ActorType)
	}
	if a.ResourceType != "server" {
		t.Errorf("expected resource_type=server, got %s", a.ResourceType)
	}
	if a.ResourceName != "audit-test" {
		t.Errorf("expected resource_name=audit-test, got %s", a.ResourceName)
	}
	if a.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}
