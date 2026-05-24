package mcpclient_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/registry/sqliteregistry"
)

// --- E2E helpers ---

// e2eBuildBinary compiles a Go main package from testdata and returns the binary path.
func e2eBuildBinary(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	src := filepath.Join("testdata", name)
	cmd := exec.Command("go", "build", "-o", bin, "./"+src)
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building %s: %v\n%s", name, err, out)
	}
	return bin
}

// e2eGateway tracks tool registrations for assertions.
type e2eGateway struct {
	mu           sync.Mutex
	servers      map[string][]mcpclient.ToolInfo
	unregistered []string
}

func newE2EGateway() *e2eGateway {
	return &e2eGateway{servers: make(map[string][]mcpclient.ToolInfo)}
}

func (g *e2eGateway) UnregisterServer(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.servers, name)
	g.unregistered = append(g.unregistered, name)
}

func (g *e2eGateway) ReconcileServer(_ context.Context, name string, tools []mcpclient.ToolInfo) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.servers[name] = tools
	return nil
}

func (g *e2eGateway) hasServer(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.servers[name]
	return ok
}

func (g *e2eGateway) toolCount(name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.servers[name])
}

func (g *e2eGateway) wasUnregistered(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range g.unregistered {
		if n == name {
			return true
		}
	}
	return false
}

// e2eGraph is a no-op graph client for E2E tests.
type e2eGraph struct{}

func (e2eGraph) Query(context.Context, string, map[string]any) ([]graph.Record, error) {
	return nil, nil
}
func (e2eGraph) Update(context.Context, string, map[string]any) error { return nil }
func (e2eGraph) Close() error                                         { return nil }

// e2eHarness bundles all E2E components.
type e2eHarness struct {
	store   registry.Store
	pool    *mcpclient.Pool
	gw      *e2eGateway
	graph   e2eGraph
	cleanup func()
}

func newE2EHarness(t *testing.T) *e2eHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	store, err := sqliteregistry.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	pool := mcpclient.NewPool(config.MCPServersConfig{}, "local")
	gw := newE2EGateway()

	return &e2eHarness{
		store:   store,
		pool:    pool,
		gw:      gw,
		cleanup: func() { pool.Close(); store.Close() },
	}
}

// simulateRestart mimics a fracta restart: close old pool, SyncConfigServers,
// create fresh pool + gateway, run one reconcile cycle. This matches real
// behavior where the process restarts and builds everything from scratch.
func (h *e2eHarness) simulateRestart(t *testing.T, servers config.MCPServersConfig) {
	t.Helper()
	ctx := context.Background()

	// Step 1: Close the old pool (process shutdown).
	h.pool.Close()

	// Step 2: Sync config to registry (what controlplane does at startup).
	if err := h.store.SyncConfigServers(ctx, servers); err != nil {
		t.Fatalf("SyncConfigServers: %v", err)
	}

	// Step 3: Create fresh pool + gateway (process startup — all runtime
	// state is rebuilt; only the registry persists across restarts).
	h.pool = mcpclient.NewPool(config.MCPServersConfig{}, "local")
	h.gw = newE2EGateway()

	// Step 4: Run one reconcile cycle via a short-lived reconciler.
	// Use a very long interval so only the initial reconcile runs.
	rec := registry.NewReconciler(h.store, h.pool, h.gw, h.graph, time.Hour)
	rec.Start()
	select {
	case <-rec.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("reconciler did not become ready")
	}
	rec.Stop()
}

// --- E2E Tests ---

func TestE2E_FailingBinary_EnrichedError(t *testing.T) {
	// T23: Start with a config pointing to failserver — should produce enriched error.
	failBin := e2eBuildBinary(t, "failserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	servers := config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"bad": {Local: config.MCPServerLocal{Command: failBin}},
		},
	}

	h.simulateRestart(t, servers)

	// The server should NOT be in the gateway (failed to connect)
	if h.gw.hasServer("bad") {
		t.Error("gateway should NOT have the failing server")
	}

	// Verify the pool recorded an enriched error
	known := h.pool.KnownServers()
	var found bool
	for _, info := range known {
		if info.Name == "bad" {
			found = true
			if info.LastErr == nil {
				t.Fatal("expected error for failing server, got nil")
			}
			errMsg := info.LastErr.Error()
			if !strings.Contains(errMsg, "exit code 42") {
				t.Errorf("expected exit code 42, got: %s", errMsg)
			}
			if !strings.Contains(errMsg, "config file not found") {
				t.Errorf("expected stderr content, got: %s", errMsg)
			}
		}
	}
	if !found {
		t.Error("expected 'bad' server in pool (in failed state)")
	}
}

