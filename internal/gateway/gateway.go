// Package gateway aggregates tools from backend MCP servers onto fracta's
// MCPServer. It discovers tools via the MCPClientPool, registers proxy
// handlers with dot-notation namespacing, and creates MCPServer/MCPTool
// nodes in the knowledge graph for search_tool discovery (when the
// reconciler is not active).
//
// The reconciler periodically syncs desired state (registry) with actual
// state (pool/gateway). When the reconciler is active, it exclusively owns
// graph writes — the gateway skips registerInGraph. Additionally, the pool
// listens for tools/list_changed notifications from backend servers to
// trigger targeted reconciliation.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/ctxkeys"
	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/graph"
	"github.com/darkquasar/fracta/internal/mcpclient"
	"github.com/darkquasar/fracta/internal/registry"
	"github.com/darkquasar/fracta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CatalogEntry describes a single proxied tool.
type CatalogEntry struct {
	ServerName   string `json:"server_name"`
	OriginalName string `json:"original_name"`
	Description  string `json:"description"`
}

// Gateway aggregates tools from backend MCP servers and registers proxy
// handlers on fracta's MCPServer.
type Gateway struct {
	mcpServer        *server.MCPServer
	pool             *mcpclient.Pool
	graph            graph.GraphClient
	schema           *schema.SchemaRegistry        // optional; enables label validation on graph writes
	events           events.Bus                    // optional; for future event emission
	reconcilerActive bool                          // when true, gateway skips graph writes (reconciler owns them)
	catalog          map[string]CatalogEntry       // namespaced name → route info
	registryStore    registry.Store                // optional; for visibility computation
	toolPolicies     map[string]*config.ToolPolicy // server name → policy (from config)
	visibleSet       map[string]bool               // namespaced tool name → visible (cached)
	visibleGen       uint64                        // monotonic generation; prevents stale builds overwriting newer ones
	scopes           *scopeStore                   // per-agent tool visibility scopes
	mu               sync.RWMutex
	logger           *slog.Logger
}

// SetSchemaRegistry attaches a schema registry for validating node labels
// during graph registration. When set, the gateway logs warnings for any
// labels not defined in the schema.
func (g *Gateway) SetSchemaRegistry(reg *schema.SchemaRegistry) {
	g.schema = reg
}

// SetReconcilerActive marks the gateway as having an active reconciler.
// When true, the gateway skips all graph writes — the reconciler owns
// inventory graph nodes exclusively.
func (g *Gateway) SetReconcilerActive(active bool) {
	g.reconcilerActive = active
}

// New creates a Gateway. Call SetMCPServer before RegisterAll.
func New(pool *mcpclient.Pool, gc graph.GraphClient) *Gateway {
	return &Gateway{
		pool:    pool,
		graph:   gc,
		catalog: make(map[string]CatalogEntry),
		scopes:  newScopeStore(),
		logger:  fractalog.Component("gateway"),
	}
}

// SetEventBus attaches an event bus for future event emission.
func (g *Gateway) SetEventBus(bus events.Bus) {
	g.events = bus
}

// SetRegistryStore attaches the registry store for visibility computations.
func (g *Gateway) SetRegistryStore(store registry.Store) {
	g.mu.Lock()
	g.registryStore = store
	g.mu.Unlock()
}

// SetToolPolicies sets the per-server tool policies from config.
func (g *Gateway) SetToolPolicies(policies map[string]*config.ToolPolicy) {
	g.mu.Lock()
	g.toolPolicies = policies
	g.mu.Unlock()
}

