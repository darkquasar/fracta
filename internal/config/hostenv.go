package config

import (
	"fmt"

	"github.com/darkquasar/fracta/internal/runtime"
)

// BuildHostEnv converts a RuntimeEntry's env declarations into runtime-generic EnvEntries.
// backend selects which env source to use: "kubernetes" -> hc.Kubernetes.Env, "local" -> hc.Env.
// Auth-specific env injection is handled by the credential pipeline (internal/auth/credentials),
// not here. This function is purely about runtime config env vars.
// Returns an error if the config contains entries incompatible with the backend.
func BuildHostEnv(hc RuntimeEntry, backend string) ([]runtime.EnvEntry, error) {
	switch backend {
	case "kubernetes":
		entries := make([]runtime.EnvEntry, 0, len(hc.Kubernetes.Env))
		for _, e := range hc.Kubernetes.Env {
			entry := runtime.EnvEntry{Name: e.Name, Value: e.Value}
			if e.SecretRef != nil {
				entry.SecretRef = &runtime.SecretRef{Name: e.SecretRef.Name, Key: e.SecretRef.Key}
			}
			entries = append(entries, entry)
		}
		return entries, nil
	default: // local
		entries := make([]runtime.EnvEntry, 0, len(hc.Env))
		for k, v := range hc.Env {
			entries = append(entries, runtime.EnvEntry{Name: k, Value: v})
		}
		if len(hc.Kubernetes.Env) > 0 {
			return entries, fmt.Errorf(
				"host config has kubernetes.env entries but backend is %q; "+
					"kubernetes.env is ignored for local execution — use env instead", backend)
		}
		return entries, nil
	}
}