func TestE2E_FixConfigAndRestart(t *testing.T) {
	// T24: Start with failserver, then fix to echoserver and restart.
	failBin := e2eBuildBinary(t, "failserver")
	echoBin := e2eBuildBinary(t, "echoserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	// First restart: bad config
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: failBin}},
		},
	})
	if h.gw.hasServer("srv") {
		t.Error("gateway should NOT have the server after bad config")
	}

	// Second restart: fixed config
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})

	if !h.gw.hasServer("srv") {
		t.Error("gateway should have the server after fix")
	}
	if h.gw.toolCount("srv") != 2 {
		t.Errorf("expected 2 tools (echo, fail), got %d", h.gw.toolCount("srv"))
	}
}

func TestE2E_AddNewServerWithoutWipingExisting(t *testing.T) {
	// T25: Start with one server, add a second, verify first survives.
	echoBin := e2eBuildBinary(t, "echoserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	// First restart: one server
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"alpha": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})
	if !h.gw.hasServer("alpha") {
		t.Fatal("alpha should be in gateway")
	}

	// Second restart: add a second server
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"alpha": {Local: config.MCPServerLocal{Command: echoBin}},
			"beta":  {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})

	if !h.gw.hasServer("alpha") {
		t.Error("alpha should still be in gateway after adding beta")
	}
	if !h.gw.hasServer("beta") {
		t.Error("beta should be in gateway after adding")
	}
}

func TestE2E_ChangeCommandAndRestart(t *testing.T) {
	// T26: Start with one command/args, change them, verify reconnect.
	echoBin := e2eBuildBinary(t, "echoserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	// First restart: echoserver with no args
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})
	if !h.gw.hasServer("srv") {
		t.Fatal("srv should be in gateway")
	}

	// Verify config is stored in registry
	ctx := context.Background()
	srvReg, err := h.store.GetServer(ctx, "srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	var oldCfg config.MCPServerEntry
	json.Unmarshal(srvReg.ConnectionConfig, &oldCfg)

	// Second restart: same binary, different args (config change)
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin, Args: []string{"--verbose"}}},
		},
	})

	// Verify registry has updated config
	srvReg2, _ := h.store.GetServer(ctx, "srv")
	var newCfg config.MCPServerEntry
	json.Unmarshal(srvReg2.ConnectionConfig, &newCfg)
	if len(newCfg.Local.Args) != 1 || newCfg.Local.Args[0] != "--verbose" {
		t.Errorf("expected args [--verbose], got %v", newCfg.Local.Args)
	}

	// Server should still be functional
	if !h.gw.hasServer("srv") {
		t.Error("srv should be in gateway after config change")
	}
}

func TestE2E_RemoveServerFromConfig(t *testing.T) {
	// T27: Start with server, remove from config, verify disabled in registry
	// and absent from gateway after restart.
	echoBin := e2eBuildBinary(t, "echoserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	// First restart: server present
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})
	if !h.gw.hasServer("srv") {
		t.Fatal("srv should be in gateway initially")
	}

	// Second restart: server removed from config.
	// SyncConfigServers sets proxy_enabled=false. The fresh reconciler sees
	// no proxy_enabled servers, so the gateway remains empty.
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{},
	})

	// Gateway (fresh) should NOT have the server — reconciler only adds
	// proxy_enabled=true servers, and SyncConfigServers disabled it.
	if h.gw.hasServer("srv") {
		t.Error("gateway should NOT have srv after removal")
	}

	// Registry should still have the row but with proxy_enabled=false.
	ctx := context.Background()
	srvReg, err := h.store.GetServer(ctx, "srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srvReg == nil {
		t.Fatal("server row should be preserved in registry")
	}
	if srvReg.ProxyEnabled {
		t.Error("proxy_enabled should be false after removal")
	}
}

func TestE2E_ReAddServerToConfig(t *testing.T) {
	// T28: Remove a server, then re-add it. Verify it comes back.
	echoBin := e2eBuildBinary(t, "echoserver")
	h := newE2EHarness(t)
	defer h.cleanup()

	// First restart: server present
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})
	if !h.gw.hasServer("srv") {
		t.Fatal("srv should be in gateway initially")
	}

	// Second restart: server removed — disabled in registry
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{},
	})
	if h.gw.hasServer("srv") {
		t.Fatal("gateway should NOT have srv after removal")
	}
	// Verify registry has proxy_enabled=false
	ctx := context.Background()
	srvReg, _ := h.store.GetServer(ctx, "srv")
	if srvReg.ProxyEnabled {
		t.Fatal("proxy_enabled should be false after removal")
	}

	// Third restart: server re-added — SyncConfigServers flips proxy_enabled=true
	h.simulateRestart(t, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"srv": {Local: config.MCPServerLocal{Command: echoBin}},
		},
	})

	if !h.gw.hasServer("srv") {
		t.Error("gateway should have srv after re-adding")
	}
	if h.gw.toolCount("srv") != 2 {
		t.Errorf("expected 2 tools after re-add, got %d", h.gw.toolCount("srv"))
	}

	// Registry should have proxy_enabled=true again
	srvReg2, _ := h.store.GetServer(ctx, "srv")
	if !srvReg2.ProxyEnabled {
		t.Error("proxy_enabled should be true after re-add")
	}
}
