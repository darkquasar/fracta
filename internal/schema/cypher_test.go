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

	// All statements should be CREATE INDEX — unique constraints moved to
	// GenerateConstraints() because FalkorDB rejects the Cypher CONSTRAINT
	// syntax (it wants GRAPH.CONSTRAINT). Make sure none leaked into here.
	for _, s := range stmts {
		if !strings.HasPrefix(s, "CREATE INDEX") {
			t.Errorf("unexpected non-INDEX statement in GenerateIndexCypher: %s", s)
		}
	}
}

func TestGenerateConstraints(t *testing.T) {
	reg := loadMergedSchema(t)

	cs := reg.GenerateConstraints()
	if len(cs) == 0 {
		t.Fatal("no unique constraints generated")
	}

	// Check unique constraint for Semantic.name is present.
	found := false
	for _, c := range cs {
		if c.Label == "Semantic" && c.Property == "name" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing unique constraint for Semantic.name")
	}

	// Output must be deterministic (sorted by label, then property).
	for i := 1; i < len(cs); i++ {
		if cs[i-1].Label > cs[i].Label {
			t.Errorf("constraint output not sorted by label: %s before %s", cs[i-1].Label, cs[i].Label)
		}
		if cs[i-1].Label == cs[i].Label && cs[i-1].Property > cs[i].Property {
			t.Errorf("constraint output not sorted by property within label %s: %s before %s",
				cs[i].Label, cs[i-1].Property, cs[i].Property)
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
