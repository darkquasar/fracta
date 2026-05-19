package sqliteregistry

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const timeFormat = time.RFC3339

// SQLiteRegistry implements registry.Store backed by SQLite.
type SQLiteRegistry struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at the given path and ensures
// the registry schema exists. The database file is shared with the state store.
func New(ctx context.Context, dbPath string) (*SQLiteRegistry, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("sqliteregistry: creating directory: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("sqliteregistry: open: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqliteregistry: migrate: %w", err)
	}
	return &SQLiteRegistry{db: db}, nil
}

// Close closes the underlying database connection.
func (s *SQLiteRegistry) Close() error {
	return s.db.Close()
}

// ListServers returns servers matching the given filter.
func (s *SQLiteRegistry) ListServers(ctx context.Context, filter registry.ServerFilter) ([]registry.Server, error) {
	query := `SELECT name, transport_type, connection_config, secret_refs, server_capabilities,
		proxy_enabled, status, health_message, last_discovered_at, created_by, created_at, updated_at
		FROM registered_servers`
	var conditions []string
	var args []any

	if filter.Status != nil {
		conditions = append(conditions, "status = ?")
		args = append(args, string(*filter.Status))
	}
	if filter.ProxyEnabled != nil {
		conditions = append(conditions, "proxy_enabled = ?")
		if *filter.ProxyEnabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY name"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqliteregistry: list servers: %w", err)
	}
	defer rows.Close()

	var servers []registry.Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("sqliteregistry: scan server: %w", err)
		}
		servers = append(servers, srv)
	}
	return servers, rows.Err()
}

// GetServer returns a single server by name, or nil if not found.
func (s *SQLiteRegistry) GetServer(ctx context.Context, name string) (*registry.Server, error) {
	row := s.db.QueryRowContext(ctx, `SELECT name, transport_type, connection_config, secret_refs, server_capabilities,
		proxy_enabled, status, health_message, last_discovered_at, created_by, created_at, updated_at
		FROM registered_servers WHERE name = ?`, name)
	srv, err := scanServer(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqliteregistry: get server %q: %w", name, err)
	}
	return &srv, nil
}

// UpsertServer inserts or updates a server registration.
func (s *SQLiteRegistry) UpsertServer(ctx context.Context, srv registry.Server) error {
	connCfg := defaultJSON(srv.ConnectionConfig)
	secretRefs := defaultJSON(srv.SecretRefs)
	serverCaps := defaultJSON(srv.ServerCapabilities)
	now := time.Now().UTC().Format(timeFormat)

	transportType := srv.TransportType
	if transportType == "" {
		transportType = "stdio"
	}
	status := srv.Status
	if status == "" {
		status = registry.StatusPending
	}
	createdBy := srv.CreatedBy
	if createdBy == "" {
		createdBy = "api"
	}

	proxyEnabled := 1
	if !srv.ProxyEnabled {
		proxyEnabled = 0
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO registered_servers
		(name, transport_type, connection_config, secret_refs, server_capabilities,
		 proxy_enabled, status, health_message, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			transport_type = excluded.transport_type,
			connection_config = excluded.connection_config,
			secret_refs = excluded.secret_refs,
			server_capabilities = excluded.server_capabilities,
			proxy_enabled = excluded.proxy_enabled,
			status = excluded.status,
			health_message = excluded.health_message,
			updated_at = excluded.updated_at`,
		srv.Name, transportType, string(connCfg), string(secretRefs), string(serverCaps),
		proxyEnabled, string(status), srv.HealthMessage, createdBy, now, now)
	if err != nil {
		return fmt.Errorf("sqliteregistry: upsert server %q: %w", srv.Name, err)
	}
	return nil
}

// DeleteServer removes a server and its tools (CASCADE).
func (s *SQLiteRegistry) DeleteServer(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM registered_servers WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("sqliteregistry: delete server %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqliteregistry: server %q not found", name)
	}
	return nil
}

// UpdateServerHealth updates status, health message, and optionally last_discovered_at.
func (s *SQLiteRegistry) UpdateServerHealth(ctx context.Context, name string, status registry.ServerStatus, healthMessage string, discoveredAt *time.Time) error {
	now := time.Now().UTC().Format(timeFormat)
	var err error
	if discoveredAt != nil {
		disc := discoveredAt.UTC().Format(timeFormat)
		_, err = s.db.ExecContext(ctx, `UPDATE registered_servers
			SET status = ?, health_message = ?, last_discovered_at = ?, updated_at = ?
			WHERE name = ?`, string(status), healthMessage, disc, now, name)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE registered_servers
			SET status = ?, health_message = ?, updated_at = ?
			WHERE name = ?`, string(status), healthMessage, now, name)
	}
	if err != nil {
		return fmt.Errorf("sqliteregistry: update health %q: %w", name, err)
	}
	return nil
}

