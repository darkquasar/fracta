package registry

import "github.com/darkquasar/fracta/internal/config"

// RegistryTransportType derives the stored transport_type for a registry server
// entry from its MCPServerEntry config. This normalizes transport variants
// (hyphen vs underscore) and maps remote entries to their actual transport.
func RegistryTransportType(entry config.MCPServerEntry) string {
	if entry.Remote != nil && entry.Remote.URL != "" {
		return registryRemoteTransportType(entry.Remote.Transport)
	}
	if entry.Local.Command != "" {
		return "stdio"
	}
	if entry.Kubernetes.URL != "" {
		return registryRemoteTransportType(entry.Kubernetes.Transport)
	}
	return "stdio"
}

func registryRemoteTransportType(transport string) string {
	switch transport {
	case "streamable-http", "streamable_http":
		return "streamable_http"
	case "sse":
		return "http"
	default:
		return "streamable_http"
	}
}
