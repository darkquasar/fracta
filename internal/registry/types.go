package registry

import (
	"encoding/json"
	"time"
)

// ServerStatus represents the health state of a registered MCP server.
type ServerStatus string

const (
	StatusPending     ServerStatus = "pending"
	StatusActive      ServerStatus = "active"
	StatusDegraded    ServerStatus = "degraded"
	StatusPaused      ServerStatus = "paused"
	StatusError       ServerStatus = "error"
	StatusAuthFailed  ServerStatus = "auth_failed"
	StatusUnreachable ServerStatus = "unreachable"
)

// Server represents a registered MCP server (maps to registered_servers table).
type Server struct {
	Name               string            `json:"name"`
	TransportType      string            `json:"transport_type"`      // stdio, http, streamable_http
	ConnectionConfig   json.RawMessage   `json:"connection_config"`   // arbitrary JSON
	SecretRefs         json.RawMessage   `json:"secret_refs"`         // arbitrary JSON
	ServerCapabilities json.RawMessage   `json:"server_capabilities"` // arbitrary JSON
	ProxyEnabled       bool              `json:"proxy_enabled"`
	Status             ServerStatus      `json:"status"`
	HealthMessage      string            `json:"health_message"`
	LastDiscoveredAt   *time.Time        `json:"last_discovered_at,omitempty"`
	CreatedBy          string            `json:"created_by"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// Tool represents a discovered tool on a registered server (maps to registered_tools table).
type Tool struct {
	ServerName  string          `json:"server_name"`
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	SchemaHash  string          `json:"schema_hash"`
	Enabled     bool            `json:"enabled"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	Metadata    json.RawMessage `json:"metadata"`
}

// AuditEntry represents a single audit log record (maps to audit_log table).
type AuditEntry struct {
	ID           int64           `json:"id"`
	Actor        string          `json:"actor"`
	ActorType    string          `json:"actor_type"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceName string          `json:"resource_name"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
}

// ServerFilter controls which servers are returned by ListServers.
type ServerFilter struct {
	Status       *ServerStatus // nil = all statuses
	ProxyEnabled *bool         // nil = all; true/false = filter
}

// ToolFilter controls which tools are returned by ListTools.
type ToolFilter struct {
	ServerName string // empty = all servers
	Enabled    *bool  // nil = all; true/false = filter
}

// AuditFilter controls which audit entries are returned by ListAuditLog.
type AuditFilter struct {
	Actor        string
	Action       string
	ResourceType string
	ResourceName string
	Limit        int // 0 = default (100)
}
