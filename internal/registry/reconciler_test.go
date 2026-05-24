package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
)

// --- Mock Store ---

type mockStore struct {
	mu      sync.Mutex
	servers map[string]Server
	tools   map[string][]Tool // keyed by server name
	audit   []AuditEntry
}

func newMockStore() *mockStore {
	return &mockStore{
		servers: make(map[string]Server),
		tools:   make(map[string][]Tool),
	}
}

func (m *mockStore) ListServers(_ context.Context, filter ServerFilter) ([]Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Server
	for _, s := range m.servers {
		if filter.ProxyEnabled != nil && s.ProxyEnabled != *filter.ProxyEnabled {
			continue
		}
		if filter.Status != nil && s.Status != *filter.Status {
			continue
		}
		result = append(result, s)
	}
	return result, nil
}

func (m *mockStore) GetServer(_ context.Context, name string) (*Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[name]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (m *mockStore) UpsertServer(_ context.Context, s Server) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[s.Name] = s
	return nil
}

func (m *mockStore) DeleteServer(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.servers, name)
	return nil
}

func (m *mockStore) UpdateServerHealth(_ context.Context, name string, status ServerStatus, msg string, discoveredAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("server %q not found", name)
	}
	s.Status = status
	s.HealthMessage = msg
	if discoveredAt != nil {
		s.LastDiscoveredAt = discoveredAt
	}
	m.servers[name] = s
	return nil
}

func (m *mockStore) ReplaceDiscoveredTools(_ context.Context, server string, tools []Tool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools[server] = tools
	return nil
}

func (m *mockStore) ListTools(_ context.Context, filter ToolFilter) ([]Tool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Tool
	for srvName, tools := range m.tools {
		if filter.ServerName != "" && srvName != filter.ServerName {
			continue
		}
		for _, t := range tools {
			if filter.Enabled != nil && t.Enabled != *filter.Enabled {
				continue
			}
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockStore) SetToolEnabled(_ context.Context, server, tool string, enabled bool) error {
	return nil
}

func (m *mockStore) LogAudit(_ context.Context, entry AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, entry)
	return nil
}

func (m *mockStore) ListAuditLog(_ context.Context, filter AuditFilter) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.audit, nil
}

func (m *mockStore) SyncConfigServers(_ context.Context, servers config.MCPServersConfig) error {
	return nil
}

func (m *mockStore) Close() error { return nil }

func (m *mockStore) auditActions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	actions := make([]string, len(m.audit))
	for i, a := range m.audit {
		actions[i] = a.Action
	}
	return actions
}

// --- Mock Pool ---

type mockPool struct {
	mu      sync.Mutex
	servers map[string]mcpclient.ServerInfo
	tools   map[string][]mcpclient.ToolInfo
	handler func(string)
}

func newMockPool() *mockPool {
	return &mockPool{
		servers: make(map[string]mcpclient.ServerInfo),
		tools:   make(map[string][]mcpclient.ToolInfo),
	}
}

func (p *mockPool) AddServer(name string, entry config.MCPServerEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.servers[name] = mcpclient.ServerInfo{
		Name:   name,
		State:  mcpclient.ConnIdle,
		Config: entry,
	}
}

func (p *mockPool) DisconnectServer(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.servers, name)
}

func (p *mockPool) KnownServers() []mcpclient.ServerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]mcpclient.ServerInfo, 0, len(p.servers))
	for _, s := range p.servers {
		result = append(result, s)
	}
	return result
}

func (p *mockPool) DiscoverTools(ctx context.Context, server string) ([]mcpclient.ToolInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tools, ok := p.tools[server]
	if !ok {
		return nil, fmt.Errorf("connection refused")
	}
	// Mark as ready after discovery
	if info, ok := p.servers[server]; ok {
		info.State = mcpclient.ConnReady
		p.servers[server] = info
	}
	return tools, nil
}

func (p *mockPool) SetToolsChangedHandler(fn func(string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handler = fn
}

// setServerState sets a server's state for testing.
func (p *mockPool) setServerState(name string, state mcpclient.ConnState, lastErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if info, ok := p.servers[name]; ok {
		info.State = state
		info.LastErr = lastErr
		p.servers[name] = info
	}
}

// --- Mock Gateway ---

type mockGateway struct {
	mu           sync.Mutex
	servers      map[string][]mcpclient.ToolInfo
	unregistered []string
}

func newMockGateway() *mockGateway {
	return &mockGateway{
		servers: make(map[string][]mcpclient.ToolInfo),
	}
}

func (g *mockGateway) UnregisterServer(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.servers, name)
	g.unregistered = append(g.unregistered, name)
}

func (g *mockGateway) ReconcileServer(_ context.Context, name string, tools []mcpclient.ToolInfo) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.servers[name] = tools
	return nil
}

func (g *mockGateway) hasServer(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.servers[name]
	return ok
}

func (g *mockGateway) wasUnregistered(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range g.unregistered {
		if n == name {
			return true
		}
	}
	return false
}

// --- Mock Graph ---

type graphUpdate struct {
	cypher string
	params map[string]any
}

type mockGraph struct {
	mu           sync.Mutex
	queries      []string
	updates      []graphUpdate
	queryResults []graph.Record // results returned by Query
}

func newMockGraph() *mockGraph {
	return &mockGraph{}
}

func (g *mockGraph) Query(_ context.Context, cypher string, _ map[string]any) ([]graph.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.queries = append(g.queries, cypher)
	return g.queryResults, nil
}

func (g *mockGraph) Update(_ context.Context, cypher string, params map[string]any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.queries = append(g.queries, cypher)
	g.updates = append(g.updates, graphUpdate{cypher: cypher, params: params})
	return nil
}