// BuildVisibleSet computes the set of namespaced tool names that are visible
// (enabled AND policy_allowed). Fails closed: on registry read error, the
// previous visibleSet is preserved (never set to nil after first successful build).
// Uses a generation counter to prevent stale builds from overwriting newer ones.
func (g *Gateway) BuildVisibleSet(ctx context.Context) {
	g.mu.Lock()
	gen := g.visibleGen + 1
	g.visibleGen = gen
	policies := g.toolPolicies
	store := g.registryStore
	catalogSnapshot := make(map[string]CatalogEntry, len(g.catalog))
	for k, v := range g.catalog {
		catalogSnapshot[k] = v
	}
	g.mu.Unlock()

	if len(catalogSnapshot) == 0 {
		g.mu.Lock()
		if g.visibleGen == gen {
			g.visibleSet = make(map[string]bool)
		}
		g.mu.Unlock()
		return
	}

	// Build enabled map from registry (if available).
	enabledMap := make(map[string]bool)
	if store != nil {
		tools, err := store.ListTools(ctx, registry.ToolFilter{})
		if err != nil {
			g.logger.Error("BuildVisibleSet: registry read failed, preserving existing set", "error", err)
			return // fail closed — keep previous visibleSet
		}
		for _, t := range tools {
			key := t.ServerName + "." + t.ToolName
			enabledMap[key] = t.Enabled
		}
	}

	newSet := make(map[string]bool, len(catalogSnapshot))
	for nsName, entry := range catalogSnapshot {
		policy := policies[entry.ServerName]
		policyAllowed := PolicyAllowed(policy, entry.OriginalName)

		enabled := true
		if store != nil {
			if e, found := enabledMap[nsName]; found {
				enabled = e
			}
		}
		newSet[nsName] = enabled && policyAllowed
	}

	g.mu.Lock()
	// Only write if this is still the latest generation (no newer build started).
	if g.visibleGen == gen {
		g.visibleSet = newSet
	}
	g.mu.Unlock()
}

// IsToolVisible returns whether a namespaced tool is in the visible set AND
// still exists in the catalog. Fails closed: if the visible set hasn't been
// computed yet but enforcement is configured, denies access.
func (g *Gateway) IsToolVisible(namespacedName string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.visibleSet == nil {
		return g.registryStore == nil && len(g.toolPolicies) == 0
	}
	// Tool must still be in catalog (prevents stale visibleSet entries
	// from authorizing tools that were removed by UnregisterServer).
	if _, inCatalog := g.catalog[namespacedName]; !inCatalog {
		return false
	}
	visible, found := g.visibleSet[namespacedName]
	if !found {
		return false
	}
	return visible
}

