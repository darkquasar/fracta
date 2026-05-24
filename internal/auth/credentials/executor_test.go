package credentials

import (
	"context"
	"testing"
)

func TestExecuteCredentialPlan_RuntimeOnlyPassthrough(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:       "proxy",
				Phase:      PhaseRuntimeOnly,
				AuthOrigin: &CredentialSource{Type: "http_header_token", Scope: "agent_runtime"},
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{"AWS_REGION": "us-east-1"},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// runtime_only source should produce a diagnostic but no artifacts.
	foundRuntimeDiag := false
	for _, d := range output.Diagnostics {
		if d.Fields["source_name"] == "proxy" && d.Fields["phase"] == "runtime_only" {
			foundRuntimeDiag = true
		}
	}
	if !foundRuntimeDiag {
		t.Error("expected runtime_only diagnostic for proxy source")
	}
	if len(output.SecretData) != 0 {
		t.Errorf("expected no secret data for runtime_only sources, got %d", len(output.SecretData))
	}
}

func TestExecuteCredentialPlan_UnavailablePassthrough(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:       "host_fallback",
				Phase:      PhaseUnavailable,
				AuthOrigin: &CredentialSource{Type: "command_output", Scope: "host_edge"},
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyInCluster,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundUnavailDiag := false
	for _, d := range output.Diagnostics {
		if d.Fields["source_name"] == "host_fallback" && d.Fields["phase"] == "unavailable" {
			foundUnavailDiag = true
		}
	}
	if !foundUnavailDiag {
		t.Error("expected unavailable diagnostic for host_fallback source")
	}
}

func TestExecuteCredentialPlan_PreMaterialized(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:             "host_fallback",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge", Path: "/var/run/auth/token"},
				MaterializedData: []byte("pre-staged-token"),
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pre-materialized data should end up in SecretData.
	if string(output.SecretData["token"]) != "pre-staged-token" {
		t.Errorf("expected pre-staged-token in SecretData[token], got %q", string(output.SecretData["token"]))
	}
	if output.MountPath != "/var/run/auth" {
		t.Errorf("MountPath = %q, want /var/run/auth", output.MountPath)
	}
}

func TestExecuteCredentialPlan_DryRun(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "host_fallback",
				Phase: PhasePrepareNow,
				AuthOrigin: &CredentialSource{
					Type:    "command_output",
					Scope:   "host_edge",
					Command: []string{"/nonexistent/command"},
					Path:    "/var/run/auth/token",
				},
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("dry-run should not fail even with nonexistent command: %v", err)
	}

	foundDryRunDiag := false
	for _, d := range output.Diagnostics {
		if d.Fields["dry_run"] == "true" {
			foundDryRunDiag = true
		}
	}
	if !foundDryRunDiag {
		t.Error("expected dry-run diagnostic")
	}

	// Dry-run should not produce actual artifacts.
	if len(output.SecretData) != 0 {
		t.Errorf("dry-run should not produce secret data, got %d entries", len(output.SecretData))
	}
}

func TestExecuteCredentialPlan_RequiredSourceFailure(t *testing.T) {
	required := true
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "required_cmd",
				Phase: PhasePrepareNow,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Command:  []string{"/nonexistent/required-command"},
					Required: &required,
				},
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{},
	}

	_, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err == nil {
		t.Fatal("expected error for required source failure")
	}
}

func TestExecuteCredentialPlan_OptionalSourceFailureContinues(t *testing.T) {
	optional := false
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "optional_cmd",
				Phase: PhasePrepareNow,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Command:  []string{"/nonexistent/optional-command"},
					Required: &optional,
				},
			},
		},
		Binding: &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:     map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("optional source failure should not cause error: %v", err)
	}

	// Should have a warning diagnostic.
	foundWarning := false
	for _, d := range output.Diagnostics {
		if d.Severity == SeverityWarning && d.Fields["source_name"] == "optional_cmd" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected warning diagnostic for optional source failure")
	}
}

func TestExecuteCredentialPlan_AssertionFailure(t *testing.T) {
	plan := &CredentialPlan{
		Profile:     "test",
		AuthOrigins: []AnnotatedSource{},
		Binding:     &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:         map[string]string{"CLAUDE_CODE_SIMPLE": "1"},
		Assertions: &CredentialAssertions{
			ForbidEnv: []string{"CLAUDE_CODE_SIMPLE"},
		},
	}

	_, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err == nil {
		t.Fatal("expected error when assertion fails")
	}
}

func TestExecuteCredentialPlan_EnvEntries(t *testing.T) {
	plan := &CredentialPlan{
		Profile:     "test",
		AuthOrigins: []AnnotatedSource{},
		Binding:     &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env: map[string]string{
			"AWS_REGION":                    "ap-southeast-2",
			"CLAUDE_CODE_USE_BEDROCK":       "1",
			"CLAUDE_CODE_SKIP_BEDROCK_AUTH": "1",
		},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range output.EnvEntries {
		envMap[e.Name] = e.Value
	}

	if envMap["AWS_REGION"] != "ap-southeast-2" {
		t.Errorf("AWS_REGION = %q, want ap-southeast-2", envMap["AWS_REGION"])
	}
	if envMap["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q, want 1", envMap["CLAUDE_CODE_USE_BEDROCK"])
	}
}

