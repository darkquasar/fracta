package graph

import "testing"

func TestValidateIdentifier(t *testing.T) {
	valid := []string{
		"LogSource",
		"my_field",
		"A",
		"_private",
		"node123",
	}
	for _, name := range valid {
		if err := ValidateIdentifier(name, "test"); err != nil {
			t.Errorf("ValidateIdentifier(%q) unexpected error: %v", name, err)
		}
	}

	invalid := []struct {
		input string
		desc  string
	}{
		{"Foo} DELETE", "injection with closing brace"},
		{"X:Admin)", "injection with colon and paren"},
		{"a b", "space in identifier"},
		{"", "empty string"},
		{"123start", "starts with digit"},
		{"name; DROP", "semicolon injection"},
	}
	for _, tt := range invalid {
		if err := ValidateIdentifier(tt.input, "test"); err == nil {
			t.Errorf("ValidateIdentifier(%q) [%s] expected error, got nil", tt.input, tt.desc)
		}
	}
}

func TestValidateEdgeTypes(t *testing.T) {
	if err := ValidateEdgeTypes([]string{"HAS_FIELD", "JOINS_WITH"}); err != nil {
		t.Errorf("ValidateEdgeTypes valid types: %v", err)
	}

	if err := ValidateEdgeTypes([]string{"HAS_FIELD", "BAD TYPE"}); err == nil {
		t.Error("ValidateEdgeTypes with space expected error, got nil")
	}

	// Empty slice is valid.
	if err := ValidateEdgeTypes(nil); err != nil {
		t.Errorf("ValidateEdgeTypes(nil): %v", err)
	}
}