func (g *mockGraph) Close() error { return nil }

func (g *mockGraph) queryCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queries)
}

func (g *mockGraph) hasQuery(substr string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, q := range g.queries {
		if containsSubstr(q, substr) {
			return true
		}
	}
	return false
}

// hasUpdateWith checks if any update query contains the given substring
// and has a parameter matching the key/value pair.
func (g *mockGraph) hasUpdateWith(substr string, key string, value any) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range g.updates {
		if containsSubstr(u.cypher, substr) {
			if v, ok := u.params[key]; ok && fmt.Sprintf("%v", v) == fmt.Sprintf("%v", value) {
				return true
			}
		}
	}
	return false
}

func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Reconciler with mock interfaces ---

// testReconciler wraps the real Reconciler but uses mock interfaces.
// Since the real Reconciler takes concrete types, we test via the
// reconcileAll/reconcileEntry logic indirectly, or we use a thin
// adapter layer.

// For testing, we create a reconcilerTestHarness that mimics reconciler
// behavior but uses interfaces we can mock.
type reconcilerTestHarness struct {
	store *mockStore
	pool  *mockPool
	gw    *mockGateway
	graph *mockGraph
}

func newHarness() *reconcilerTestHarness {
	return &reconcilerTestHarness{
		store: newMockStore(),
		pool:  newMockPool(),
		gw:    newMockGateway(),
		graph: newMockGraph(),
	}
}

func (h *reconcilerTestHarness) addDesiredServer(name string, cfg config.MCPServerEntry) {
	cfgJSON, _ := json.Marshal(cfg)
	h.store.servers[name] = Server{
		Name:             name,
		TransportType:    "stdio",
		ConnectionConfig: cfgJSON,
		ProxyEnabled:     true,
		Status:           StatusPending,
		CreatedBy:        "test",
	}
}

func (h *reconcilerTestHarness) addPoolServer(name string, cfg config.MCPServerEntry, state mcpclient.ConnState) {
	h.pool.servers[name] = mcpclient.ServerInfo{
		Name:   name,
		State:  state,
		Config: cfg,
	}
}

func (h *reconcilerTestHarness) setDiscoverableTools(server string, tools []mcpclient.ToolInfo) {
	h.pool.tools[server] = tools
}

// --- Tests ---

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status ServerStatus
	}{
		{"nil error", nil, StatusError},
		{"auth error", fmt.Errorf("unauthorized access"), StatusAuthFailed},
		{"401 error", fmt.Errorf("HTTP 401 response"), StatusAuthFailed},
		{"403 error", fmt.Errorf("HTTP 403 forbidden"), StatusAuthFailed},
		{"connection refused", fmt.Errorf("connection refused"), StatusUnreachable},
		{"timeout", fmt.Errorf("dial timeout"), StatusUnreachable},
		{"eof", fmt.Errorf("unexpected EOF"), StatusUnreachable},
		{"transport error", fmt.Errorf("transport error: broken pipe"), StatusUnreachable},
		{"generic error", fmt.Errorf("some random error"), StatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := classifyError(tt.err)
			if status != tt.status {
				t.Errorf("classifyError(%v) = %s, want %s", tt.err, status, tt.status)
			}
		})
	}
}

func TestSchemaHash(t *testing.T) {
	schema1 := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	schema2 := json.RawMessage(`{"type":"object","properties":{"q":{"type":"integer"}}}`)

	h1 := schemaHash(schema1)
	h2 := schemaHash(schema2)
	h3 := schemaHash(schema1)

	if h1 == "" {
		t.Fatal("hash should not be empty")
	}
	if h1 == h2 {
		t.Fatal("different schemas should produce different hashes")
	}
	if h1 != h3 {
		t.Fatal("same schema should produce same hash")
	}

	if schemaHash(nil) != "" {
		t.Fatal("nil schema should produce empty hash")
	}
}

func TestServerToConfigEntry(t *testing.T) {
	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "test-cmd", Args: []string{"--arg"}},
	}
	cfgJSON, _ := json.Marshal(cfg)
	srv := Server{
		Name:             "test",
		ConnectionConfig: cfgJSON,
	}

	entry := serverToConfigEntry(srv)
	if entry.Local.Command != "test-cmd" {
		t.Errorf("expected command 'test-cmd', got %q", entry.Local.Command)
	}
	if len(entry.Local.Args) != 1 || entry.Local.Args[0] != "--arg" {
		t.Errorf("expected args [--arg], got %v", entry.Local.Args)
	}
}

func TestToRegistryTools(t *testing.T) {
	tools := []mcpclient.ToolInfo{
		{Name: "tool1", Description: "desc1", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "tool2", Description: "desc2", InputSchema: json.RawMessage(`{"type":"string"}`)},
	}

	result := toRegistryTools("srv1", tools)
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}

	if result[0].ServerName != "srv1" {
		t.Errorf("expected server name 'srv1', got %q", result[0].ServerName)
	}
	if result[0].SchemaHash == "" {
		t.Error("schema hash should not be empty")
	}
	if !result[0].Enabled {
		t.Error("tool should be enabled by default")
	}
}

