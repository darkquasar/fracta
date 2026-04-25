package loaders

import (
	"strings"
	"testing"
)

func TestInterpolateSimple_Basic(t *testing.T) {
	tmpl := `userIdentity.arn:{{identity_arn}} AND eventTime:[{{start_time}} TO {{end_time}}]`
	params := map[string]any{
		"identity_arn": "arn:aws:iam::123:user/alice",
		"start_time":   "2026-03-25T00:00:00Z",
		"end_time":     "2026-03-26T00:00:00Z",
	}

	result, err := InterpolateSimple(tmpl, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "arn:aws:iam::123:user/alice") {
		t.Errorf("expected interpolated ARN, got: %s", result)
	}
	if strings.Contains(result, "{{") {
		t.Errorf("expected no remaining placeholders, got: %s", result)
	}
}

func TestInterpolateSimple_MissingParam(t *testing.T) {
	tmpl := `field:{{missing_param}}`
	params := map[string]any{}

	_, err := InterpolateSimple(tmpl, params)
	if err == nil {
		t.Fatal("expected error for unresolved placeholder")
	}
	if !strings.Contains(err.Error(), "missing_param") {
		t.Errorf("expected error to mention missing_param, got: %v", err)
	}
}

func TestInterpolateSimple_NumericValues(t *testing.T) {
	tmpl := `count:{{num}} enabled:{{flag}}`
	params := map[string]any{
		"num":  42,
		"flag": true,
	}

	result, err := InterpolateSimple(tmpl, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "count:42") {
		t.Errorf("expected count:42, got: %s", result)
	}
	if !strings.Contains(result, "enabled:true") {
		t.Errorf("expected enabled:true, got: %s", result)
	}
}
