package schema

import (
	"strings"
	"testing"
)

func TestGenerateIndexCypher(t *testing.T) {
	reg := loadMergedSchema(t)

	stmts := reg.GenerateIndexCypher()
	if len(stmts) == 0 {
		t.Fatal("no index statements generated")
	}

	// Check that DomainSource.name index is generated.
	found := false
	for _, s := range stmts {
		if strings.Contains(s, "DomainSource") && strings.Contains(s, "n.name") {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing CREATE INDEX for DomainSource.name")
	}

	// Check unique constraint for Semantic.name.
	found = false
	for _, s := range stmts {
		if strings.Contains(s, "CONSTRAINT") && strings.Contains(s, "Semantic") && strings.Contains(s, "n.name") {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing CREATE CONSTRAINT for Semantic.name")
	}

	// All statements should start with CREATE
	for _, s := range stmts {
		if !strings.HasPrefix(s, "CREATE ") {
			t.Errorf("unexpected statement: %s", s)
		}
	}
}

func TestGenerateSeedCypher(t *testing.T) {
	reg := loadMergedSchema(t)

	stmts := reg.GenerateSeedCypher()
	if len(stmts) != 23 {
		t.Errorf("seed statement count = %d, want 23", len(stmts))
	}

	// All should be MERGE statements.
	for _, s := range stmts {
		if !strings.HasPrefix(s, "MERGE") {
			t.Errorf("unexpected seed statement: %s", s)
		}
	}

	// Check ip_address is present.
	found := false
	for _, s := range stmts {
		if strings.Contains(s, "ip_address") {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing MERGE for ip_address")
	}
}