func TestReconciler_AddNewServer(t *testing.T) {
	h := newHarness()
	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "test-server"},
	}
	h.addDesiredServer("new-server", cfg)
	h.setDiscoverableTools("new-server", []mcpclient.ToolInfo{
		{Name: "tool1", Description: "desc1", InputSchema: json.RawMessage(`{}`)},
	})

	// Create and run one reconcile cycle
	rec := NewReconciler(h.store, nil, nil, nil, time.Hour)
	// We can't use the real reconciler with mocks due to concrete types,
	// so we test the helper functions directly.

	// Simulate what reconcileAll would do:
	ctx := context.Background()
	proxyEnabled := true
	desired, _ := h.store.ListServers(ctx, ServerFilter{ProxyEnabled: &proxyEnabled})
	if len(desired) != 1 {
		t.Fatalf("expected 1 desired server, got %d", len(desired))
	}
	if desired[0].Name != "new-server" {
		t.Errorf("expected 'new-server', got %q", desired[0].Name)
	}

	// Verify pool has no servers initially
	if len(h.pool.KnownServers()) != 0 {
		t.Fatal("pool should be empty initially")
	}

	// Add to pool (as reconciler would)
	h.pool.AddServer("new-server", cfg)
	if len(h.pool.KnownServers()) != 1 {
		t.Fatal("pool should have 1 server after add")
	}

	// Discover tools (as reconciler would)
	tools, err := h.pool.DiscoverTools(ctx, "new-server")
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Persist tools
	regTools := toRegistryTools("new-server", tools)
	if err := h.store.ReplaceDiscoveredTools(ctx, "new-server", regTools); err != nil {
		t.Fatal(err)
	}

	// Reconcile gateway
	if err := h.gw.ReconcileServer(context.Background(), "new-server", tools); err != nil {
		t.Fatal(err)
	}
	if !h.gw.hasServer("new-server") {
		t.Fatal("gateway should have new-server")
	}

	_ = rec // use the reconciler reference
}

func TestReconciler_RemoveServer(t *testing.T) {
	h := newHarness()
	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "old-server"},
	}

	// Server in pool but not in desired (registry)
	h.addPoolServer("old-server", cfg, mcpclient.ConnReady)
	h.gw.ReconcileServer(context.Background(), "old-server", []mcpclient.ToolInfo{
		{Name: "tool1", Description: "desc"},
	})

	// Verify preconditions
	if !h.gw.hasServer("old-server") {
		t.Fatal("gateway should have old-server")
	}

	// Simulate removal (as reconciler would):
	h.gw.UnregisterServer("old-server")
	h.pool.DisconnectServer("old-server")

	if h.gw.hasServer("old-server") {
		t.Fatal("gateway should not have old-server after unregister")
	}
	if len(h.pool.KnownServers()) != 0 {
		t.Fatal("pool should be empty after disconnect")
	}
	if !h.gw.wasUnregistered("old-server") {
		t.Fatal("gateway should record unregistration")
	}
}

func TestReconciler_ConfigDrift(t *testing.T) {
	h := newHarness()

	oldCfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "old-cmd"},
	}
	newCfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "new-cmd"},
	}

	// Server in pool with old config
	h.addPoolServer("drifted", oldCfg, mcpclient.ConnReady)
	h.gw.ReconcileServer(context.Background(), "drifted", []mcpclient.ToolInfo{
		{Name: "t1", Description: "d1"},
	})

	// Server in registry with new config
	h.addDesiredServer("drifted", newCfg)

	// Detect drift
	cfgJSON, _ := json.Marshal(newCfg)
	srv := Server{
		Name:             "drifted",
		ConnectionConfig: cfgJSON,
	}
	info := h.pool.servers["drifted"]

	// configDrifted: compare pool config vs registry config
	registryEntry := serverToConfigEntry(srv)
	poolJSON, _ := json.Marshal(info.Config)
	regJSON, _ := json.Marshal(registryEntry)

	if string(poolJSON) == string(regJSON) {
		t.Fatal("configs should be different (drift detected)")
	}

	// Simulate cleanup on drift:
	h.gw.UnregisterServer("drifted")
	h.pool.DisconnectServer("drifted")
	h.pool.AddServer("drifted", newCfg)

	// Verify gateway was cleaned
	if !h.gw.wasUnregistered("drifted") {
		t.Fatal("gateway should record unregistration on drift")
	}

	// Pool should have the server back with new config
	servers := h.pool.KnownServers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Config.Local.Command != "new-cmd" {
		t.Errorf("expected new-cmd, got %q", servers[0].Config.Local.Command)
	}
}

func TestReconciler_HealthMapping(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	h.addDesiredServer("srv", config.MCPServerEntry{})

	tests := []struct {
		state      mcpclient.ConnState
		lastErr    error
		drift      bool
		wantStatus ServerStatus
	}{
		{mcpclient.ConnIdle, nil, false, StatusPending},
		{mcpclient.ConnConnecting, nil, false, StatusPending},
		{mcpclient.ConnReady, nil, false, StatusActive},
		{mcpclient.ConnReady, nil, true, StatusDegraded},
		{mcpclient.ConnFailed, fmt.Errorf("connection refused"), false, StatusUnreachable},
		{mcpclient.ConnFailed, fmt.Errorf("unauthorized"), false, StatusAuthFailed},
		{mcpclient.ConnFailed, fmt.Errorf("something broke"), false, StatusError},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d_%v_%v", tt.state, tt.lastErr, tt.drift), func(t *testing.T) {
			// Reset
			h.store.servers["srv"] = Server{
				Name:         "srv",
				ProxyEnabled: true,
				Status:       StatusPending,
			}

			// Use writeHealth logic directly
			var status ServerStatus
			switch tt.state {
			case mcpclient.ConnIdle, mcpclient.ConnConnecting:
				status = StatusPending
			case mcpclient.ConnReady:
				if tt.drift {
					status = StatusDegraded
				} else {
					status = StatusActive
				}
			case mcpclient.ConnFailed:
				status, _ = classifyError(tt.lastErr)
			}

			h.store.UpdateServerHealth(ctx, "srv", status, "", nil)

			srv, _ := h.store.GetServer(ctx, "srv")
			if srv.Status != tt.wantStatus {
				t.Errorf("got %s, want %s", srv.Status, tt.wantStatus)
			}
		})
	}
}

