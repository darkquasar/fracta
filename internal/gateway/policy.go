package gateway

import (
	"strings"

	"github.com/darkquasar/fracta/internal/config"
)

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
