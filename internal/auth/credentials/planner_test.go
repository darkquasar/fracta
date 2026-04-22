package credentials

import (
	"testing"

	"github.com/darkquasar/fracta/internal/runtime"
)

func TestBuildCredentialPlan_PhaseAnnotation(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins: map[string]CredentialSource{
			"proxy": {
				Type:  "http_header_token",
				Scope: "agent_runtime",
			},
			"host_fallback": {
				Type:     "command_output",
				Scope:    "host_edge",
				Command:  []string{"bedrock-auth-helper"},
				Delivery: "file_mount",
				Path:     "/var/run/fracta-auth/bedrock-token",
			},
			"universal": {
				Type:  "command_output",
				Scope: "any",
			},
		},
		RuntimeAuthResolvers: map[string]CredentialResolver{
			"bedrock_helper": {
				Type:    "command",
				Command: "/usr/local/bin/fetch-bedrock-token",
				TTLMs:   60000,
				Order:   []string{"proxy", "host_fallback", "universal"},
			},
		},
		DefaultBinding: &CredentialBinding{
			Type:     "claude_api_key_helper",
			RuntimeAuthResolver: "bedrock_helper",
		},
	}

	tests := []struct {
		name       string
		topology   Topology
		wantPhases map[string]ExecutionPhase
	}{
		{
			name:     "host_edge topology",
			topology: TopologyHostEdge,
			wantPhases: map[string]ExecutionPhase{
				"proxy":         PhaseRuntimeOnly,
				"host_fallback": PhasePrepareNow,
				"universal":     PhasePrepareNow,
			},
		},
		{
			name:     "in_cluster topology",
			topology: TopologyInCluster,
			wantPhases: map[string]ExecutionPhase{
				"proxy":         PhaseRuntimeOnly,
				"host_fallback": PhaseUnavailable,
				"universal":     PhasePrepareNow,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildCredentialPlan("bedrock", profile, nil, nil, PlanContext{
				Topology: tt.topology,
			})
			if err != nil {
				t.Fatalf("BuildCredentialPlan failed: %v", err)
			}

			if len(plan.AuthOrigins) != 3 {
				t.Fatalf("expected 3 sources (no filtering), got %d", len(plan.AuthOrigins))
			}

			for _, src := range plan.AuthOrigins {
				wantPhase, ok := tt.wantPhases[src.Name]
				if !ok {
					t.Errorf("unexpected source %q", src.Name)
					continue
				}
				if src.Phase != wantPhase {
					t.Errorf("source %q: phase = %q, want %q", src.Name, src.Phase, wantPhase)
				}
			}
		})
	}
}

func TestBuildCredentialPlan_DeprecatedOrderStillBuilds(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins: map[string]CredentialSource{
			"c": {Type: "command_output", Scope: "any"},
			"a": {Type: "command_output", Scope: "any"},
			"b": {Type: "command_output", Scope: "any"},
		},
		RuntimeAuthResolvers: map[string]CredentialResolver{
			"r": {
				Type:    "command",
				Command: "/bin/helper",
				Order:   []string{"b", "a", "c"},
			},
		},
		DefaultBinding: &CredentialBinding{
			Type:     "claude_api_key_helper",
			RuntimeAuthResolver: "r",
		},
	}

	plan, err := BuildCredentialPlan("test", profile, nil, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("BuildCredentialPlan failed: %v", err)
	}

	if len(plan.AuthOrigins) != 3 {
		t.Fatalf("len(plan.AuthOrigins) = %d, want 3", len(plan.AuthOrigins))
	}

	seen := make(map[string]bool)
	for _, src := range plan.AuthOrigins {
		seen[src.Name] = true
	}
	for _, name := range []string{"a", "b", "c"} {
		if !seen[name] {
			t.Errorf("expected source %q to be present in plan", name)
		}
	}
}

func TestBuildCredentialPlan_HostBindingOverride(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins: map[string]CredentialSource{
			"proxy": {Type: "http_header_token", Scope: "agent_runtime"},
		},
		RuntimeAuthResolvers: map[string]CredentialResolver{
			"helper": {Type: "command", Command: "/bin/helper"},
		},
		DefaultBinding: &CredentialBinding{
			Type:     "claude_api_key_helper",
			RuntimeAuthResolver: "helper",
		},
	}

	hostBinding := &CredentialBinding{
		Type:    "bearer_env",
		AuthOrigin: "proxy",
		EnvName: "MY_TOKEN",
	}

	plan, err := BuildCredentialPlan("test", profile, hostBinding, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("BuildCredentialPlan failed: %v", err)
	}

	if plan.Binding.Type != "bearer_env" {
		t.Errorf("binding type = %q, want bearer_env (host override)", plan.Binding.Type)
	}
	if plan.RuntimeAuthResolver != nil {
		t.Errorf("resolver should be nil when binding has no resolver ref")
	}
}