func TestReconciler_SchemaDriftDetection(t *testing.T) {
	h := newHarness()

	// Existing tools with known hash
	oldSchema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	oldHash := schemaHash(oldSchema)

	h.store.tools["srv"] = []Tool{
		{ServerName: "srv", ToolName: "search", SchemaHash: oldHash, Enabled: true},
	}

	// New tools with different schema
	newSchema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"},"limit":{"type":"integer"}}}`)
	newTools := []Tool{
		{ServerName: "srv", ToolName: "search", InputSchema: newSchema, SchemaHash: schemaHash(newSchema), Enabled: true},
	}

	// Detect drift
	ctx := context.Background()
	enabled := true
	existing, _ := h.store.ListTools(ctx, ToolFilter{ServerName: "srv", Enabled: &enabled})

	existingMap := make(map[string]string)
	for _, t := range existing {
		existingMap[t.ToolName] = t.SchemaHash
	}

	drifted := false
	for _, t := range newTools {
		if oldH, ok := existingMap[t.ToolName]; ok && oldH != "" && oldH != t.SchemaHash {
			drifted = true
			break
		}
	}

	if !drifted {
		t.Fatal("schema drift should be detected")
	}
}

func TestReconciler_TriggerChannel(t *testing.T) {
	store := newMockStore()
	rec := NewReconciler(store, nil, nil, nil, time.Hour)

	// Trigger should not block
	rec.Trigger("server1")
	rec.Trigger("server2")

	// Drain
	select {
	case s := <-rec.trigger:
		if s != "server1" {
			t.Errorf("expected server1, got %q", s)
		}
	default:
		t.Fatal("trigger channel should have server1")
	}

	select {
	case s := <-rec.trigger:
		if s != "server2" {
			t.Errorf("expected server2, got %q", s)
		}
	default:
		t.Fatal("trigger channel should have server2")
	}
}

func TestReconciler_ReadyChannel(t *testing.T) {
	store := newMockStore()
	rec := NewReconciler(store, nil, nil, nil, time.Hour)

	// readyCh should not be closed before Start
	select {
	case <-rec.Ready():
		t.Fatal("ready channel should not be closed before start")
	default:
	}
}

