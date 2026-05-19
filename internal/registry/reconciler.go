package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"strings"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
)

// ReconcilerPool defines the pool operations the reconciler needs.
type ReconcilerPool interface {
	KnownServers() []mcpclient.ServerInfo
	AddServer(name string, entry config.MCPServerEntry)
	DisconnectServer(name string)
	DiscoverTools(ctx context.Context, name string) ([]mcpclient.ToolInfo, error)
}

// ReconcilerGateway defines the gateway operations the reconciler needs.
type ReconcilerGateway interface {
	UnregisterServer(name string)
	ReconcileServer(ctx context.Context, name string, tools []mcpclient.ToolInfo) error
}

// Reconciler drives the gateway from registry state. It runs as a background
// goroutine (reaper pattern) with a periodic tick and an out-of-band trigger
// channel for targeted reconciliation.
type Reconciler struct {
	registry         Store
	pool             ReconcilerPool
	gateway          ReconcilerGateway
	graph            graph.GraphClient
	events           events.Bus // optional; for future event emission
	interval         time.Duration
	discoveryTimeout time.Duration // per-server timeout for DiscoverTools (default 60s)
	trigger          chan string   // server name for targeted reconcile ("" = full)
	stopCh           chan struct{}
	done             chan struct{}
	readyCh          chan struct{}
	readyOnce        sync.Once
	logger           *slog.Logger
}

// ReconcilerOption configures a Reconciler.
type ReconcilerOption func(*Reconciler)

// WithDiscoveryTimeout sets the per-server timeout for DiscoverTools during
// reconciliation. Defaults to 60s. If a backend doesn't respond within this
// window it is marked as failed/degraded and reconciliation continues.
func WithDiscoveryTimeout(d time.Duration) ReconcilerOption {
	return func(r *Reconciler) { r.discoveryTimeout = d }
}

