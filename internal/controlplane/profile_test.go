package controlplane

import (
	"testing"

	"github.com/darkquasar/fracta/internal/config"
)

func TestResolveProfile_PVCMountPath(t *testing.T) {
	tests := []struct {
		name         string
		pvcMountPath string
		wantBase     string
	}{
		{
			name:         "derives workspace base from pvc_mount_path",
			pvcMountPath: "/workspace",
			wantBase:     "/workspace/agents",
		},
		{
			name:     "uses profile default when pvc_mount_path empty",
			wantBase: "/workspace/agents", // kubernetes profile default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Profile: "kubernetes",
				Runtime: config.RuntimeConfig{
					Backend: "kubernetes",
					Kubernetes: config.KubernetesConfig{
						PVCMountPath: tt.pvcMountPath,
					},
					State: config.StateConfig{Path: "/workspace/.fracta/state.db"},
				},
			}

			p := ResolveProfile(cfg, "")
			if p.WorkspaceBase != tt.wantBase {
				t.Errorf("WorkspaceBase = %q, want %q", p.WorkspaceBase, tt.wantBase)
			}
		})
	}
}

func TestResolveProfile_BackendType(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		backend     string
		wantBackend string
	}{
		{
			name:        "kubernetes profile sets kubernetes backend",
			profile:     "kubernetes",
			wantBackend: "kubernetes",
		},
		{
			name:        "local profile sets local backend",
			profile:     "local",
			wantBackend: "local",
		},
		{
			name:        "runtime.backend overrides profile default",
			profile:     "local",
			backend:     "kubernetes",
			wantBackend: "kubernetes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Profile: tt.profile,
				Runtime: config.RuntimeConfig{
					Backend: tt.backend,
				},
			}

			p := ResolveProfile(cfg, "/tmp/test")
			if p.BackendType != tt.wantBackend {
				t.Errorf("BackendType = %q, want %q", p.BackendType, tt.wantBackend)
			}
		})
	}
}