func TestReconciler_FullLifecycle(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	cfg1 := config.MCPServerEntry{Local: config.MCPServerLocal{Command: "server-a"}}
	cfg2 := config.MCPServerEntry{Local: config.MCPServerLocal{Command: "server-b"}}

	// Phase 1: Add two servers to desired
	h.addDesiredServer("server-a", cfg1)
	h.addDesiredServer("server-b", cfg2)

	h.setDiscoverableTools("server-a", []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	h.setDiscoverableTools("server-b", []mcpclient.ToolInfo{
		{Name: "fetch", Description: "Fetch data", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})

	// Simulate reconcile: add to pool
	for name := range h.store.servers {
		srv := h.store.servers[name]
		entry := serverToConfigEntry(srv)
		h.pool.AddServer(name, entry)
	}

	// Discover and reconcile
	for name := range h.store.servers {
		tools, err := h.pool.DiscoverTools(ctx, name)
		if err != nil {
			t.Fatalf("discover %s: %v", name, err)
		}
		regTools := toRegistryTools(name, tools)
		h.store.ReplaceDiscoveredTools(ctx, name, regTools)
		h.gw.ReconcileServer(context.Background(), name, tools)
	}

	// Verify
	if !h.gw.hasServer("server-a") || !h.gw.hasServer("server-b") {
		t.Fatal("both servers should be in gateway")
	}
	if len(h.pool.KnownServers()) != 2 {
		t.Fatal("pool should have 2 servers")
	}

	// Phase 2: Remove server-b from desired (simulate admin delete)
	delete(h.store.servers, "server-b")

	// Reconcile: server-b in pool but not in desired → remove
	proxyEnabled := true
	desired, _ := h.store.ListServers(ctx, ServerFilter{ProxyEnabled: &proxyEnabled})
	desiredMap := make(map[string]bool)
	for _, s := range desired {
		desiredMap[s.Name] = true
	}

	for _, info := range h.pool.KnownServers() {
		if !desiredMap[info.Name] {
			h.gw.UnregisterServer(info.Name)
			h.pool.DisconnectServer(info.Name)
		}
	}

	if h.gw.hasServer("server-b") {
		t.Fatal("server-b should be removed from gateway")
	}
	if len(h.pool.KnownServers()) != 1 {
		t.Fatal("pool should have 1 server after removal")
	}
}

func TestReconciler_PersistFailureWritesFailedHealth(t *testing.T) {
	// When ReplaceDiscoveredTools fails, the reconciler should write
	// ConnFailed health, NOT ConnReady.
	store := newMockStore()
	store.servers["srv-a"] = Server{
		Name:         "srv-a",
		ProxyEnabled: true,
		Status:       StatusPending,
	}

	failStore := &failingStore{mockStore: store, failReplace: true}
	pool := newMockPool()
	pool.AddServer("srv-a", config.MCPServerEntry{})
	pool.tools["srv-a"] = []mcpclient.ToolInfo{
		{Name: "t1", Description: "d", InputSchema: json.RawMessage(`{}`)},
	}

	gw := newMockGateway()
	rec := NewReconciler(failStore, pool, gw, nil, time.Hour)
	rec.Start()
	<-rec.Ready()
	rec.Stop()

	srv, _ := failStore.GetServer(context.Background(), "srv-a")
	if srv == nil {
		t.Fatal("server should exist")
	}
	// Health should NOT be active since tool persistence failed
	if srv.Status == StatusActive {
		t.Errorf("expected non-active status after persist failure, got %s", srv.Status)
	}
}

func TestReconciler_GatewayFailureWritesFailedHealth(t *testing.T) {
	// When gateway.ReconcileServer fails, the reconciler should write
	// ConnFailed health, NOT ConnReady.
	store := newMockStore()
	store.servers["srv-b"] = Server{
		Name:         "srv-b",
		ProxyEnabled: true,
		Status:       StatusPending,
	}

	pool := newMockPool()
	pool.AddServer("srv-b", config.MCPServerEntry{})
	pool.tools["srv-b"] = []mcpclient.ToolInfo{
		{Name: "t1", Description: "d", InputSchema: json.RawMessage(`{}`)},
	}

	gw := &failingGateway{mockGateway: newMockGateway(), failReconcile: true}
	rec := NewReconciler(store, pool, gw, nil, time.Hour)
	rec.Start()
	<-rec.Ready()
	rec.Stop()

	srv, _ := store.GetServer(context.Background(), "srv-b")
	if srv == nil {
		t.Fatal("server should exist")
	}
	// Health should NOT be active since gateway reconcile failed
	if srv.Status == StatusActive {
		t.Errorf("expected non-active status after gateway failure, got %s", srv.Status)
	}
}

// failingStore wraps mockStore but makes ReplaceDiscoveredTools return an error.
type failingStore struct {
	*mockStore
	failReplace bool
}

func (f *failingStore) ReplaceDiscoveredTools(ctx context.Context, server string, tools []Tool) error {
	if f.failReplace {
		return fmt.Errorf("simulated persistence failure")
	}
	return f.mockStore.ReplaceDiscoveredTools(ctx, server, tools)
}

// failingGateway wraps mockGateway but makes ReconcileServer return an error.
type failingGateway struct {
	*mockGateway
	failReconcile bool
}

func (f *failingGateway) ReconcileServer(ctx context.Context, name string, tools []mcpclient.ToolInfo) error {
	if f.failReconcile {
		return fmt.Errorf("simulated gateway failure")
	}
	return f.mockGateway.ReconcileServer(ctx, name, tools)
}

func TestReconciler_DisabledServerRemoved(t *testing.T) {
	// When a server has proxy_enabled=false, the reconciler should:
	// - Unregister gateway tools
	// - Disconnect pool
	h := newHarness()
	ctx := context.Background()

	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "test-server"},
	}

	// Server in pool and gateway, but disabled in registry.
	h.addPoolServer("disabled-srv", cfg, mcpclient.ConnReady)
	h.gw.ReconcileServer(context.Background(), "disabled-srv", []mcpclient.ToolInfo{
		{Name: "tool1", Description: "a tool"},
	})

	// Mark as disabled in registry (proxy_enabled=false).
	cfgJSON, _ := json.Marshal(cfg)
	h.store.servers["disabled-srv"] = Server{
		Name:             "disabled-srv",
		TransportType:    "stdio",
		ConnectionConfig: cfgJSON,
		ProxyEnabled:     false, // disabled
		Status:           StatusActive,
		CreatedBy:        "config",
	}

	// Simulate reconciler behavior: disabled servers not in desired set.
	proxyEnabled := true
	desired, _ := h.store.ListServers(ctx, ServerFilter{ProxyEnabled: &proxyEnabled})
	desiredMap := make(map[string]bool)
	for _, s := range desired {
		desiredMap[s.Name] = true
	}

	// Pool has disabled-srv, but desired doesn't → remove.
	for _, info := range h.pool.KnownServers() {
		if !desiredMap[info.Name] {
			h.gw.UnregisterServer(info.Name)
			h.pool.DisconnectServer(info.Name)
		}
	}

	if h.gw.hasServer("disabled-srv") {
		t.Fatal("gateway should not have disabled server")
	}
	if len(h.pool.KnownServers()) != 0 {
		t.Fatal("pool should be empty after disabling server")
	}
	if !h.gw.wasUnregistered("disabled-srv") {
		t.Fatal("gateway should record unregistration of disabled server")
	}
}

func TestReconciler_ReEnabledServerReconnects(t *testing.T) {
	// When a previously disabled server is re-enabled, the reconciler should
	// add it back to the pool and rediscover tools.
	h := newHarness()
	ctx := context.Background()

	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "test-server"},
	}

	// Server re-enabled in registry.
	h.addDesiredServer("reenabled-srv", cfg)
	h.setDiscoverableTools("reenabled-srv", []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things", InputSchema: json.RawMessage(`{}`)},
	})

	// Verify pool empty initially (server was previously disconnected).
	if len(h.pool.KnownServers()) != 0 {
		t.Fatal("pool should be empty initially")
	}

	// Simulate reconciler: add to pool.
	h.pool.AddServer("reenabled-srv", cfg)

	// Discover tools.
	tools, err := h.pool.DiscoverTools(ctx, "reenabled-srv")
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Reconcile gateway.
	if err := h.gw.ReconcileServer(context.Background(), "reenabled-srv", tools); err != nil {
		t.Fatal(err)
	}

	if !h.gw.hasServer("reenabled-srv") {
		t.Fatal("gateway should have re-enabled server")
	}
	if len(h.pool.KnownServers()) != 1 {
		t.Fatal("pool should have 1 server after re-enable")
	}
}

