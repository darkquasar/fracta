package pgregistry

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// PgRegistry implements registry.Store backed by PostgreSQL via pgx v5.
type PgRegistry struct {
	pool *pgxpool.Pool
}

// New creates a PgRegistry with its own connection pool, pings the database,
// and runs schema migration. Uses the same DSN as pgstore but a separate pool.
func New(ctx context.Context, dsn string) (*PgRegistry, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgregistry: parse config: %w", err)
	}

	poolCfg.MaxConns = 10
	poolCfg.MinConns = 1
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgregistry: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgregistry: ping: %w", err)
	}

	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgregistry: migrate: %w", err)
	}

	return &PgRegistry{pool: pool}, nil
}

// Close releases the connection pool.
func (r *PgRegistry) Close() error {
	r.pool.Close()
	return nil
}

// ListServers returns servers matching the given filter.
func (r *PgRegistry) ListServers(ctx context.Context, filter registry.ServerFilter) ([]registry.Server, error) {
	query := `SELECT name, transport_type, connection_config, secret_refs, server_capabilities,
		proxy_enabled, status, health_message, last_discovered_at, created_by, created_at, updated_at
		FROM registered_servers`
	var conditions []string
	var args []any
	argIdx := 1

	if filter.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(*filter.Status))
		argIdx++
	}
	if filter.ProxyEnabled != nil {
		conditions = append(conditions, fmt.Sprintf("proxy_enabled = $%d", argIdx))
		args = append(args, *filter.ProxyEnabled)
		argIdx++
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY name"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgregistry: list servers: %w", err)
	}
	defer rows.Close()

	var servers []registry.Server
	for rows.Next() {
		srv, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("pgregistry: scan server: %w", err)
		}
		servers = append(servers, srv)
	}
	return servers, rows.Err()
}

// GetServer returns a single server by name, or nil if not found.
func (r *PgRegistry) GetServer(ctx context.Context, name string) (*registry.Server, error) {
	row := r.pool.QueryRow(ctx, `SELECT name, transport_type, connection_config, secret_refs, server_capabilities,
		proxy_enabled, status, health_message, last_discovered_at, created_by, created_at, updated_at
		FROM registered_servers WHERE name = $1`, name)
	srv, err := scanServer(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pgregistry: get server %q: %w", name, err)
	}
	return &srv, nil
}

// UpsertServer inserts or updates a server registration.
func (r *PgRegistry) UpsertServer(ctx context.Context, srv registry.Server) error {
	connCfg := defaultJSON(srv.ConnectionConfig)
	secretRefs := defaultJSON(srv.SecretRefs)
	serverCaps := defaultJSON(srv.ServerCapabilities)
	now := time.Now().UTC()

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

	_, err := r.pool.Exec(ctx, `INSERT INTO registered_servers
		(name, transport_type, connection_config, secret_refs, server_capabilities,
		 proxy_enabled, status, health_message, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT(name) DO UPDATE SET
			transport_type = EXCLUDED.transport_type,
			connection_config = EXCLUDED.connection_config,
			secret_refs = EXCLUDED.secret_refs,
			server_capabilities = EXCLUDED.server_capabilities,
			proxy_enabled = EXCLUDED.proxy_enabled,
			status = EXCLUDED.status,
			health_message = EXCLUDED.health_message,
			updated_at = EXCLUDED.updated_at`,
		srv.Name, transportType, connCfg, secretRefs, serverCaps,
		srv.ProxyEnabled, string(status), srv.HealthMessage, createdBy, now, now)
	if err != nil {
		return fmt.Errorf("pgregistry: upsert server %q: %w", srv.Name, err)
	}
	return nil
}

// DeleteServer removes a server and its tools (CASCADE).
func (r *PgRegistry) DeleteServer(ctx context.Context, name string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM registered_servers WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("pgregistry: delete server %q: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgregistry: server %q not found", name)
	}
	return nil
}

// UpdateServerHealth updates status, health message, and optionally last_discovered_at.
func (r *PgRegistry) UpdateServerHealth(ctx context.Context, name string, status registry.ServerStatus, healthMessage string, discoveredAt *time.Time) error {
	now := time.Now().UTC()
	var err error
	if discoveredAt != nil {
		_, err = r.pool.Exec(ctx, `UPDATE registered_servers
			SET status = $1, health_message = $2, last_discovered_at = $3, updated_at = $4
			WHERE name = $5`, string(status), healthMessage, discoveredAt.UTC(), now, name)
	} else {
		_, err = r.pool.Exec(ctx, `UPDATE registered_servers
			SET status = $1, health_message = $2, updated_at = $3
			WHERE name = $4`, string(status), healthMessage, now, name)
	}
	if err != nil {
		return fmt.Errorf("pgregistry: update health %q: %w", name, err)
	}
	return nil
}