func TestBuildCredentialPlan_EnvMerging(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins: map[string]CredentialSource{},
		RuntimeAuthResolvers: map[string]CredentialResolver{
			"r": {Type: "command", Command: "/bin/helper"},
		},
		Env: map[string]string{
			"PROFILE_VAR": "from_profile",
			"SHARED_VAR":  "from_profile",
		},
		DefaultBinding: &CredentialBinding{
			Type:     "claude_api_key_helper",
			RuntimeAuthResolver: "r",
		},
	}

	hostEnv := []runtime.EnvEntry{
		{Name: "HOST_VAR", Value: "from_host"},
		{Name: "SHARED_VAR", Value: "from_host"},
	}

	plan, err := BuildCredentialPlan("test", profile, nil, hostEnv, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("BuildCredentialPlan failed: %v", err)
	}

	if plan.Env["PROFILE_VAR"] != "from_profile" {
		t.Errorf("PROFILE_VAR = %q, want from_profile", plan.Env["PROFILE_VAR"])
	}
	if plan.Env["HOST_VAR"] != "from_host" {
		t.Errorf("HOST_VAR = %q, want from_host", plan.Env["HOST_VAR"])
	}
	if plan.Env["SHARED_VAR"] != "from_host" {
		t.Errorf("SHARED_VAR = %q, want from_host (host overrides profile)", plan.Env["SHARED_VAR"])
	}
}

func TestBuildCredentialPlan_NilProfile(t *testing.T) {
	_, err := BuildCredentialPlan("test", nil, nil, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err == nil {
		t.Fatal("expected error for nil profile")
	}
}

func TestBuildCredentialPlan_NoBinding(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins:          map[string]CredentialSource{},
		RuntimeAuthResolvers: map[string]CredentialResolver{},
		// No DefaultBinding, no host override.
	}

	_, err := BuildCredentialPlan("test", profile, nil, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err == nil {
		t.Fatal("expected error when no binding is available")
	}
}

func TestBuildCredentialPlan_UnknownResolver(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins:          map[string]CredentialSource{},
		RuntimeAuthResolvers: map[string]CredentialResolver{},
		DefaultBinding: &CredentialBinding{
			Type:                "claude_api_key_helper",
			RuntimeAuthResolver: "nonexistent",
		},
	}

	_, err := BuildCredentialPlan("test", profile, nil, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err == nil {
		t.Fatal("expected error for unknown resolver reference")
	}
}

func TestBuildCredentialPlan_DefaultBindingFallback(t *testing.T) {
	profile := &CredentialProfile{
		AuthOrigins: map[string]CredentialSource{
			"s": {Type: "command_output", Scope: "any"},
		},
		RuntimeAuthResolvers: map[string]CredentialResolver{
			"r": {Type: "command", Command: "/bin/r"},
		},
		DefaultBinding: &CredentialBinding{
			Type:     "claude_api_key_helper",
			RuntimeAuthResolver: "r",
		},
	}

	plan, err := BuildCredentialPlan("test", profile, nil, nil, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Binding.Type != "claude_api_key_helper" {
		t.Errorf("binding type = %q, want claude_api_key_helper from default", plan.Binding.Type)
	}
}

func TestAnnotatePhase(t *testing.T) {
	tests := []struct {
		scope    Scope
		topology Topology
		want     ExecutionPhase
	}{
		{ScopeAgentRuntime, TopologyHostEdge, PhaseRuntimeOnly},
		{ScopeAgentRuntime, TopologyInCluster, PhaseRuntimeOnly},
		{ScopeHostEdge, TopologyHostEdge, PhasePrepareNow},
		{ScopeHostEdge, TopologyInCluster, PhaseUnavailable},
		{ScopeAny, TopologyHostEdge, PhasePrepareNow},
		{ScopeAny, TopologyInCluster, PhasePrepareNow},
		{Scope("unknown"), TopologyHostEdge, PhaseUnavailable},
	}

	for _, tt := range tests {
		t.Run(string(tt.scope)+"_"+string(tt.topology), func(t *testing.T) {
			got := annotatePhase(tt.scope, tt.topology)
			if got != tt.want {
				t.Errorf("annotatePhase(%q, %q) = %q, want %q", tt.scope, tt.topology, got, tt.want)
			}
		})
	}
}