// NewReconciler creates a Reconciler. Call Start() to begin the background loop.
func NewReconciler(reg Store, pool ReconcilerPool, gw ReconcilerGateway, gc graph.GraphClient, interval time.Duration, opts ...ReconcilerOption) *Reconciler {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	r := &Reconciler{
		registry:         reg,
		pool:             pool,
		gateway:          gw,
		graph:            gc,
		interval:         interval,
		discoveryTimeout: 60 * time.Second,
		trigger:          make(chan string, 16),
		stopCh:           make(chan struct{}),
		done:             make(chan struct{}),
		readyCh:          make(chan struct{}),
		logger:           fractalog.Component("reconciler"),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SetEventBus attaches an event bus for future event emission.
func (r *Reconciler) SetEventBus(bus events.Bus) {
	r.events = bus
}

// Start begins the background reconciliation loop.
func (r *Reconciler) Start() {
	go r.loop()
}

// Stop signals the reconciler to shut down and waits for completion.
func (r *Reconciler) Stop() {
	close(r.stopCh)
	<-r.done
}

// Ready returns a channel that closes after the first full reconcile completes.
func (r *Reconciler) Ready() <-chan struct{} {
	return r.readyCh
}

// Trigger sends a targeted reconcile request for a specific server.
func (r *Reconciler) Trigger(server string) {
	select {
	case r.trigger <- server:
	default:
		r.logger.Warn("reconciler trigger channel full, dropping", "server", server)
	}
}

func (r *Reconciler) loop() {
	defer close(r.done)

	// Initial full reconcile
	ctx := context.Background()
	r.reconcileAll(ctx)
	r.readyOnce.Do(func() { close(r.readyCh) })

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case server := <-r.trigger:
			if server != "" {
				r.reconcileServer(ctx, server)
			} else {
				r.reconcileAll(ctx)
			}
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *Reconciler) reconcileAll(ctx context.Context) {
	proxyEnabled := true
	desired, err := r.registry.ListServers(ctx, ServerFilter{ProxyEnabled: &proxyEnabled})
	if err != nil {
		r.logger.Error("reconcile: listing desired servers", "error", err)
		return
	}

	actual := r.pool.KnownServers()

	desiredMap := make(map[string]Server, len(desired))
	for _, s := range desired {
		desiredMap[s.Name] = s
	}

	actualMap := make(map[string]mcpclient.ServerInfo, len(actual))
	for _, s := range actual {
		actualMap[s.Name] = s
	}

	// Step 3: Add servers in desired but not in pool
	for name, srv := range desiredMap {
		if _, ok := actualMap[name]; !ok {
			entry := serverToConfigEntry(srv)
			r.pool.AddServer(name, entry)
			r.logAudit(ctx, "reconciler", "system", "server_added", "server", name, nil)
			r.logger.Info("reconcile: added server to pool", "server", name)
		}
	}

	// Step 4: Remove servers in pool but not in desired
	for name := range actualMap {
		if _, ok := desiredMap[name]; !ok {
			r.gateway.UnregisterServer(name)
			r.removeGraphNodes(ctx, name)
			r.pool.DisconnectServer(name)
			r.logAudit(ctx, "reconciler", "system", "server_removed", "server", name, nil)
			r.logger.Info("reconcile: removed server from pool", "server", name)
		}
	}

	// Re-read pool state after adds/removes
	actual = r.pool.KnownServers()
	for _, info := range actual {
		srv, ok := desiredMap[info.Name]
		if !ok {
			continue
		}
		r.reconcileEntry(ctx, info, srv)
	}
}

func (r *Reconciler) reconcileServer(ctx context.Context, name string) {
	srv, err := r.registry.GetServer(ctx, name)
	if err != nil {
		r.logger.Error("reconcile: getting server", "server", name, "error", err)
		return
	}
	if srv == nil || !srv.ProxyEnabled {
		return
	}

	actual := r.pool.KnownServers()
	for _, info := range actual {
		if info.Name == name {
			r.reconcileEntry(ctx, info, *srv)
			return
		}
	}

	// Server not in pool — add it
	entry := serverToConfigEntry(*srv)
	r.pool.AddServer(name, entry)
	r.logAudit(ctx, "reconciler", "system", "server_added", "server", name, nil)
}

func (r *Reconciler) reconcileEntry(ctx context.Context, info mcpclient.ServerInfo, srv Server) {
	name := info.Name

	switch info.State {
	case mcpclient.ConnReady:
		// Check config drift
		if r.configDrifted(info, srv) {
			r.logger.Info("reconcile: config drift detected, reconnecting", "server", name)
			r.gateway.UnregisterServer(name)
			r.removeGraphNodes(ctx, name)
			r.pool.DisconnectServer(name)
			entry := serverToConfigEntry(srv)
			r.pool.AddServer(name, entry)
			r.logAudit(ctx, "reconciler", "system", "config_drift", "server", name, nil)
			// Will be handled as idle on next cycle
			r.writeHealth(ctx, name, mcpclient.ConnIdle, nil, false)
			return
		}

		// Rediscover tools
		discCtx, discCancel := context.WithTimeout(ctx, r.discoveryTimeout)
		tools, err := r.pool.DiscoverTools(discCtx, name)
		discCancel()
		if err != nil {
			r.logger.Warn("reconcile: rediscovery failed for ready server", "server", name, "error", err)
			r.writeHealth(ctx, name, mcpclient.ConnFailed, err, false)
			return
		}

		schemaDrift, reconcileErr := r.persistAndReconcile(ctx, name, tools)
		if reconcileErr != nil {
			r.writeHealth(ctx, name, mcpclient.ConnFailed, reconcileErr, schemaDrift)
		} else {
			r.writeHealth(ctx, name, mcpclient.ConnReady, nil, schemaDrift)
		}

	case mcpclient.ConnIdle, mcpclient.ConnFailed:
		discCtx, discCancel := context.WithTimeout(ctx, r.discoveryTimeout)
		tools, err := r.pool.DiscoverTools(discCtx, name)
		discCancel()
		if err != nil {
			r.logger.Warn("reconcile: discovery failed", "server", name, "error", err)
			r.writeHealth(ctx, name, mcpclient.ConnFailed, err, false)
			return
		}

		schemaDrift, reconcileErr := r.persistAndReconcile(ctx, name, tools)
		if reconcileErr != nil {
			r.writeHealth(ctx, name, mcpclient.ConnFailed, reconcileErr, schemaDrift)
		} else {
			r.writeHealth(ctx, name, mcpclient.ConnReady, nil, schemaDrift)
		}

	case mcpclient.ConnConnecting:
		// In progress — write pending, skip
		r.writeHealth(ctx, name, mcpclient.ConnConnecting, nil, false)
	}
}

// persistAndReconcile saves discovered tools to the registry, reconciles the
// gateway, and syncs graph nodes. Returns schema drift flag and the first
// critical error encountered (registry persistence or gateway update failure).
func (r *Reconciler) persistAndReconcile(ctx context.Context, name string, tools []mcpclient.ToolInfo) (schemaDrift bool, err error) {
	regTools := toRegistryTools(name, tools)
	schemaDrift = r.detectSchemaDrift(ctx, name, regTools)

	if err := r.registry.ReplaceDiscoveredTools(ctx, name, regTools); err != nil {
		r.logger.Error("reconcile: persisting tools", "server", name, "error", err)
		return schemaDrift, fmt.Errorf("persisting tools: %w", err)
	}

	now := time.Now()
	if err := r.registry.UpdateServerHealth(ctx, name, StatusActive, "", &now); err != nil {
		r.logger.Error("reconcile: updating discovered_at", "server", name, "error", err)
		// Non-critical: health update failure doesn't invalidate tool persistence.
	}

	if err := r.gateway.ReconcileServer(ctx, name, tools); err != nil {
		r.logger.Error("reconcile: gateway reconcile", "server", name, "error", err)
		return schemaDrift, fmt.Errorf("gateway reconcile: %w", err)
	}

	r.syncGraphNodes(ctx, name, tools)
	staleRemoved := r.cleanStaleToolNodes(ctx, name, tools)

	r.logger.Info("reconcile: sync complete",
		"server", name,
		"tools_discovered", len(tools),
		"tools_stale_removed", staleRemoved,
		"schema_drift", schemaDrift,
	)

	if schemaDrift {
		r.logAudit(ctx, "reconciler", "system", "schema_drift", "server", name, nil)
	}

	return schemaDrift, nil
}

// detectSchemaDrift compares new schema hashes against stored tools.
func (r *Reconciler) detectSchemaDrift(ctx context.Context, serverName string, newTools []Tool) bool {
	enabled := true
	existing, err := r.registry.ListTools(ctx, ToolFilter{ServerName: serverName, Enabled: &enabled})
	if err != nil {
		return false
	}

	existingMap := make(map[string]string, len(existing))
	for _, t := range existing {
		existingMap[t.ToolName] = t.SchemaHash
	}

	for _, t := range newTools {
		if oldHash, ok := existingMap[t.ToolName]; ok && oldHash != "" && oldHash != t.SchemaHash {
			r.logger.Info("reconcile: schema drift on tool",
				"server", serverName, "tool", t.ToolName,
				"old_hash", oldHash[:8], "new_hash", t.SchemaHash[:8])
			return true
		}
	}
	return false
}

// configDrifted checks if the registry server config differs from the pool's stored config.
func (r *Reconciler) configDrifted(info mcpclient.ServerInfo, srv Server) bool {
	registryEntry := serverToConfigEntry(srv)
	poolCfg := info.Config

	// Compare serialized JSON — simple and correct
	regJSON, _ := json.Marshal(registryEntry)
	poolJSON, _ := json.Marshal(poolCfg)
	return string(regJSON) != string(poolJSON)
}

// writeHealth maps pool state to registry health status and persists it.
func (r *Reconciler) writeHealth(ctx context.Context, name string, state mcpclient.ConnState, lastErr error, schemaDrift bool) {
	var status ServerStatus
	var msg string

	switch state {
	case mcpclient.ConnIdle, mcpclient.ConnConnecting:
		status = StatusPending
	case mcpclient.ConnReady:
		if schemaDrift {
			status = StatusDegraded
			msg = "schema drift detected"
		} else {
			status = StatusActive
		}
	case mcpclient.ConnFailed:
		status, msg = classifyError(lastErr)
	default:
		status = StatusError
		msg = fmt.Sprintf("unknown pool state: %d", state)
	}

	// Check for health state change and log audit
	srv, err := r.registry.GetServer(ctx, name)
	if err == nil && srv != nil && srv.Status != status {
		r.logAudit(ctx, "reconciler", "system", "health_change", "server", name,
			map[string]any{"from": string(srv.Status), "to": string(status)})
	}

	if err := r.registry.UpdateServerHealth(ctx, name, status, msg, nil); err != nil {
		r.logger.Error("reconcile: writing health", "server", name, "error", err)
	}
}

// classifyError maps an error to the appropriate registry health status.
func classifyError(err error) (ServerStatus, string) {
	if err == nil {
		return StatusError, "unknown error"
	}
	errMsg := err.Error()
	errLower := strings.ToLower(errMsg)

	// Auth errors
	if strings.Contains(errLower, "auth") ||
		strings.Contains(errLower, "unauthorized") ||
		strings.Contains(errLower, "403") ||
		strings.Contains(errLower, "401") {
		return StatusAuthFailed, errMsg
	}

	// Transport errors
	if strings.Contains(errLower, "connection refused") ||
		strings.Contains(errLower, "no such host") ||
		strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "dial") ||
		strings.Contains(errLower, "eof") ||
		strings.Contains(errLower, "transport") ||
		strings.Contains(errLower, "unreachable") {
		return StatusUnreachable, errMsg
	}

	return StatusError, errMsg
}

// syncGraphNodes creates MCPServer/MCPTool graph nodes and PROVIDES edges for a server's tools.
// All writes include _source and _updated_at provenance fields.
func (r *Reconciler) syncGraphNodes(ctx context.Context, serverName string, tools []mcpclient.ToolInfo) {
	if r.graph == nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// MCPServer per server
	msQuery := `MERGE (ms:MCPServer {config_key: $server})
		SET ms.type = 'mcp', ms.name = 'Gateway — ' + $server,
		    ms._source = 'reconciler:auto', ms._updated_at = $updated_at`
	if err := r.graph.Update(ctx, msQuery, map[string]any{
		"server": serverName, "updated_at": now,
	}); err != nil {
		r.logger.Warn("reconcile: graph MCPServer failed", "server", serverName, "error", err)
		return
	}

	for _, t := range tools {
		nsName := serverName + "." + t.Name

		// MCPTool per tool
		mtQuery := `MERGE (mt:MCPTool {name: $name})
			SET mt.tool = $tool, mt.mcp_server = $server, mt.description = $desc,
			    mt._source = 'reconciler:auto', mt._updated_at = $updated_at`
		if err := r.graph.Update(ctx, mtQuery, map[string]any{
			"name": nsName, "tool": t.Name, "server": serverName, "desc": t.Description,
			"updated_at": now,
		}); err != nil {
			r.logger.Warn("reconcile: graph MCPTool failed", "tool", nsName, "error", err)
			continue
		}

		// MCPServer → MCPTool PROVIDES edge
		edgeQuery := `MATCH (ms:MCPServer {config_key: $server})
			MATCH (mt:MCPTool {name: $name})
			MERGE (ms)-[:PROVIDES]->(mt)`
		if err := r.graph.Update(ctx, edgeQuery, map[string]any{
			"server": serverName, "name": nsName,
		}); err != nil {
			r.logger.Warn("reconcile: graph PROVIDES failed", "tool", nsName, "error", err)
		}
	}
}

// cleanStaleToolNodes removes MCPTool nodes in the graph that are no longer in the
// discovered tool list for a server. Only removes nodes with automated provenance.
// Marks connected MCPField nodes as stale before deletion. Returns count of removed tools.
func (r *Reconciler) cleanStaleToolNodes(ctx context.Context, serverName string, tools []mcpclient.ToolInfo) int {
	if r.graph == nil {
		return 0
	}

	// Build set of current tool names (namespaced)
	currentTools := make(map[string]bool, len(tools))
	for _, t := range tools {
		currentTools[serverName+"."+t.Name] = true
	}

	// Query graph for all MCPTool nodes for this server
	queryStr := `MATCH (mt:MCPTool {mcp_server: $server})
		WHERE mt._source IN ['gateway:auto', 'reconciler:auto']
		RETURN mt.name AS name`
	rows, err := r.graph.Query(ctx, queryStr, map[string]any{"server": serverName})
	if err != nil {
		r.logger.Warn("reconcile: querying stale tools failed", "server", serverName, "error", err)
		return 0
	}

	now := time.Now().UTC().Format(time.RFC3339)
	removed := 0

	for _, row := range rows {
		name, ok := row["name"].(string)
		if !ok || name == "" {
			continue
		}
		if currentTools[name] {
			continue // tool still exists
		}

		// Mark connected MCPField nodes as stale
		fieldQuery := `MATCH (mt:MCPTool {name: $name})-[:RETURNS_FIELD]->(mf:MCPField)
			SET mf._status = 'stale', mf._updated_at = $updated_at`
		if err := r.graph.Update(ctx, fieldQuery, map[string]any{
			"name": name, "updated_at": now,
		}); err != nil {
			r.logger.Warn("reconcile: stale MCPField marking failed", "tool", name, "error", err)
		}

		// Remove the stale MCPTool node
		delQuery := `MATCH (mt:MCPTool {name: $name})
			WHERE mt._source IN ['gateway:auto', 'reconciler:auto']
			DETACH DELETE mt`
		if err := r.graph.Update(ctx, delQuery, map[string]any{"name": name}); err != nil {
			r.logger.Warn("reconcile: stale MCPTool removal failed", "tool", name, "error", err)
		} else {
			removed++
			r.logger.Info("reconcile: removed stale tool from graph", "server", serverName, "tool", name)
		}
	}
	return removed
}

// removeGraphNodes removes MCPTool/MCPServer graph nodes for a server.
// Only deletes nodes with automated provenance (_source IN ['reconciler:auto', 'gateway:auto']).
// Marks connected MCPField nodes as stale before deleting MCPTool nodes.
func (r *Reconciler) removeGraphNodes(ctx context.Context, serverName string) {
	if r.graph == nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Mark MCPField nodes connected to this server's MCPTool nodes as stale
	fieldQuery := `MATCH (mt:MCPTool {mcp_server: $server})-[:RETURNS_FIELD]->(mf:MCPField)
		WHERE mt._source IN ['gateway:auto', 'reconciler:auto']
		SET mf._status = 'stale', mf._updated_at = $updated_at`
	if err := r.graph.Update(ctx, fieldQuery, map[string]any{
		"server": serverName, "updated_at": now,
	}); err != nil {
		r.logger.Warn("reconcile: graph MCPField stale marking failed", "server", serverName, "error", err)
	}

	// Remove MCPTool nodes (provenance-gated)
	mtQuery := `MATCH (mt:MCPTool {mcp_server: $server})
		WHERE mt._source IN ['gateway:auto', 'reconciler:auto']
		DETACH DELETE mt`
	if err := r.graph.Update(ctx, mtQuery, map[string]any{"server": serverName}); err != nil {
		r.logger.Warn("reconcile: graph MCPTool removal failed", "server", serverName, "error", err)
	}

	// Mark orphaned DataStore nodes as stale: those whose only QUERYABLE_VIA
	// target is the MCPServer being removed. DataStore with another MCPServer
	// is NOT marked stale. Uses OPTIONAL MATCH pattern (FalkorDB lacks NOT EXISTS).
	dsQuery := `MATCH (ds:DataStore)-[:QUERYABLE_VIA]->(ms:MCPServer {config_key: $server})
		OPTIONAL MATCH (ds)-[:QUERYABLE_VIA]->(other:MCPServer) WHERE other.config_key <> $server
		WITH ds, other WHERE other IS NULL AND (ds._status IS NULL OR ds._status <> 'stale')
		SET ds._status = 'stale', ds._updated_at = $updated_at`
	if err := r.graph.Update(ctx, dsQuery, map[string]any{
		"server": serverName, "updated_at": now,
	}); err != nil {
		r.logger.Warn("reconcile: graph DataStore stale marking failed", "server", serverName, "error", err)
	}

	// Remove MCPServer (provenance-gated). DETACH DELETE removes any
	// remaining edges (e.g. QUERYABLE_VIA from DataStore).
	// Uses OPTIONAL MATCH pattern (FalkorDB lacks NOT EXISTS).
	msQuery := `MATCH (ms:MCPServer {config_key: $server})
		WHERE ms._source IN ['gateway:auto', 'reconciler:auto']
		OPTIONAL MATCH (ms)-[:PROVIDES]->(tool)
		WITH ms, tool WHERE tool IS NULL
		DETACH DELETE ms`
	if err := r.graph.Update(ctx, msQuery, map[string]any{"server": serverName}); err != nil {
		r.logger.Warn("reconcile: graph MCPServer removal failed", "server", serverName, "error", err)
	}
}

// logAudit writes an audit entry to the registry store.
func (r *Reconciler) logAudit(ctx context.Context, actor, actorType, action, resourceType, resourceName string, detail map[string]any) {
	var detailJSON json.RawMessage
	if detail != nil {
		detailJSON, _ = json.Marshal(detail)
	}
	entry := AuditEntry{
		Actor:        actor,
		ActorType:    actorType,
		Action:       action,
		ResourceType: resourceType,
		ResourceName: resourceName,
		Detail:       detailJSON,
		Timestamp:    time.Now(),
	}
	if err := r.registry.LogAudit(ctx, entry); err != nil {
		r.logger.Error("reconcile: audit log failed", "action", action, "error", err)
	}
}

// toRegistryTools converts mcpclient.ToolInfo to registry.Tool with schema hashes.
func toRegistryTools(serverName string, tools []mcpclient.ToolInfo) []Tool {
	result := make([]Tool, len(tools))
	for i, t := range tools {
		hash := schemaHash(t.InputSchema)
		result[i] = Tool{
			ServerName:  serverName,
			ToolName:    t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			SchemaHash:  hash,
			Enabled:     true,
			LastSeenAt:  time.Now(),
		}
	}
	return result
}

// schemaHash returns SHA-256 hex digest of JSON input schema.
func schemaHash(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	h := sha256.Sum256(schema)
	return fmt.Sprintf("%x", h)
}

// serverToConfigEntry converts a registry Server to a config.MCPServerEntry
// by deserializing the connection_config JSON.
func serverToConfigEntry(srv Server) config.MCPServerEntry {
	var entry config.MCPServerEntry
	if len(srv.ConnectionConfig) > 0 {
		_ = json.Unmarshal(srv.ConnectionConfig, &entry)
	}
	return entry
} 