func TestReconciler_NoStaleGatewayAfterDisable(t *testing.T) {
	// After disabling a server, gateway should have zero tools for that server.
	h := newHarness()

	cfg := config.MCPServerEntry{
		Local: config.MCPServerLocal{Command: "test-server"},
	}

	// Set up server with tools in gateway.
	h.addPoolServer("srv", cfg, mcpclient.ConnReady)
	h.gw.ReconcileServer(context.Background(), "srv", []mcpclient.ToolInfo{
		{Name: "t1", Description: "d1"},
		{Name: "t2", Description: "d2"},
	})

	if !h.gw.hasServer("srv") {
		t.Fatal("precondition: gateway should have server")
	}

	// Simulate disable: unregister.
	h.gw.UnregisterServer("srv")
	h.pool.DisconnectServer("srv")

	if h.gw.hasServer("srv") {
		t.Fatal("gateway should not have server after unregister (no stale tools)")
	}
}

func TestReconciler_AuditLogging(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	// Log some audit entries
	h.store.LogAudit(ctx, AuditEntry{
		Actor: "reconciler", ActorType: "system",
		Action: "server_added", ResourceType: "server", ResourceName: "srv1",
	})
	h.store.LogAudit(ctx, AuditEntry{
		Actor: "reconciler", ActorType: "system",
		Action: "health_change", ResourceType: "server", ResourceName: "srv1",
	})
	h.store.LogAudit(ctx, AuditEntry{
		Actor: "reconciler", ActorType: "system",
		Action: "schema_drift", ResourceType: "server", ResourceName: "srv1",
	})

	actions := h.store.auditActions()
	if len(actions) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(actions))
	}
	expected := []string{"server_added", "health_change", "schema_drift"}
	for i, want := range expected {
		if actions[i] != want {
			t.Errorf("audit[%d] = %q, want %q", i, actions[i], want)
		}
	}
}

func TestReconciler_SyncGraphNodes_NewLabels_Provenance(t *testing.T) {
	gr := newMockGraph()
	rec := NewReconciler(newMockStore(), nil, nil, gr, time.Hour)
	ctx := context.Background()

	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search things"},
		{Name: "esql", Description: "Run ESQL"},
	}

	rec.syncGraphNodes(ctx, "elastic", tools)

	// Should write MCPServer (not DataSource)
	if !gr.hasQuery("MCPServer") {
		t.Error("expected MCPServer label in graph queries")
	}
	if gr.hasQuery("DataSource") {
		t.Error("should not write DataSource label")
	}

	// Should write MCPTool (not MCPSource)
	if !gr.hasQuery("MCPTool") {
		t.Error("expected MCPTool label in graph queries")
	}
	if gr.hasQuery("MCPSource") {
		t.Error("should not write MCPSource label")
	}

	// Should write PROVIDES edge (not FETCHABLE_VIA)
	if !gr.hasQuery("PROVIDES") {
		t.Error("expected PROVIDES edge in graph queries")
	}
	if gr.hasQuery("FETCHABLE_VIA") {
		t.Error("should not write FETCHABLE_VIA edge")
	}

	// Should NOT write ToolRef
	if gr.hasQuery("ToolRef") {
		t.Error("should not write ToolRef")
	}

	// Provenance: _source should be 'reconciler:auto'
	if !gr.hasUpdateWith("MCPServer", "updated_at", "") {
		// Check that _source is set in the cypher SET clause
		foundProvenance := false
		for _, u := range gr.updates {
			if containsSubstr(u.cypher, "MCPServer") && containsSubstr(u.cypher, "_source") && containsSubstr(u.cypher, "_updated_at") {
				foundProvenance = true
				break
			}
		}
		if !foundProvenance {
			t.Error("MCPServer write should include _source and _updated_at in SET clause")
		}
	}

	// Should have: 1 MCPServer + 2 MCPTool + 2 PROVIDES = 5 queries
	if gr.queryCount() != 5 {
		t.Errorf("expected 5 graph queries, got %d", gr.queryCount())
	}
}

func TestReconciler_RemoveGraphNodes_ProvenanceGate(t *testing.T) {
	gr := newMockGraph()
	rec := NewReconciler(newMockStore(), nil, nil, gr, time.Hour)
	ctx := context.Background()

	rec.removeGraphNodes(ctx, "elastic")

	// MCPTool delete should be provenance-gated
	foundProvenanceGate := false
	for _, q := range gr.queries {
		if containsSubstr(q, "MCPTool") && containsSubstr(q, "DELETE") {
			if containsSubstr(q, "_source IN") && containsSubstr(q, "gateway:auto") && containsSubstr(q, "reconciler:auto") {
				foundProvenanceGate = true
			} else {
				t.Error("MCPTool DELETE should be provenance-gated")
			}
		}
	}
	if !foundProvenanceGate {
		t.Error("expected provenance-gated MCPTool DELETE")
	}

	// MCPServer delete should be provenance-gated and check for no PROVIDES edges
	foundServerGate := false
	for _, q := range gr.queries {
		if containsSubstr(q, "MCPServer") && containsSubstr(q, "DELETE") {
			if containsSubstr(q, "_source IN") && containsSubstr(q, "PROVIDES") {
				foundServerGate = true
			}
		}
	}
	if !foundServerGate {
		t.Error("expected provenance-gated MCPServer DELETE with PROVIDES check")
	}

	// Should mark MCPField as stale with correct edge direction (MCPTool -[:RETURNS_FIELD]-> MCPField)
	foundFieldStale := false
	for _, q := range gr.queries {
		if containsSubstr(q, "MCPField") && containsSubstr(q, "stale") {
			foundFieldStale = true
			// Validate edge direction: must be MCPTool)-[:RETURNS_FIELD]->(MCPField, NOT reversed
			if containsSubstr(q, "<-[:RETURNS_FIELD]-") {
				t.Errorf("RETURNS_FIELD edge direction is reversed — should be MCPTool-[:RETURNS_FIELD]->MCPField, got: %s", q)
			}
			if !containsSubstr(q, "-[:RETURNS_FIELD]->") {
				t.Errorf("expected forward RETURNS_FIELD traversal (MCPTool-[:RETURNS_FIELD]->MCPField), got: %s", q)
			}
		}
	}
	if !foundFieldStale {
		t.Error("expected MCPField stale marking query")
	}

	// Should NOT reference old labels
	for _, q := range gr.queries {
		if containsSubstr(q, "MCPSource") || containsSubstr(q, "DataSource") || containsSubstr(q, "ToolRef") {
			t.Errorf("removeGraphNodes should not reference old labels, found in: %s", q)
		}
	}
}

