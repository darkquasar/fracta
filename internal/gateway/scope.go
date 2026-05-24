package gateway

import (
	"sync"
	"time"
)

// AgentScope defines the tool set visible to a specific agent and its sidecars.
type AgentScope struct {
	AgentTask    string
	AllowedTools map[string]bool // namespaced tools: "elastic.search" → true
	ExpiresAt    time.Time
}

type scopeStore struct {
	mu     sync.RWMutex
	scopes map[string]*AgentScope
}

func newScopeStore() *scopeStore {
	return &scopeStore{scopes: make(map[string]*AgentScope)}
}

// RegisterScope records which tools an agent is allowed to use.
// If allowedTools is nil/empty, no scope restriction is applied (global visibility).
func (g *Gateway) RegisterScope(agentTask string, allowedTools []string, ttl time.Duration) {
	if len(allowedTools) == 0 {
		return
	}
	scope := &AgentScope{
		AgentTask:    agentTask,
		AllowedTools: make(map[string]bool, len(allowedTools)),
		ExpiresAt:    time.Now().Add(ttl),
	}
	for _, t := range allowedTools {
		scope.AllowedTools[t] = true
	}
	g.scopes.mu.Lock()
	g.scopes.scopes[agentTask] = scope
	g.scopes.mu.Unlock()
}

// UnregisterScope removes an agent's scope entry.
func (g *Gateway) UnregisterScope(agentTask string) {
	g.scopes.mu.Lock()
	delete(g.scopes.scopes, agentTask)
	g.scopes.mu.Unlock()
}

// VisibleToolsForAgent returns tools visible to a specific agent.
// Intersects: global visibility ∩ agent scope. If no scope registered, returns global.
func (g *Gateway) VisibleToolsForAgent(agentTask string) []CatalogEntry {
	global := g.visibleCatalogEntries()

	g.scopes.mu.RLock()
	scope, hasScope := g.scopes.scopes[agentTask]
	g.scopes.mu.RUnlock()

	if !hasScope || time.Now().After(scope.ExpiresAt) {
		return global
	}

	var filtered []CatalogEntry
	for _, entry := range global {
		nsName := entry.ServerName + "." + entry.OriginalName
		if scope.AllowedTools[nsName] {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// IsToolVisibleForAgent checks a single tool against global policy + agent scope.
func (g *Gateway) IsToolVisibleForAgent(agentTask, nsName string) bool {
	if !g.IsToolVisible(nsName) {
		return false
	}

	g.scopes.mu.RLock()
	scope, hasScope := g.scopes.scopes[agentTask]
	g.scopes.mu.RUnlock()

	if !hasScope || time.Now().After(scope.ExpiresAt) {
		return true
	}

	return scope.AllowedTools[nsName]
}

// visibleCatalogEntries returns all globally visible catalog entries.
func (g *Gateway) visibleCatalogEntries() []CatalogEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var entries []CatalogEntry
	for nsName, entry := range g.catalog {
		if g.visibleSet[nsName] {
			entries = append(entries, entry)
		}
	}
	return entries
}