// FilterToolsForAgent returns a filtered tool list removing non-visible
// gateway-proxied tools. Native fracta tools (not in catalog) always pass.
// Respects both global visibility and per-agent scope (if registered).
// Fails closed: if visibility enforcement is configured but the set hasn't
// been built yet, all proxied tools are filtered out.
func (g *Gateway) FilterToolsForAgent(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	g.mu.RLock()
	visible := g.visibleSet
	hasEnforcement := g.registryStore != nil || len(g.toolPolicies) > 0
	catalogKeys := make(map[string]struct{}, len(g.catalog))
	for k := range g.catalog {
		catalogKeys[k] = struct{}{}
	}
	g.mu.RUnlock()

	if visible == nil && !hasEnforcement {
		return tools // no enforcement configured — pass all (backward compat)
	}

	// Check for per-agent scope.
	agentTask, hasAgent := ctxkeys.AgentTask(ctx)
	var agentScope *AgentScope
	if hasAgent {
		g.scopes.mu.RLock()
		if s, ok := g.scopes.scopes[agentTask]; ok && time.Now().Before(s.ExpiresAt) {
			agentScope = s
		}
		g.scopes.mu.RUnlock()
	}

	filtered := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if _, inCatalog := catalogKeys[t.Name]; !inCatalog {
			filtered = append(filtered, t)
			continue
		}
		// Global visibility: fail closed if set not built yet.
		if visible == nil || !visible[t.Name] {
			continue
		}
		// Per-agent scope: if registered, intersect.
		if agentScope != nil && !agentScope.AllowedTools[t.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// SetMCPServer is called by the WithGateway ServerOption during construction.
func (g *Gateway) SetMCPServer(m *server.MCPServer) {
	g.mcpServer = m
}

// RegisterAll discovers tools from all configured backend servers and
// registers proxy handlers on the MCPServer.
//
// Startup policy: best-effort with degradation. Per-server failures are
// logged as warnings but do not fail startup. Fracta's native tools always
// work; gateway tools are available for servers that connected successfully.
// This is intentional — a slow or unavailable backend should not block
// the entire MCP server from starting.
func (g *Gateway) RegisterAll(ctx context.Context) error {
	if g.mcpServer == nil {
		return fmt.Errorf("gateway: MCPServer not set (call SetMCPServer first)")
	}

	var totalTools int
	for _, name := range g.pool.ServerNames() {
		n, err := g.RegisterServer(ctx, name)
		if err != nil {
			g.logger.Warn("failed to register server", "server", name, "error", err)
			continue
		}
		totalTools += n
	}
	configured := len(g.pool.ServerNames())
	connected := 0
	for _, name := range g.pool.ServerNames() {
		g.mu.RLock()
		for _, e := range g.catalog {
			if e.ServerName == name {
				connected++
				break
			}
		}
		g.mu.RUnlock()
	}
	if connected == configured {
		g.logger.Info("all backends connected", "servers", configured, "tools", totalTools)
	} else {
		g.logger.Warn("partial registration (degraded)", "connected", connected, "configured", configured, "tools", totalTools)
	}
	return nil
}

// RegisterServer discovers and registers tools from a single backend.
// Returns the number of tools registered.
func (g *Gateway) RegisterServer(ctx context.Context, name string) (int, error) {
	tools, err := g.pool.DiscoverTools(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("discovering tools from %q: %w", name, err)
	}

	registered := 0
	for _, t := range tools {
		nsName := name + "." + t.Name

		// Skip if collides with a native fracta tool (no dots in native names)
		if existing := g.mcpServer.GetTool(nsName); existing != nil {
			g.logger.Debug("skipping duplicate tool", "tool", nsName)
			continue
		}

		// Register proxy handler
		proxyTool := mcp.NewToolWithRawSchema(
			nsName,
			fmt.Sprintf("[%s] %s", name, t.Description),
			t.InputSchema,
		)
		g.mcpServer.AddTool(proxyTool, g.proxyHandler(name, t.Name))

		// Track in catalog
		g.mu.Lock()
		g.catalog[nsName] = CatalogEntry{
			ServerName:   name,
			OriginalName: t.Name,
			Description:  t.Description,
		}
		g.mu.Unlock()

		registered++
	}

	// Register in graph if available and reconciler is not active.
	// When reconciler is active, it owns all inventory graph writes.
	if g.graph != nil && !g.reconcilerActive {
		g.registerInGraph(ctx, name, tools)
	}

	g.logger.Info("registered server", "server", name, "tools", registered)
	return registered, nil
}

// proxyHandler creates a handler that forwards tool calls to the backend
// via CallToolRaw — preserving the full MCP response without normalization.
// Includes defense-in-depth visibility check (global + per-agent scope) at call time.
func (g *Gateway) proxyHandler(serverName, toolName string) server.ToolHandlerFunc {
	nsName := serverName + "." + toolName
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		agentTask, _ := ctxkeys.AgentTask(ctx)

		// Global policy check.
		if !g.IsToolVisible(nsName) {
			g.emitProxyEvent("proxy_rejected", "rejected", nsName, agentTask, map[string]string{
				"server": serverName,
				"tool":   toolName,
				"reason": "disabled by policy",
			})
			return mcp.NewToolResultError(fmt.Sprintf("tool %q is not available (blocked by policy or disabled)", nsName)), nil
		}

		// Per-agent scope check.
		if agentTask != "" && !g.IsToolVisibleForAgent(agentTask, nsName) {
			g.emitProxyEvent("proxy_rejected", "rejected", nsName, agentTask, map[string]string{
				"server": serverName,
				"tool":   toolName,
				"reason": "not in agent scope",
			})
			return mcp.NewToolResultError(fmt.Sprintf("tool %q is not available in your current scope", nsName)), nil
		}

		start := time.Now()
		result, err := g.pool.CallToolRaw(ctx, serverName, toolName, req.GetArguments())
		duration := time.Since(start)

		if err != nil {
			g.emitProxyEvent("proxy_call", "failure", nsName, agentTask, map[string]string{
				"server":      serverName,
				"tool":        toolName,
				"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
				"error":       err.Error(),
			})
			return mcp.NewToolResultError(fmt.Sprintf("gateway proxy %s.%s: %v", serverName, toolName, err)), nil
		}

		outcome := "success"
		if result.IsError {
			outcome = "failure"
		}
		g.emitProxyEvent("proxy_call", outcome, nsName, agentTask, map[string]string{
			"server":      serverName,
			"tool":        toolName,
			"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
		})
		return result, nil
	}
}

func (g *Gateway) emitProxyEvent(action, outcome, resource, agentTask string, attrs map[string]string) {
	if g.events == nil {
		return
	}
	var e events.Event
	if outcome == "rejected" {
		e = events.Warn("gateway", action, attrs["reason"])
	} else {
		e = events.Info("gateway", action)
	}
	e.Category = "backend"
	e.Resource = "mcp_tool:" + resource
	e.Outcome = outcome
	e.Task = agentTask
	e.Attrs = attrs
	g.events.Emit(context.Background(), e)
}

// Catalog returns a sorted snapshot of all registered proxied tools.
func (g *Gateway) Catalog() []CatalogEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()

	entries := make([]CatalogEntry, 0, len(g.catalog))
	for nsName, e := range g.catalog {
		if g.visibleSet != nil {
			if visible, found := g.visibleSet[nsName]; found && !visible {
				continue
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ServerName+"."+entries[i].OriginalName <
			entries[j].ServerName+"."+entries[j].OriginalName
	})
	return entries
}

// registerInGraph creates MCPServer + MCPTool nodes with PROVIDES edges for
// proxied tools. This is the legacy fallback path — only used when the
// reconciler is NOT active (see SetReconcilerActive).
func (g *Gateway) registerInGraph(ctx context.Context, serverName string, tools []mcpclient.ToolInfo) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Validate labels against schema if available.
	if g.schema != nil {
		for _, label := range []string{"MCPServer", "MCPTool"} {
			if _, ok := g.schema.Nodes[label]; !ok {
				g.logger.Warn("graph label not in schema", "label", label, "server", serverName)
			}
		}
	}

	// Step 1: MCPServer per server
	msQuery := `MERGE (ms:MCPServer {config_key: $server})
		SET ms.type = 'mcp', ms.name = 'Gateway — ' + $server,
		    ms._source = $source, ms._updated_at = $updated_at`
	if err := g.graph.Update(ctx, msQuery, map[string]any{
		"server": serverName, "source": "gateway:auto", "updated_at": now,
	}); err != nil {
		g.logger.Warn("graph MCPServer failed", "server", serverName, "error", err)
		return
	}

	for _, t := range tools {
		nsName := serverName + "." + t.Name

		// Step 2: MCPTool per tool
		mtQuery := `MERGE (mt:MCPTool {name: $name})
			SET mt.tool = $tool, mt.mcp_server = $server, mt.description = $desc,
			    mt._source = $source, mt._updated_at = $updated_at`
		if err := g.graph.Update(ctx, mtQuery, map[string]any{
			"name": nsName, "tool": t.Name, "server": serverName, "desc": t.Description,
			"source": "gateway:auto", "updated_at": now,
		}); err != nil {
			g.logger.Warn("graph MCPTool failed", "tool", nsName, "error", err)
			continue
		}

		// Step 3: MCPServer → MCPTool PROVIDES edge
		edgeQuery := `MATCH (ms:MCPServer {config_key: $server})
			MATCH (mt:MCPTool {name: $name})
			MERGE (ms)-[:PROVIDES]->(mt)`
		if err := g.graph.Update(ctx, edgeQuery, map[string]any{
			"server": serverName, "name": nsName,
		}); err != nil {
			g.logger.Warn("graph PROVIDES failed", "tool", nsName, "error", err)
		}
	}
}