// ReplaceDiscoveredTools upserts discovered tools for a server, preserving
// the operator-controlled `enabled` column. Stale tools (no longer in the
// discovered set) are deleted.
func (s *SQLiteRegistry) ReplaceDiscoveredTools(ctx context.Context, server string, tools []registry.Tool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqliteregistry: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(timeFormat)

	for _, t := range tools {
		inputSchema := ""
		if t.InputSchema != nil {
			inputSchema = string(t.InputSchema)
		}
		metadata := defaultJSON(t.Metadata)
		_, err := tx.ExecContext(ctx, `INSERT INTO registered_tools
			(server_name, tool_name, description, input_schema, schema_hash, enabled, last_seen_at, metadata)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT(server_name, tool_name) DO UPDATE SET
				description = excluded.description,
				input_schema = excluded.input_schema,
				schema_hash = excluded.schema_hash,
				last_seen_at = excluded.last_seen_at,
				metadata = excluded.metadata`,
			server, t.ToolName, t.Description, inputSchema, t.SchemaHash, now, string(metadata))
		if err != nil {
			return fmt.Errorf("sqliteregistry: upsert tool %q/%q: %w", server, t.ToolName, err)
		}
	}

	if len(tools) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM registered_tools WHERE server_name = ?`, server); err != nil {
			return fmt.Errorf("sqliteregistry: clear all tools for %q: %w", server, err)
		}
	} else {
		placeholders := make([]string, len(tools))
		args := make([]any, 0, len(tools)+1)
		args = append(args, server)
		for i, t := range tools {
			placeholders[i] = "?"
			args = append(args, t.ToolName)
		}
		query := fmt.Sprintf(`DELETE FROM registered_tools WHERE server_name = ? AND tool_name NOT IN (%s)`,
			strings.Join(placeholders, ","))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("sqliteregistry: delete stale tools for %q: %w", server, err)
		}
	}

	return tx.Commit()
}

// ListTools returns tools matching the given filter.
func (s *SQLiteRegistry) ListTools(ctx context.Context, filter registry.ToolFilter) ([]registry.Tool, error) {
	query := `SELECT server_name, tool_name, description, input_schema, schema_hash,
		enabled, last_seen_at, metadata FROM registered_tools`
	var conditions []string
	var args []any

	if filter.ServerName != "" {
		conditions = append(conditions, "server_name = ?")
		args = append(args, filter.ServerName)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		if *filter.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY server_name, tool_name"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqliteregistry: list tools: %w", err)
	}
	defer rows.Close()

	var tools []registry.Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, fmt.Errorf("sqliteregistry: scan tool: %w", err)
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// SetToolEnabled enables or disables a specific tool.
func (s *SQLiteRegistry) SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE registered_tools SET enabled = ? WHERE server_name = ? AND tool_name = ?`,
		val, server, tool)
	if err != nil {
		return fmt.Errorf("sqliteregistry: set tool enabled %q/%q: %w", server, tool, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqliteregistry: tool %q/%q not found", server, tool)
	}
	return nil
}

// LogAudit inserts an audit log entry.
func (s *SQLiteRegistry) LogAudit(ctx context.Context, entry registry.AuditEntry) error {
	detail := defaultJSON(entry.Detail)
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log
		(actor, actor_type, action, resource_type, resource_name, detail, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.Actor, entry.ActorType, entry.Action, entry.ResourceType, entry.ResourceName,
		string(detail), ts.Format(timeFormat))
	if err != nil {
		return fmt.Errorf("sqliteregistry: log audit: %w", err)
	}
	return nil
}