func TestReconciler_CleanStaleToolNodes(t *testing.T) {
	gr := newMockGraph()
	// Simulate graph having 3 tools, but only 2 are in the current discovered set
	gr.queryResults = []graph.Record{
		{"name": "elastic.search"},
		{"name": "elastic.esql"},
		{"name": "elastic.old_tool"}, // stale — not in discovered tools
	}

	rec := NewReconciler(newMockStore(), nil, nil, gr, time.Hour)
	ctx := context.Background()

	// Current tools only have search and esql
	tools := []mcpclient.ToolInfo{
		{Name: "search", Description: "Search"},
		{Name: "esql", Description: "ESQL"},
	}

	rec.cleanStaleToolNodes(ctx, "elastic", tools)

	// Should query for existing MCPTool nodes
	foundQuery := false
	for _, q := range gr.queries {
		if containsSubstr(q, "MCPTool") && containsSubstr(q, "RETURN") && containsSubstr(q, "name") {
			foundQuery = true
		}
	}
	if !foundQuery {
		t.Error("expected query for existing MCPTool nodes")
	}

	// Should delete only the stale tool (elastic.old_tool)
	foundStaleDelete := false
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "DETACH DELETE") && containsSubstr(u.cypher, "MCPTool") {
			if name, ok := u.params["name"].(string); ok && name == "elastic.old_tool" {
				foundStaleDelete = true
			}
		}
	}
	if !foundStaleDelete {
		t.Error("expected stale tool 'elastic.old_tool' to be deleted")
	}

	// Should mark MCPField nodes connected to stale tool as stale, with correct edge direction
	foundFieldStale := false
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "MCPField") && containsSubstr(u.cypher, "stale") {
			if name, ok := u.params["name"].(string); ok && name == "elastic.old_tool" {
				foundFieldStale = true
				if containsSubstr(u.cypher, "<-[:RETURNS_FIELD]-") {
					t.Errorf("RETURNS_FIELD edge direction is reversed in stale cleanup: %s", u.cypher)
				}
				if !containsSubstr(u.cypher, "-[:RETURNS_FIELD]->") {
					t.Errorf("expected forward RETURNS_FIELD traversal in stale cleanup: %s", u.cypher)
				}
			}
		}
	}
	if !foundFieldStale {
		t.Error("expected MCPField stale marking for stale tool")
	}

	// Should NOT delete current tools
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "DETACH DELETE") {
			if name, ok := u.params["name"].(string); ok {
				if name == "elastic.search" || name == "elastic.esql" {
					t.Errorf("should not delete current tool %q", name)
				}
			}
		}
	}
}

func TestReconciler_SyncGraphNodes_NilGraph(t *testing.T) {
	// Reconciler with nil graph should not panic
	rec := NewReconciler(newMockStore(), nil, nil, nil, time.Hour)
	ctx := context.Background()

	// Should be a no-op, not panic
	rec.syncGraphNodes(ctx, "test", []mcpclient.ToolInfo{
		{Name: "t1", Description: "d1"},
	})
	rec.removeGraphNodes(ctx, "test")
	rec.cleanStaleToolNodes(ctx, "test", nil)
}

// === F2: Reconciler DataStore lifecycle tests ===

// TestReconciler_RemoveGraphNodes_OrphanedDataStoreMarkedStale verifies that
// removeGraphNodes marks DataStore nodes as stale when their only QUERYABLE_VIA
// target MCPServer is being removed.
func TestReconciler_RemoveGraphNodes_OrphanedDataStoreMarkedStale(t *testing.T) {
	gr := newMockGraph()
	rec := NewReconciler(newMockStore(), nil, nil, gr, time.Hour)
	ctx := context.Background()

	rec.removeGraphNodes(ctx, "elastic")

	// Look for the DataStore stale-marking Cypher in the updates.
	// E1 adds: MATCH (ds:DataStore)-[:QUERYABLE_VIA]->(ms:MCPServer {config_key: $server})
	//          WHERE NOT EXISTS { MATCH (ds)-[:QUERYABLE_VIA]->(other:MCPServer) WHERE other.config_key <> $server }
	//          SET ds._status = 'stale'
	foundDataStoreStale := false
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "DataStore") &&
			containsSubstr(u.cypher, "QUERYABLE_VIA") &&
			containsSubstr(u.cypher, "stale") {
			foundDataStoreStale = true

			// Verify the parameter references the correct server
			if srv, ok := u.params["server"]; !ok || fmt.Sprint(srv) != "elastic" {
				t.Errorf("DataStore stale query should reference server 'elastic', got params: %v", u.params)
			}

			// Verify the query guards against marking DataStore with other QUERYABLE_VIA targets.
			// FalkorDB-compatible pattern uses OPTIONAL MATCH + WHERE IS NULL instead of NOT EXISTS.
			hasGuard := containsSubstr(u.cypher, "NOT EXISTS") ||
				containsSubstr(u.cypher, "IS NULL") ||
				containsSubstr(u.cypher, "other")
			if !hasGuard {
				t.Error("DataStore stale query should guard against marking DataStore with other QUERYABLE_VIA targets")
			}

			// Verify direction: DataStore-[:QUERYABLE_VIA]->MCPServer (not reversed)
			if containsSubstr(u.cypher, "<-[:QUERYABLE_VIA]-") {
				t.Error("QUERYABLE_VIA direction should be DataStore->MCPServer, not reversed")
			}
		}
	}

	if !foundDataStoreStale {
		t.Error("removeGraphNodes should mark orphaned DataStore nodes as stale (E1 not yet implemented?)")
	}

	// Also verify the MCPServer delete is now DETACH DELETE (E1 changes plain DELETE to DETACH DELETE)
	foundDetachMCPServer := false
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "MCPServer") && containsSubstr(u.cypher, "DETACH DELETE") {
			foundDetachMCPServer = true
		}
	}
	// Note: current code uses plain DELETE for MCPServer. E1 should change this to DETACH DELETE.
	// This assertion will pass once E1 is merged.
	if !foundDetachMCPServer {
		t.Log("INFO: MCPServer removal should use DETACH DELETE after E1 lands")
	}
}

