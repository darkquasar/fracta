package gateway

import (
	"testing"

	"github.com/darkquasar/fracta/internal/config"
)

func TestPolicyAllowed(t *testing.T) {
	tests := []struct {
		name     string
		policy   *config.ToolPolicy
		tool     string
		expected bool
	}{
		{"nil policy allows all", nil, "any_tool", true},
		{"empty policy allows all", &config.ToolPolicy{}, "any_tool", true},
		{"deny blocks exact match", &config.ToolPolicy{Deny: []string{"post_case_note"}}, "post_case_note", false},
		{"deny does not block non-match", &config.ToolPolicy{Deny: []string{"post_case_note"}}, "search_cases", true},
		{"deny wildcard blocks all", &config.ToolPolicy{Deny: []string{"*"}}, "anything", false},
		{"deny prefix glob blocks matching", &config.ToolPolicy{Deny: []string{"post_*"}}, "post_case_note", false},
		{"deny prefix glob allows non-matching", &config.ToolPolicy{Deny: []string{"post_*"}}, "get_cases", true},
		{"allow_only restricts to matching", &config.ToolPolicy{AllowOnly: []string{"search_*", "get_*"}}, "search_cases", true},
		{"allow_only blocks non-matching", &config.ToolPolicy{AllowOnly: []string{"search_*", "get_*"}}, "post_case_note", false},
		{"allow_only wildcard allows all", &config.ToolPolicy{AllowOnly: []string{"*"}}, "anything", true},
		{"allow_only exact match", &config.ToolPolicy{AllowOnly: []string{"search_cases"}}, "search_cases", true},
		{"both: allow_only first then deny removes", &config.ToolPolicy{AllowOnly: []string{"search_*"}, Deny: []string{"search_internal"}}, "search_cases", true},
		{"both: deny removes from allow_only set", &config.ToolPolicy{AllowOnly: []string{"search_*"}, Deny: []string{"search_internal"}}, "search_internal", false},
		{"both: allow_only blocks before deny applies", &config.ToolPolicy{AllowOnly: []string{"search_*"}, Deny: []string{"post_note"}}, "post_note", false},
		{"prefix glob requires prefix not substring", &config.ToolPolicy{Deny: []string{"obs*"}}, "get_obs_data", true},
		{"prefix glob matches prefix", &config.ToolPolicy{Deny: []string{"obs*"}}, "obs_alerts", false},
		{"no mid-string wildcard support", &config.ToolPolicy{Deny: []string{"obs*alerts"}}, "obs_get_alerts", true},
		{"empty string tool", &config.ToolPolicy{Deny: []string{"x"}}, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PolicyAllowed(tc.policy, tc.tool)
			if got != tc.expected {
				t.Errorf("PolicyAllowed(%+v, %q) = %v, want %v", tc.policy, tc.tool, got, tc.expected)
			}
		})
	}
}

func TestMatchesAny(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		input    string
		expected bool
	}{
		{"empty patterns", nil, "foo", false},
		{"exact match", []string{"foo"}, "foo", true},
		{"no match", []string{"bar"}, "foo", false},
		{"wildcard", []string{"*"}, "anything", true},
		{"prefix glob match", []string{"observability_*"}, "observability_get_alerts", true},
		{"prefix glob no match", []string{"observability_*"}, "obs_alerts", false},
		{"multiple patterns first matches", []string{"get_*", "search_*"}, "get_cases", true},
		{"multiple patterns second matches", []string{"get_*", "search_*"}, "search_cases", true},
		{"multiple patterns none match", []string{"get_*", "search_*"}, "post_note", false},
		{"glob star alone matches empty after prefix", []string{"foo*"}, "foo", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesAny(tc.patterns, tc.input)
			if got != tc.expected {
				t.Errorf("matchesAny(%v, %q) = %v, want %v", tc.patterns, tc.input, got, tc.expected)
			}
		})
	}
} 
