package registry

import (
	"testing"

	"github.com/darkquasar/fracta/internal/config"
)

func TestRegistryTransportType_Remote(t *testing.T) {
	tests := []struct {
		name  string
		entry config.MCPServerEntry
		want  string
	}{
		{
			name:  "remote streamable-http",
			entry: config.MCPServerEntry{Remote: &config.MCPServerRemote{URL: "http://mcp:8080", Transport: "streamable-http"}},
			want:  "streamable_http",
		},
		{
			name:  "remote sse",
			entry: config.MCPServerEntry{Remote: &config.MCPServerRemote{URL: "http://mcp:8080/sse", Transport: "sse"}},
			want:  "http",
		},
		{
			name: "local beats legacy kubernetes alias",
			entry: config.MCPServerEntry{
				Local:      config.MCPServerLocal{Command: "local-mcp"},
				Kubernetes: config.MCPServerRemote{URL: "http://mcp:8080"},
			},
			want: "stdio",
		},
		{
			name:  "legacy kubernetes alias",
			entry: config.MCPServerEntry{Kubernetes: config.MCPServerRemote{URL: "http://mcp:8080"}},
			want:  "streamable_http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RegistryTransportType(tt.entry); got != tt.want {
				t.Fatalf("RegistryTransportType() = %q, want %q", got, tt.want)
			}
		})
	}
}