// ReplaceDiscoveredTools upserts discovered tools for a server, preserving
// the operator-controlled `enabled` column. Stale tools (no longer in the
// discovered set) are deleted.
func (r *PgRegistry) ReplaceDiscoveredTools(ctx context.Context, server string, tools []registry.Tool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgregistry: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()

	for _, t := range tools {
		metadata := defaultJSON(t.Metadata)
		_, err := tx.Exec(ctx, `INSERT INTO registered_tools
			(server_name, tool_name, description, input_schema, schema_hash, enabled, last_seen_at, metadata)
			VALUES ($1, $2, $3, $4, $5, true, $6, $7)
			ON CONFLICT(server_name, tool_name) DO UPDATE SET
				description = EXCLUDED.description,
				input_schema = EXCLUDED.input_schema,
				schema_hash = EXCLUDED.schema_hash,
				last_seen_at = EXCLUDED.last_seen_at,
				metadata = EXCLUDED.metadata`,
			server, t.ToolName, t.Description, t.InputSchema, t.SchemaHash, now, metadata)
		if err != nil {
			return fmt.Errorf("pgregistry: upsert tool %q/%q: %w", server, t.ToolName, err)
		}
	}

	if len(tools) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM registered_tools WHERE server_name = $1`, server); err != nil {
			return fmt.Errorf("pgregistry: clear all tools for %q: %w", server, err)
		}
	} else {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.ToolName
		}
		if _, err := tx.Exec(ctx, `DELETE FROM registered_tools WHERE server_name = $1 AND tool_name != ALL($2::text[])`,
			server, names); err != nil {
			return fmt.Errorf("pgregistry: delete stale tools for %q: %w", server, err)
		}
	}

	return tx.Commit(ctx)
}

// ListTools returns tools matching the given filter.
func (r *PgRegistry) ListTools(ctx context.Context, filter registry.ToolFilter) ([]registry.Tool, error) {
	query := `SELECT server_name, tool_name, description, input_schema, schema_hash,
		enabled, last_seen_at, metadata FROM registered_tools`
	var conditions []string
	var args []any
	argIdx := 1

	if filter.ServerName != "" {
		conditions = append(conditions, fmt.Sprintf("server_name = $%d", argIdx))
		args = append(args, filter.ServerName)
		argIdx++
	}
	if filter.Enabled != nil {
		conditions = append(conditions, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, *filter.Enabled)
		argIdx++
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY server_name, tool_name"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgregistry: list tools: %w", err)
	}
	defer rows.Close()

	var tools []registry.Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, fmt.Errorf("pgregistry: scan tool: %w", err)
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// SetToolEnabled enables or disables a specific tool.
func (r *PgRegistry) SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE registered_tools SET enabled = $1 WHERE server_name = $2 AND tool_name = $3`,
		enabled, server, tool)
	if err != nil {
		return fmt.Errorf("pgregistry: set tool enabled %q/%q: %w", server, tool, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pgregistry: tool %q/%q not found", server, tool)
	}
	return nil
}

// LogAudit inserts an audit log entry.
func (r *PgRegistry) LogAudit(ctx context.Context, entry registry.AuditEntry) error {
	detail := defaultJSON(entry.Detail)
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO audit_log
		(actor, actor_type, action, resource_type, resource_name, detail, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		entry.Actor, entry.ActorType, entry.Action, entry.ResourceType, entry.ResourceName,
		detail, ts)
	if err != nil {
		return fmt.Errorf("pgregistry: log audit: %w", err)
	}
	return nil
}

