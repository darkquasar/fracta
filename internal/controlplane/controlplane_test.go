package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry/sqliteregistry"
)

func TestNewControlPlane_LocalBackend(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Path: t.TempDir() + "/state.db",
			},
		},
		Reaper: config.ReaperConfig{
			MaxAge:        dur(1 * time.Hour),
			Interval:      dur(30 * time.Second),
			MaxConcurrent: 5,
		},
	}

	cp, err := NewControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer cp.Close()

	if cp.Backend == nil {
		t.Error("Backend should not be nil")
	}
	if cp.Store == nil {
		t.Error("Store should not be nil")
	}
	if cp.Mailbox == nil {
		t.Error("Mailbox should not be nil")
	}
	if cp.Reaper == nil {
		t.Error("Reaper should not be nil")
	}
}

func TestNewControlPlane_DefaultBackend(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			State: config.StateConfig{
				Path: t.TempDir() + "/state.db",
			},
		},
		Reaper: config.ReaperConfig{
			Interval: dur(time.Hour),
		},
	}

	cp, err := NewControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewControlPlane with empty backend: %v", err)
	}
	defer cp.Close()
}

func TestNewControlPlane_UnknownBackend(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "quantum",
		},
	}

	_, err := NewControlPlane(cfg, "")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestNewControlPlane_SQLiteStore(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Path: dir + "/test.db",
			},
		},
		Reaper: config.ReaperConfig{
			Interval: dur(time.Hour),
		},
	}

	cp, err := NewControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewControlPlane with sqlite: %v", err)
	}
	defer cp.Close()

	if cp.Store == nil {
		t.Error("Store should not be nil")
	}
}

func TestControlPlane_Reconfigure(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Path: t.TempDir() + "/state.db",
			},
		},
		Reaper: config.ReaperConfig{
			MaxAge:        dur(1 * time.Hour),
			Interval:      dur(time.Hour),
			MaxConcurrent: 5,
		},
	}

	cp, err := NewControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer cp.Close()

	newCfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Path: t.TempDir() + "/state.db",
			},
		},
		Reaper: config.ReaperConfig{
			MaxAge:        dur(30 * time.Minute),
			Interval:      dur(15 * time.Second),
			MaxConcurrent: 10,
		},
	}

	if err := cp.Reconfigure(newCfg); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}

	// Verify the reaper picked up the new config
	cp.Reaper.mu.RLock()
	got := cp.Reaper.cfg.MaxConcurrent
	cp.Reaper.mu.RUnlock()

	if got != 10 {
		t.Errorf("expected MaxConcurrent 10 after reconfigure, got %d", got)
	}
}

// --- Registry bootstrap ownership tests ---

func TestNewControlPlane_DoesNotBootstrapRegistry(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"

	// Pre-seed the registry with a config-owned server (simulates gateway bootstrap).
	ctx := context.Background()
	regStore, err := sqliteregistry.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqliteregistry.New: %v", err)
	}
	err = regStore.SyncConfigServers(ctx, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"gateway-srv": {Local: config.MCPServerLocal{Command: "some-mcp"}},
		},
	})
	if err != nil {
		t.Fatalf("SyncConfigServers seed: %v", err)
	}
	regStore.Close()

	// Create a NewControlPlane (non-gateway) with empty MCPServers.
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State:   config.StateConfig{Path: dbPath},
		},
		Reaper: config.ReaperConfig{Interval: dur(time.Hour)},
	}
	cp, err := NewControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer cp.Close()

	// The pre-seeded server must NOT have been disabled.
	srv, err := cp.RegistryStore.GetServer(ctx, "gateway-srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected gateway-srv to exist in registry")
	}
	if !srv.ProxyEnabled {
		t.Error("NewControlPlane (non-gateway) must not disable config-owned registry rows")
	}
}

func TestNewGatewayControlPlane_BootstrapsRegistry(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"

	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State:   config.StateConfig{Path: dbPath},
		},
		MCPServers: config.MCPServersConfig{
			Servers: map[string]config.MCPServerEntry{
				"gw-srv": {Local: config.MCPServerLocal{Command: "gw-mcp"}},
			},
		},
	}
	cp, err := NewGatewayControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewGatewayControlPlane: %v", err)
	}
	defer cp.Close()

	ctx := context.Background()
	srv, err := cp.RegistryStore.GetServer(ctx, "gw-srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected gw-srv to exist in registry after gateway bootstrap")
	}
	if !srv.ProxyEnabled {
		t.Error("gateway bootstrap should enable config server")
	}
	if srv.CreatedBy != "config" {
		t.Errorf("CreatedBy = %q, want config", srv.CreatedBy)
	}
}