// UnregisterServer removes all tools from a server and cleans up proxy handlers.
func (g *Gateway) UnregisterServer(name string) {
	g.mu.Lock()
	var toDelete []string
	for nsName, e := range g.catalog {
		if e.ServerName == name {
			toDelete = append(toDelete, nsName)
		}
	}
	for _, nsName := range toDelete {
		delete(g.catalog, nsName)
	}
	g.mu.Unlock()

	if len(toDelete) > 0 && g.mcpServer != nil {
		g.mcpServer.DeleteTools(toDelete...)
	}

	g.logger.Info("unregistered server", "server", name, "tools", len(toDelete))
}

// ReconcileServer diffs the current catalog for a server against a new tool list
// and applies additions/removals/updates. Rebuilds the visibility set after
// reconciliation. No graph sync — that is the reconciler's job.
func (g *Gateway) ReconcileServer(ctx context.Context, name string, tools []mcpclient.ToolInfo) error {
	if g.mcpServer == nil {
		return fmt.Errorf("gateway: MCPServer not set")
	}

	// Build set of incoming tools
	incoming := make(map[string]mcpclient.ToolInfo, len(tools))
	for _, t := range tools {
		incoming[t.Name] = t
	}

	// Find current tools for this server
	g.mu.RLock()
	var currentNames []string
	currentByOriginal := make(map[string]string) // originalName → nsName
	for nsName, e := range g.catalog {
		if e.ServerName == name {
			currentNames = append(currentNames, nsName)
			currentByOriginal[e.OriginalName] = nsName
		}
	}
	g.mu.RUnlock()

	// Remove tools no longer in incoming
	var toRemove []string
	for _, nsName := range currentNames {
		g.mu.RLock()
		e := g.catalog[nsName]
		g.mu.RUnlock()
		if _, ok := incoming[e.OriginalName]; !ok {
			toRemove = append(toRemove, nsName)
		}
	}

	if len(toRemove) > 0 {
		g.mu.Lock()
		for _, nsName := range toRemove {
			delete(g.catalog, nsName)
		}
		g.mu.Unlock()
		g.mcpServer.DeleteTools(toRemove...)
	}

	// Add or update tools
	for _, t := range tools {
		nsName := name + "." + t.Name

		if _, exists := currentByOriginal[t.Name]; exists {
			// Tool exists — update by re-adding (mcp-go AddTool overwrites)
			proxyTool := mcp.NewToolWithRawSchema(
				nsName,
				fmt.Sprintf("[%s] %s", name, t.Description),
				t.InputSchema,
			)
			g.mcpServer.DeleteTools(nsName)
			g.mcpServer.AddTool(proxyTool, g.proxyHandler(name, t.Name))

			g.mu.Lock()
			g.catalog[nsName] = CatalogEntry{
				ServerName:   name,
				OriginalName: t.Name,
				Description:  t.Description,
			}
			g.mu.Unlock()
		} else {
			// New tool
			proxyTool := mcp.NewToolWithRawSchema(
				nsName,
				fmt.Sprintf("[%s] %s", name, t.Description),
				t.InputSchema,
			)
			g.mcpServer.AddTool(proxyTool, g.proxyHandler(name, t.Name))

			g.mu.Lock()
			g.catalog[nsName] = CatalogEntry{
				ServerName:   name,
				OriginalName: t.Name,
				Description:  t.Description,
			}
			g.mu.Unlock()
		}
	}

	// Count tools for this server after reconciliation.
	g.mu.RLock()
	var serverToolCount int
	for _, e := range g.catalog {
		if e.ServerName == name {
			serverToolCount++
		}
	}
	g.mu.RUnlock()

	g.logger.Info("reconciled server tools",
		"server", name,
		"incoming", len(tools),
		"removed", len(toRemove),
		"server_tools_after", serverToolCount,
		"total_tools", g.ToolCount(),
	)

	g.BuildVisibleSet(ctx)

	return nil
}

// ServerForTool returns the backend server name for a namespaced tool name.
// Returns empty string if not found.
func (g *Gateway) ServerForTool(namespacedName string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if e, ok := g.catalog[namespacedName]; ok {
		return e.ServerName
	}
	return ""
}

// ToolCount returns the number of proxied tools registered.
func (g *Gateway) ToolCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.catalog)
}

// ToolsByServer returns tool names grouped by server.
func (g *Gateway) ToolsByServer() map[string][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make(map[string][]string)
	for nsName, e := range g.catalog {
		result[e.ServerName] = append(result[e.ServerName], nsName)
	}
	return result
}

// ParseNamespacedTool splits "server.tool" into server and tool components.
func ParseNamespacedTool(name string) (server, tool string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return "", name
	}
	return parts[0], parts[1]
}

// GetTool returns a JSON representation of a tool from the catalog.
func (g *Gateway) GetTool(namespacedName string) (json.RawMessage, bool) {
	g.mu.RLock()
	e, ok := g.catalog[namespacedName]
	g.mu.RUnlock()
	if !ok {
		return nil, false
	}
	data, _ := json.Marshal(e)
	return data, true
} 