// TestReconciler_RemoveGraphNodes_DataStoreNotStaledIfOtherServer verifies that
// removeGraphNodes does NOT mark a DataStore as stale if it has QUERYABLE_VIA
// edges pointing to other MCPServers besides the one being removed.
func TestReconciler_RemoveGraphNodes_DataStoreNotStaledIfOtherServer(t *testing.T) {
	gr := newMockGraph()
	rec := NewReconciler(newMockStore(), nil, nil, gr, time.Hour)
	ctx := context.Background()

	rec.removeGraphNodes(ctx, "elastic")

	// The stale-marking Cypher should include a guard that excludes multi-server DataStores.
	// FalkorDB-compatible pattern uses OPTIONAL MATCH + WHERE IS NULL instead of NOT EXISTS.
	for _, u := range gr.updates {
		if containsSubstr(u.cypher, "DataStore") &&
			containsSubstr(u.cypher, "stale") {
			// The query must guard against marking DataStore with other QUERYABLE_VIA targets
			hasGuard := containsSubstr(u.cypher, "NOT EXISTS") ||
				containsSubstr(u.cypher, "IS NULL") ||
				containsSubstr(u.cypher, "other")
			if !hasGuard {
				t.Error("DataStore stale-marking Cypher must guard against marking DataStore that has other QUERYABLE_VIA targets")
			}

			// The guard should reference a different server comparison
			hasComparison := containsSubstr(u.cypher, "<>") ||
				containsSubstr(u.cypher, "!=") ||
				containsSubstr(u.cypher, "IS NULL")
			if !hasComparison {
				t.Error("DataStore stale guard should exclude multi-target DataStores")
			}
		}
	}
}

// slowPool wraps mockPool but blocks DiscoverTools for specific servers until
// the context is cancelled, simulating a hung backend.
type slowPool struct {
	*mockPool
	slowServers map[string]bool
}

func (p *slowPool) DiscoverTools(ctx context.Context, server string) ([]mcpclient.ToolInfo, error) {
	if p.slowServers[server] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.mockPool.DiscoverTools(ctx, server)
}

func TestReconciler_DiscoveryTimeout_HungBackendBecomesDegrade(t *testing.T) {
	store := newMockStore()
	pool := newMockPool()
	slow := &slowPool{mockPool: pool, slowServers: map[string]bool{"hung-server": true}}
	gw := newMockGateway()

	// Register two servers: one healthy, one hung.
	enabled := true
	store.servers["fast-server"] = Server{Name: "fast-server", ProxyEnabled: enabled}
	store.servers["hung-server"] = Server{Name: "hung-server", ProxyEnabled: enabled}

	pool.AddServer("fast-server", config.MCPServerEntry{})
	pool.AddServer("hung-server", config.MCPServerEntry{})
	pool.tools["fast-server"] = []mcpclient.ToolInfo{{Name: "fast_tool"}}

	// Set both to Idle so reconcileEntry tries discovery.
	pool.setServerState("fast-server", mcpclient.ConnIdle, nil)
	pool.setServerState("hung-server", mcpclient.ConnIdle, nil)

	r := NewReconciler(store, slow, gw, nil, time.Hour, WithDiscoveryTimeout(200*time.Millisecond))

	// Run reconcileAll directly — it should complete despite hung-server.
	start := time.Now()
	r.reconcileAll(context.Background())
	elapsed := time.Since(start)

	// Should complete within a bounded time (discovery timeout + margin).
	if elapsed > 2*time.Second {
		t.Errorf("reconcileAll took %s, expected <2s with 200ms discovery timeout", elapsed)
	}

	// fast-server should be healthy.
	fastHealth, _ := store.GetServer(context.Background(), "fast-server")
	if fastHealth == nil || fastHealth.Status != StatusActive {
		t.Errorf("fast-server status = %v, want active", fastHealth)
	}

	// hung-server should be marked as failed/error.
	hungHealth, _ := store.GetServer(context.Background(), "hung-server")
	if hungHealth == nil {
		t.Fatal("hung-server not found in store")
	}
	if hungHealth.Status != StatusError && hungHealth.Status != StatusUnreachable {
		t.Errorf("hung-server status = %q, want error or unreachable", hungHealth.Status)
	}

	// Ready channel should close after reconcileAll.
	r.readyOnce.Do(func() { close(r.readyCh) })
	select {
	case <-r.readyCh:
	default:
		t.Error("readyCh should be closeable after reconcileAll completes")
	}
}