// ListAuditLog returns audit entries matching the given filter.
func (s *SQLiteRegistry) ListAuditLog(ctx context.Context, filter registry.AuditFilter) ([]registry.AuditEntry, error) {
	query := `SELECT id, actor, actor_type, action, resource_type, resource_name, detail, timestamp
		FROM audit_log`
	var conditions []string
	var args []any

	if filter.Actor != "" {
		conditions = append(conditions, "actor = ?")
		args = append(args, filter.Actor)
	}
	if filter.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, filter.Action)
	}
	if filter.ResourceType != "" {
		conditions = append(conditions, "resource_type = ?")
		args = append(args, filter.ResourceType)
	}
	if filter.ResourceName != "" {
		conditions = append(conditions, "resource_name = ?")
		args = append(args, filter.ResourceName)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY timestamp DESC"

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqliteregistry: list audit: %w", err)
	}
	defer rows.Close()

	var entries []registry.AuditEntry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("sqliteregistry: scan audit: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// SyncConfigServers performs a per-server config-aware upsert at startup.
// Only touches rows with created_by='config'. API-managed servers are never modified.
func (s *SQLiteRegistry) SyncConfigServers(ctx context.Context, servers config.MCPServersConfig) error {
	now := time.Now().UTC().Format(timeFormat)

	// Step 1: Upsert present servers with proxy_enabled=true.
	for name, entry := range servers.Servers {
		connCfg, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("sqliteregistry: marshal config for %q: %w", name, err)
		}
		transportType := registry.RegistryTransportType(entry)
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO registered_servers
				(name, transport_type, connection_config, proxy_enabled, status, created_by, created_at, updated_at)
			VALUES (?, ?, ?, 1, 'pending', 'config', ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				transport_type = excluded.transport_type,
				connection_config = excluded.connection_config,
				proxy_enabled = 1,
				updated_at = excluded.updated_at
			WHERE registered_servers.created_by = 'config'
			  AND (registered_servers.connection_config != excluded.connection_config
			       OR registered_servers.transport_type != excluded.transport_type
			       OR registered_servers.proxy_enabled = 0)`,
			name, transportType, string(connCfg), now, now)
		if err != nil {
			return fmt.Errorf("sqliteregistry: sync upsert %q: %w", name, err)
		}
	}

	// Step 2: Disable config-owned servers absent from current config.
	if len(servers.Servers) == 0 {
		// Empty config: disable ALL config-owned rows.
		_, err := s.db.ExecContext(ctx, `
			UPDATE registered_servers SET proxy_enabled = 0, updated_at = ?
			WHERE created_by = 'config' AND proxy_enabled = 1`, now)
		if err != nil {
			return fmt.Errorf("sqliteregistry: sync disable all: %w", err)
		}
	} else {
		names := make([]any, 0, len(servers.Servers)+1)
		names = append(names, now) // first arg is updated_at
		placeholders := make([]string, 0, len(servers.Servers))
		for name := range servers.Servers {
			placeholders = append(placeholders, "?")
			names = append(names, name)
		}
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			UPDATE registered_servers SET proxy_enabled = 0, updated_at = ?
			WHERE created_by = 'config' AND proxy_enabled = 1
			  AND name NOT IN (%s)`, strings.Join(placeholders, ",")),
			names...)
		if err != nil {
			return fmt.Errorf("sqliteregistry: sync disable absent: %w", err)
		}
	}

	return nil
}

// scanServer scans a row into a Server struct.
func scanServer(scanner interface{ Scan(...any) error }) (registry.Server, error) {
	var srv registry.Server
	var proxyEnabled int
	var connCfg, secretRefs, serverCaps, status string
	var lastDiscovered sql.NullString
	var createdAt, updatedAt string

	err := scanner.Scan(&srv.Name, &srv.TransportType, &connCfg, &secretRefs, &serverCaps,
		&proxyEnabled, &status, &srv.HealthMessage, &lastDiscovered, &srv.CreatedBy, &createdAt, &updatedAt)
	if err != nil {
		return srv, err
	}

	srv.ConnectionConfig = json.RawMessage(connCfg)
	srv.SecretRefs = json.RawMessage(secretRefs)
	srv.ServerCapabilities = json.RawMessage(serverCaps)
	srv.ProxyEnabled = proxyEnabled != 0
	srv.Status = registry.ServerStatus(status)
	if lastDiscovered.Valid && lastDiscovered.String != "" {
		t, _ := time.Parse(timeFormat, lastDiscovered.String)
		srv.LastDiscoveredAt = &t
	}
	srv.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	srv.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return srv, nil
}

// scanTool scans a row into a Tool struct.
func scanTool(scanner interface{ Scan(...any) error }) (registry.Tool, error) {
	var t registry.Tool
	var enabled int
	var inputSchema sql.NullString
	var metadata, lastSeenAt string

	err := scanner.Scan(&t.ServerName, &t.ToolName, &t.Description, &inputSchema,
		&t.SchemaHash, &enabled, &lastSeenAt, &metadata)
	if err != nil {
		return t, err
	}

	if inputSchema.Valid && inputSchema.String != "" {
		t.InputSchema = json.RawMessage(inputSchema.String)
	}
	t.Enabled = enabled != 0
	t.Metadata = json.RawMessage(metadata)
	t.LastSeenAt, _ = time.Parse(timeFormat, lastSeenAt)
	return t, nil
}

// scanAudit scans a row into an AuditEntry struct.
func scanAudit(scanner interface{ Scan(...any) error }) (registry.AuditEntry, error) {
	var e registry.AuditEntry
	var detail, ts string

	err := scanner.Scan(&e.ID, &e.Actor, &e.ActorType, &e.Action, &e.ResourceType,
		&e.ResourceName, &detail, &ts)
	if err != nil {
		return e, err
	}

	e.Detail = json.RawMessage(detail)
	e.Timestamp, _ = time.Parse(timeFormat, ts)
	return e, nil
}

// defaultJSON returns the input if non-nil, or []byte("{}") otherwise.
func defaultJSON(data json.RawMessage) json.RawMessage {
	if data == nil {
		return json.RawMessage("{}")
	}
	return data
}

// Verify interface compliance at compile time.
var _ registry.Store = (*SQLiteRegistry)(nil) 