// ListAuditLog returns audit entries matching the given filter.
func (r *PgRegistry) ListAuditLog(ctx context.Context, filter registry.AuditFilter) ([]registry.AuditEntry, error) {
	query := `SELECT id, actor, actor_type, action, resource_type, resource_name, detail, timestamp
		FROM audit_log`
	var conditions []string
	var args []any
	argIdx := 1

	if filter.Actor != "" {
		conditions = append(conditions, fmt.Sprintf("actor = $%d", argIdx))
		args = append(args, filter.Actor)
		argIdx++
	}
	if filter.Action != "" {
		conditions = append(conditions, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, filter.Action)
		argIdx++
	}
	if filter.ResourceType != "" {
		conditions = append(conditions, fmt.Sprintf("resource_type = $%d", argIdx))
		args = append(args, filter.ResourceType)
		argIdx++
	}
	if filter.ResourceName != "" {
		conditions = append(conditions, fmt.Sprintf("resource_name = $%d", argIdx))
		args = append(args, filter.ResourceName)
		argIdx++
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

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pgregistry: list audit: %w", err)
	}
	defer rows.Close()

	var entries []registry.AuditEntry
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("pgregistry: scan audit: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// SyncConfigServers performs a per-server config-aware upsert at startup.
// Only touches rows with created_by='config'. API-managed servers are never modified.
func (r *PgRegistry) SyncConfigServers(ctx context.Context, servers config.MCPServersConfig) error {
	now := time.Now().UTC()

	// Step 1: Upsert present servers with proxy_enabled=true.
	for name, entry := range servers.Servers {
		connCfg, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("pgregistry: marshal config for %q: %w", name, err)
		}
		transportType := registry.RegistryTransportType(entry)
		_, err = r.pool.Exec(ctx, `
			INSERT INTO registered_servers
				(name, transport_type, connection_config, proxy_enabled, status, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, true, 'pending', 'config', $4, $5)
			ON CONFLICT(name) DO UPDATE SET
				transport_type = EXCLUDED.transport_type,
				connection_config = EXCLUDED.connection_config,
				proxy_enabled = true,
				updated_at = EXCLUDED.updated_at
			WHERE registered_servers.created_by = 'config'
			  AND (registered_servers.connection_config != EXCLUDED.connection_config
			       OR registered_servers.transport_type != EXCLUDED.transport_type
			       OR registered_servers.proxy_enabled = false)`,
			name, transportType, connCfg, now, now)
		if err != nil {
			return fmt.Errorf("pgregistry: sync upsert %q: %w", name, err)
		}
	}

	// Step 2: Disable config-owned servers absent from current config.
	if len(servers.Servers) == 0 {
		// Empty config: disable ALL config-owned rows.
		_, err := r.pool.Exec(ctx, `
			UPDATE registered_servers SET proxy_enabled = false, updated_at = $1
			WHERE created_by = 'config' AND proxy_enabled = true`, now)
		if err != nil {
			return fmt.Errorf("pgregistry: sync disable all: %w", err)
		}
	} else {
		names := make([]string, 0, len(servers.Servers))
		for name := range servers.Servers {
			names = append(names, name)
		}
		_, err := r.pool.Exec(ctx, `
			UPDATE registered_servers SET proxy_enabled = false, updated_at = $1
			WHERE created_by = 'config' AND proxy_enabled = true
			  AND name != ALL($2::text[])`,
			now, names)
		if err != nil {
			return fmt.Errorf("pgregistry: sync disable absent: %w", err)
		}
	}

	return nil
}

// scanServer scans a row into a Server struct.
func scanServer(scanner interface{ Scan(...any) error }) (registry.Server, error) {
	var srv registry.Server
	var connCfg, secretRefs, serverCaps []byte
	var status string
	var lastDiscovered *time.Time

	err := scanner.Scan(&srv.Name, &srv.TransportType, &connCfg, &secretRefs, &serverCaps,
		&srv.ProxyEnabled, &status, &srv.HealthMessage, &lastDiscovered, &srv.CreatedBy, &srv.CreatedAt, &srv.UpdatedAt)
	if err != nil {
		return srv, err
	}

	srv.ConnectionConfig = json.RawMessage(connCfg)
	srv.SecretRefs = json.RawMessage(secretRefs)
	srv.ServerCapabilities = json.RawMessage(serverCaps)
	srv.Status = registry.ServerStatus(status)
	srv.LastDiscoveredAt = lastDiscovered
	return srv, nil
}

// scanTool scans a row into a Tool struct.
func scanTool(scanner interface{ Scan(...any) error }) (registry.Tool, error) {
	var t registry.Tool
	var inputSchema []byte
	var metadata []byte

	err := scanner.Scan(&t.ServerName, &t.ToolName, &t.Description, &inputSchema,
		&t.SchemaHash, &t.Enabled, &t.LastSeenAt, &metadata)
	if err != nil {
		return t, err
	}

	if inputSchema != nil {
		t.InputSchema = json.RawMessage(inputSchema)
	}
	t.Metadata = json.RawMessage(metadata)
	return t, nil
}

// scanAudit scans a row into an AuditEntry struct.
func scanAudit(scanner interface{ Scan(...any) error }) (registry.AuditEntry, error) {
	var e registry.AuditEntry
	var detail []byte

	err := scanner.Scan(&e.ID, &e.Actor, &e.ActorType, &e.Action, &e.ResourceType,
		&e.ResourceName, &detail, &e.Timestamp)
	if err != nil {
		return e, err
	}

	e.Detail = json.RawMessage(detail)
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
var _ registry.Store = (*PgRegistry)(nil)
