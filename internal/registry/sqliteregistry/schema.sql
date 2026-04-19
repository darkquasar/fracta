CREATE TABLE IF NOT EXISTS registered_servers (
    name                TEXT PRIMARY KEY,
    transport_type      TEXT NOT NULL DEFAULT 'stdio'
                        CHECK (transport_type IN ('stdio', 'http', 'streamable_http')),
    connection_config   TEXT NOT NULL DEFAULT '{}',
    secret_refs         TEXT NOT NULL DEFAULT '{}',
    server_capabilities TEXT NOT NULL DEFAULT '{}',
    proxy_enabled       INTEGER NOT NULL DEFAULT 1,
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'active', 'degraded', 'paused', 'error', 'auth_failed', 'unreachable')),
    health_message      TEXT NOT NULL DEFAULT '',
    last_discovered_at  TEXT,
    created_by          TEXT NOT NULL DEFAULT 'config',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS registered_tools (
    server_name     TEXT NOT NULL REFERENCES registered_servers(name) ON DELETE CASCADE,
    tool_name       TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    input_schema    TEXT,
    schema_hash     TEXT NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_seen_at    TEXT NOT NULL DEFAULT (datetime('now')),
    metadata        TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (server_name, tool_name)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    actor           TEXT NOT NULL,
    actor_type      TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_name   TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '{}',
    timestamp       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log (timestamp DESC);
