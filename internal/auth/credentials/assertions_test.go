package credentials

import (
	"testing"
)

func TestValidateAssertions_RequireEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		require   []string
		wantError bool
	}{
		{
			name:      "all required present",
			env:       map[string]string{"AWS_REGION": "us-east-1", "FOO": "bar"},
			require:   []string{"AWS_REGION", "FOO"},
			wantError: false,
		},
		{
			name:      "one missing",
			env:       map[string]string{"AWS_REGION": "us-east-1"},
			require:   []string{"AWS_REGION", "MISSING"},
			wantError: true,
		},
		{
			name:      "present but empty",
			env:       map[string]string{"AWS_REGION": ""},
			require:   []string{"AWS_REGION"},
			wantError: true,
		},
		{
			name:      "no requirements",
			env:       map[string]string{},
			require:   nil,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertions := &CredentialAssertions{RequireEnv: tt.require}
			diags := ValidateAssertions(assertions, tt.env, nil)
			if HasErrors(diags) != tt.wantError {
				t.Errorf("HasErrors = %v, want %v; diags: %+v", HasErrors(diags), tt.wantError, diags)
			}
		})
	}
}

func TestValidateAssertions_ForbidEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		forbid    []string
		wantError bool
	}{
		{
			name:      "forbidden not present",
			env:       map[string]string{"AWS_REGION": "us-east-1"},
			forbid:    []string{"CLAUDE_CODE_SIMPLE"},
			wantError: false,
		},
		{
			name:      "forbidden is present",
			env:       map[string]string{"CLAUDE_CODE_SIMPLE": "1"},
			forbid:    []string{"CLAUDE_CODE_SIMPLE"},
			wantError: true,
		},
		{
			name:      "forbidden present with empty value still fails",
			env:       map[string]string{"CLAUDE_CODE_SIMPLE": ""},
			forbid:    []string{"CLAUDE_CODE_SIMPLE"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertions := &CredentialAssertions{ForbidEnv: tt.forbid}
			diags := ValidateAssertions(assertions, tt.env, nil)
			if HasErrors(diags) != tt.wantError {
				t.Errorf("HasErrors = %v, want %v; diags: %+v", HasErrors(diags), tt.wantError, diags)
			}
		})
	}
}

func TestValidateAssertions_RequireSource(t *testing.T) {
	availableSources := []AnnotatedSource{
		{Name: "proxy", Phase: PhaseRuntimeOnly},
		{Name: "host_fallback", Phase: PhasePrepareNow},
	}
	unavailableSources := []AnnotatedSource{
		{Name: "proxy", Phase: PhaseRuntimeOnly},
		{Name: "host_fallback", Phase: PhaseUnavailable},
	}

	tests := []struct {
		name      string
		sources   []AnnotatedSource
		require   []string
		wantError bool
	}{
		{
			name:      "required source available (prepare_now)",
			sources:   availableSources,
			require:   []string{"host_fallback"},
			wantError: false,
		},
		{
			name:      "required source available (runtime_only counts)",
			sources:   availableSources,
			require:   []string{"proxy"},
			wantError: false,
		},
		{
			name:      "required source unavailable",
			sources:   unavailableSources,
			require:   []string{"host_fallback"},
			wantError: true,
		},
		{
			name:      "required source does not exist",
			sources:   availableSources,
			require:   []string{"nonexistent"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertions := &CredentialAssertions{RequireSource: tt.require}
			diags := ValidateAssertions(assertions, nil, tt.sources)
			if HasErrors(diags) != tt.wantError {
				t.Errorf("HasErrors = %v, want %v; diags: %+v", HasErrors(diags), tt.wantError, diags)
			}
		})
	}
}

func TestValidateAssertions_WarnIfMissing(t *testing.T) {
	assertions := &CredentialAssertions{WarnIfMissing: []string{"OPTIONAL_VAR"}}

	// Missing — should produce warning, NOT error.
	diags := ValidateAssertions(assertions, map[string]string{}, nil)
	if HasErrors(diags) {
		t.Errorf("expected no errors for warn_if_missing, got: %+v", diags)
	}
	hasWarning := false
	for _, d := range diags {
		if d.Severity == SeverityWarning {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected warning diagnostic for missing optional var")
	}

	// Present — should produce info, no warning.
	diags = ValidateAssertions(assertions, map[string]string{"OPTIONAL_VAR": "value"}, nil)
	if HasErrors(diags) {
		t.Errorf("expected no errors when optional var is present")
	}
	for _, d := range diags {
		if d.Severity == SeverityWarning {
			t.Errorf("unexpected warning when optional var is present: %+v", d)
		}
	}
}

func TestValidateAssertions_NilAssertions(t *testing.T) {
	diags := ValidateAssertions(nil, map[string]string{"FOO": "bar"}, nil)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for nil assertions, got %d", len(diags))
	}
}

func TestValidateAssertions_Combined(t *testing.T) {
	assertions := &CredentialAssertions{
		RequireEnv:    []string{"AWS_REGION"},
		ForbidEnv:     []string{"CLAUDE_CODE_SIMPLE"},
		WarnIfMissing: []string{"OPTIONAL"},
	}
	env := map[string]string{
		"AWS_REGION": "ap-southeast-2",
	}

	diags := ValidateAssertions(assertions, env, nil)
	if HasErrors(diags) {
		t.Errorf("expected no errors for valid combined assertions, got: %+v", diags)
	}

	// Now add the forbidden var.
	env["CLAUDE_CODE_SIMPLE"] = "1"
	diags = ValidateAssertions(assertions, env, nil)
	if !HasErrors(diags) {
		t.Errorf("expected error when forbidden env is present")
	}
}
