CREATE TABLE IF NOT EXISTS registered_servers (
    name                TEXT PRIMARY KEY,
    transport_type      TEXT NOT NULL DEFAULT 'stdio'
                        CHECK (transport_type IN ('stdio', 'http', 'streamable_http')),
    connection_config   JSONB NOT NULL DEFAULT '{}',
    secret_refs         JSONB NOT NULL DEFAULT '{}',
    server_capabilities JSONB NOT NULL DEFAULT '{}',
    proxy_enabled       BOOLEAN NOT NULL DEFAULT true,
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'active', 'degraded', 'paused', 'error', 'auth_failed', 'unreachable')),
    health_message      TEXT NOT NULL DEFAULT '',
    last_discovered_at  TIMESTAMPTZ,
    created_by          TEXT NOT NULL DEFAULT 'config',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS registered_tools (
    server_name     TEXT NOT NULL REFERENCES registered_servers(name) ON DELETE CASCADE,
    tool_name       TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    input_schema    JSONB,
    schema_hash     TEXT NOT NULL DEFAULT '',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata        JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (server_name, tool_name)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id              BIGSERIAL PRIMARY KEY,
    actor           TEXT NOT NULL,
    actor_type      TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_name   TEXT NOT NULL DEFAULT '',
    detail          JSONB NOT NULL DEFAULT '{}',
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log (timestamp DESC);