func TestNewGatewayControlPlane_EmptyConfigDisablesAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"

	// Pre-seed a config-owned server.
	ctx := context.Background()
	regStore, err := sqliteregistry.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqliteregistry.New: %v", err)
	}
	err = regStore.SyncConfigServers(ctx, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"old-srv": {Local: config.MCPServerLocal{Command: "old-mcp"}},
		},
	})
	if err != nil {
		t.Fatalf("SyncConfigServers seed: %v", err)
	}
	regStore.Close()

	// Gateway with empty config — should disable previously-registered servers.
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State:   config.StateConfig{Path: dbPath},
		},
	}
	cp, err := NewGatewayControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewGatewayControlPlane: %v", err)
	}
	defer cp.Close()

	srv, err := cp.RegistryStore.GetServer(ctx, "old-srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected old-srv to still exist (row preserved)")
	}
	if srv.ProxyEnabled {
		t.Error("gateway with empty config should disable config-owned servers")
	}
}

func TestNewGatewayControlPlane_BootstrapOptOut(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"

	// Pre-seed a config-owned server.
	ctx := context.Background()
	regStore, err := sqliteregistry.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqliteregistry.New: %v", err)
	}
	err = regStore.SyncConfigServers(ctx, config.MCPServersConfig{
		Servers: map[string]config.MCPServerEntry{
			"existing-srv": {Local: config.MCPServerLocal{Command: "existing-mcp"}},
		},
	})
	if err != nil {
		t.Fatalf("SyncConfigServers seed: %v", err)
	}
	regStore.Close()

	// Gateway with bootstrap_from_config=false — should NOT mutate registry.
	// Driver must be set so the registry block is considered "explicitly present"
	// (profile.go only reads BootstrapFromConfig when cfg.Registry is non-zero).
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State:   config.StateConfig{Path: dbPath},
		},
		Registry: config.RegistryConfig{
			Driver:              "sqlite",
			BootstrapFromConfig: false,
		},
	}
	cp, err := NewGatewayControlPlane(cfg, "")
	if err != nil {
		t.Fatalf("NewGatewayControlPlane: %v", err)
	}
	defer cp.Close()

	srv, err := cp.RegistryStore.GetServer(ctx, "existing-srv")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv == nil {
		t.Fatal("expected existing-srv to still exist")
	}
	if !srv.ProxyEnabled {
		t.Error("gateway with bootstrap_from_config=false must not disable existing registry rows")
	}
}

func TestResolveProfile_RegistryPostgresOverride(t *testing.T) {
	// When state driver is SQLite but registry.driver is postgres with its own DSN,
	// the registry should get the explicit Postgres config.
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Driver: "sqlite",
				SQLite: config.SQLiteConfig{Path: "/tmp/state.db"},
			},
		},
		Registry: config.RegistryConfig{
			Driver: "postgres",
			Postgres: config.PostgresConfig{
				DSN: "postgres://registry:secret@db:5432/registry",
			},
		},
	}

	p := ResolveProfile(cfg, "")
	if p.StateDriver != "sqlite" {
		t.Errorf("StateDriver = %q, want sqlite", p.StateDriver)
	}
	if p.RegistryDriver != "postgres" {
		t.Errorf("RegistryDriver = %q, want postgres", p.RegistryDriver)
	}
	if p.RegistryPostgresConfig.DSN != "postgres://registry:secret@db:5432/registry" {
		t.Errorf("RegistryPostgresConfig.DSN = %q, want explicit registry DSN", p.RegistryPostgresConfig.DSN)
	}
}

func TestResolveProfile_RegistryPostgresFallsBackToState(t *testing.T) {
	// When registry.driver is postgres but registry.postgres is empty,
	// it should fall back to runtime.state.postgres.
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Driver: "postgres",
				Postgres: config.PostgresConfig{
					DSN: "postgres://state:secret@db:5432/fracta",
				},
			},
		},
		Registry: config.RegistryConfig{
			Driver: "postgres",
			// No explicit postgres config — should inherit from state.
		},
	}

	p := ResolveProfile(cfg, "")
	if p.RegistryDriver != "postgres" {
		t.Errorf("RegistryDriver = %q, want postgres", p.RegistryDriver)
	}
	if p.RegistryPostgresConfig.DSN != "postgres://state:secret@db:5432/fracta" {
		t.Errorf("RegistryPostgresConfig.DSN = %q, want inherited state DSN", p.RegistryPostgresConfig.DSN)
	}
}

func TestResolveProfile_RegistryPostgresEmptyDSNOnSQLiteState(t *testing.T) {
	// When state is SQLite and registry.driver is postgres but no DSN is set anywhere,
	// RegistryPostgresConfig.DSN should be empty — buildRegistryStore will catch this.
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Backend: "local",
			State: config.StateConfig{
				Driver: "sqlite",
			},
		},
		Registry: config.RegistryConfig{
			Driver: "postgres",
			// No postgres config anywhere.
		},
	}

	p := ResolveProfile(cfg, "")
	if p.RegistryPostgresConfig.DSN != "" {
		t.Errorf("RegistryPostgresConfig.DSN = %q, want empty", p.RegistryPostgresConfig.DSN)
	}
}