func TestExecuteCredentialPlan_BearerEnvPassthrough(t *testing.T) {
	plan := &CredentialPlan{
		Profile:     "test",
		AuthOrigins: []AnnotatedSource{},
		Binding: &CredentialBinding{
			Type:    "bearer_env",
			EnvName: "OPENAI_API_KEY",
		},
		Env: map[string]string{
			"OPENAI_API_KEY": "host-token",
		},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range output.EnvEntries {
		envMap[e.Name] = e.Value
	}

	if envMap["OPENAI_API_KEY"] != "host-token" {
		t.Errorf("OPENAI_API_KEY = %q, want host-token", envMap["OPENAI_API_KEY"])
	}
}

func TestExecuteCredentialPlan_BearerEnvResolverSingleMaterializedSource(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:             "host_fallback",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge"},
				MaterializedData: []byte("resolver-token"),
			},
		},
		RuntimeAuthResolver: &CredentialResolver{
			Type:    "command",
			Command: "/bin/helper",
		},
		Binding: &CredentialBinding{
			Type:                "bearer_env",
			RuntimeAuthResolver: "helper",
			EnvName:             "API_TOKEN",
		},
		Env: map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range output.EnvEntries {
		envMap[e.Name] = e.Value
	}
	if envMap["API_TOKEN"] != "resolver-token" {
		t.Errorf("API_TOKEN = %q, want resolver-token", envMap["API_TOKEN"])
	}
}

func TestExecuteCredentialPlan_BearerEnvResolverMultipleSourcesUsesDeprecatedOrder(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:             "first",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge"},
				MaterializedData: []byte("first-token"),
			},
			{
				Name:             "second",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge"},
				MaterializedData: []byte("second-token"),
			},
		},
		RuntimeAuthResolver: &CredentialResolver{
			Type:    "command",
			Command: "/bin/helper",
			Order:   []string{"second", "first"},
		},
		Binding: &CredentialBinding{
			Type:                "bearer_env",
			RuntimeAuthResolver: "helper",
			EnvName:             "API_TOKEN",
		},
		Env: map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range output.EnvEntries {
		envMap[e.Name] = e.Value
	}
	if envMap["API_TOKEN"] != "second-token" {
		t.Errorf("API_TOKEN = %q, want second-token", envMap["API_TOKEN"])
	}

	foundWarning := false
	for _, d := range output.Diagnostics {
		if d.Severity == SeverityWarning && d.Fields["deprecated_field"] == "resolver.order" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected deprecation warning for resolver.order")
	}
}

func TestExecuteCredentialPlan_BearerEnvResolverMultipleSourcesWithoutOrderIsAmbiguous(t *testing.T) {
	plan := &CredentialPlan{
		Profile: "test",
		AuthOrigins: []AnnotatedSource{
			{
				Name:             "first",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge"},
				MaterializedData: []byte("first-token"),
			},
			{
				Name:             "second",
				Phase:            PhasePrepareNow,
				AuthOrigin:       &CredentialSource{Type: "command_output", Scope: "host_edge"},
				MaterializedData: []byte("second-token"),
			},
		},
		RuntimeAuthResolver: &CredentialResolver{
			Type:    "command",
			Command: "/bin/helper",
		},
		Binding: &CredentialBinding{
			Type:                "bearer_env",
			RuntimeAuthResolver: "helper",
			EnvName:             "API_TOKEN",
		},
		Env: map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := make(map[string]string)
	for _, e := range output.EnvEntries {
		envMap[e.Name] = e.Value
	}
	if _, ok := envMap["API_TOKEN"]; ok {
		t.Errorf("API_TOKEN = %q, want unset for ambiguous resolver binding", envMap["API_TOKEN"])
	}

	foundWarning := false
	for _, d := range output.Diagnostics {
		if d.Severity == SeverityWarning && d.Stage == "binding.project" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected ambiguity warning for bearer_env resolver without order")
	}
}

func TestExecuteCredentialPlan_PlanBackref(t *testing.T) {
	plan := &CredentialPlan{
		Profile:     "test",
		AuthOrigins: []AnnotatedSource{},
		Binding:     &CredentialBinding{Type: "claude_api_key_helper", RuntimeAuthResolver: "r"},
		Env:         map[string]string{},
	}

	output, err := ExecuteCredentialPlan(context.Background(), plan, PlanContext{
		Topology: TopologyHostEdge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output.Plan != plan {
		t.Error("output.Plan should reference the input plan")
	}
}
