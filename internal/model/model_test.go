package model

import "testing"

func TestResolveModelFromTier(t *testing.T) {
	tiers := map[string]string{
		"heavy":  "claude-opus-4-6",
		"medium": "claude-sonnet-4-5-20250929",
		"light":  "claude-haiku-3-5-20241022",
	}

	tests := []struct {
		name   string
		tier   string
		tiers  map[string]string
		wantID string
		wantOK bool
	}{
		{"heavy tier", "heavy", tiers, "claude-opus-4-6", true},
		{"medium tier", "medium", tiers, "claude-sonnet-4-5-20250929", true},
		{"light tier", "light", tiers, "claude-haiku-3-5-20241022", true},
		{"unknown tier", "turbo", tiers, "", false},
		{"nil map", "heavy", nil, "", false},
		{"empty map", "heavy", map[string]string{}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveModelFromTier(tt.tier, tt.tiers)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantID {
				t.Errorf("model = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestValidTierNames(t *testing.T) {
	expected := map[string]bool{"heavy": true, "medium": true, "light": true}
	if len(ValidTierNames) != len(expected) {
		t.Fatalf("ValidTierNames has %d entries, want %d", len(ValidTierNames), len(expected))
	}
	for _, name := range ValidTierNames {
		if !expected[name] {
			t.Errorf("unexpected tier name %q", name)
		}
	}
}
