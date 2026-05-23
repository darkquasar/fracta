package gateway

import (
	"sort"
	"strings"

	"github.com/darkquasar/fracta/internal/config"
)

// PolicyServerSummary is a JSON-friendly snapshot of one server's tool policy.
type PolicyServerSummary struct {
	Server    string   `json:"server"`
	Deny      []string `json:"deny,omitempty"`
	AllowOnly []string `json:"allow_only,omitempty"`
}

// PolicySummary returns a deterministic, JSON-friendly snapshot of the given
// per-server policy map. Servers without a policy are omitted. Servers are
// sorted by name so output is stable across calls.
func PolicySummary(policies map[string]*config.ToolPolicy) []PolicyServerSummary {
	if len(policies) == 0 {
		return nil
	}
	names := make([]string, 0, len(policies))
	for name, p := range policies {
		if p == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]PolicyServerSummary, 0, len(names))
	for _, name := range names {
		p := policies[name]
		s := PolicyServerSummary{Server: name}
		if len(p.Deny) > 0 {
			s.Deny = append(s.Deny, p.Deny...)
		}
		if len(p.AllowOnly) > 0 {
			s.AllowOnly = append(s.AllowOnly, p.AllowOnly...)
		}
		out = append(out, s)
	}
	return out
}

// PolicyAllowed returns true if the given tool name passes the policy.
// A nil policy means all tools are allowed (backward compatible default).
func PolicyAllowed(policy *config.ToolPolicy, toolName string) bool {
	if policy == nil {
		return true
	}
	if len(policy.AllowOnly) > 0 && !matchesAny(policy.AllowOnly, toolName) {
		return false
	}
	if matchesAny(policy.Deny, toolName) {
		return false
	}
	return true
}

// matchesAny returns true if name matches any pattern in the list.
// Supported syntax: exact string, "*" (match all), "prefix*" (prefix glob).
func matchesAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if p == "*" || p == name {
			return true
		}
		if strings.HasSuffix(p, "*") && strings.HasPrefix(name, p[:len(p)-1]) {
			return true
		}
	}
	return false
}
