package contract

import (
	"testing"
)

func TestParseBinding_MutualExclusion(t *testing.T) {
	yaml := `
source_bindings:
  events:
    backend: mcp
    mcp_tool: test_tool
    response_format: csv
    response_adapter: tabular_text
`
	_, err := ParseBinding([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for both response_format and response_adapter set")
	}
	if !contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %v", err)
	}
}

func TestParseBinding_ResponseFormatOnly(t *testing.T) {
	yaml := `
source_bindings:
  events:
    backend: mcp
    mcp_tool: test_tool
    response_format: csv
`
	bs, err := ParseBinding([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs.SourceBindings["events"].ResponseFormat != "csv" {
		t.Errorf("expected response_format=csv, got %q", bs.SourceBindings["events"].ResponseFormat)
	}
}

func TestParseBinding_ResponseAdapterOnly(t *testing.T) {
	yaml := `
source_bindings:
  events:
    backend: mcp
    mcp_tool: test_tool
    response_adapter: tabular_text
`
	bs, err := ParseBinding([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs.SourceBindings["events"].ResponseAdapter != "tabular_text" {
		t.Errorf("expected response_adapter=tabular_text, got %q", bs.SourceBindings["events"].ResponseAdapter)
	}
}

func TestParseBinding_NeitherSet(t *testing.T) {
	yaml := `
source_bindings:
  events:
    backend: mcp
    mcp_tool: test_tool
`
	_, err := ParseBinding([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseBinding_UnknownResponseFormat(t *testing.T) {
	yaml := `
source_bindings:
  events:
    backend: mcp
    mcp_tool: test_tool
    response_format: cvs
`
	_, err := ParseBinding([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown response_format")
	}
	if !contains(err.Error(), "unknown response_format") {
		t.Errorf("expected 'unknown response_format' error, got: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
