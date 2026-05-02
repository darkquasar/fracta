package orchestrator

import (
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/config"
)

func TestResolveModel(t *testing.T) {
	hc := &config.HostConfig{
		Model: "default-model",
		ModelTiers: map[string]string{
			"heavy":  "claude-opus-4-6",
			"medium": "claude-sonnet-4-5-20250929",
			"light":  "claude-haiku-3-5-20241022",
		},
	}

	hcNoTiers := &config.HostConfig{
		Model: "default-model",
	}

	tests := []struct {
		name     string
		explicit string
		tier     string
		hc       *config.HostConfig
		want     string
		wantErr  string
	}{
		// Explicit model overrides everything
		{
			name:     "explicit model overrides tier",
			explicit: "my-custom-model",
			tier:     "heavy",
			hc:       hc,
			want:     "my-custom-model",
		},
		{
			name:     "explicit model overrides host config default",
			explicit: "my-custom-model",
			hc:       hc,
			want:     "my-custom-model",
		},
		// Tier lookup
		{
			name: "heavy tier resolves",
			tier: "heavy",
			hc:   hc,
			want: "claude-opus-4-6",
		},
		{
			name: "light tier resolves",
			tier: "light",
			hc:   hc,
			want: "claude-haiku-3-5-20241022",
		},
		// Invalid tier
		{
			name:    "invalid tier rejects",
			tier:    "turbo",
			hc:      hc,
			wantErr: "invalid tier",
		},
		// Tier not configured
		{
			name:    "tier not configured",
			tier:    "heavy",
			hc:      hcNoTiers,
			wantErr: "not configured",
		},
		// No explicit, no tier → host config default
		{
			name: "falls through to host config default",
			hc:   hc,
			want: "default-model",
		},
		// Nil host config → empty (host CLI default)
		{
			name: "nil host config returns empty",
			hc:   nil,
			want: "",
		},
		// Empty host config → empty
		{
			name: "empty host config returns empty",
			hc:   &config.HostConfig{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveModel(tt.explicit, tt.tier, tt.hc)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveModel(%q, %q, ...) = %q, want %q", tt.explicit, tt.tier, got, tt.want)
			}
		})
	}
}
