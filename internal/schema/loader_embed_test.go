package schema

import (
	"testing"
)

// TestLoadSchemaSet_Embed_AllFamilies confirms every family baked into
// EmbeddedFS loads cleanly via the resolver chain (URI → resolve.Parse → Open
// → LoadSchemaSet). If a new family is added under internal/schema/graph-schema/,
// add it to this table.
func TestLoadSchemaSet_Embed_AllFamilies(t *testing.T) {
	cases := []struct {
		uri  string
		name string
	}{
		{"embed://graph-schema/core", "core"},
		{"embed://graph-schema/threat-hunting", "threat-hunting"},
		{"embed://graph-schema/fracta-mcp-gateway", "fracta-mcp-gateway"},
		{"embed://graph-schema/knowledge-garden", "knowledge-garden"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			ss, err := LoadSchemaSetFromURI(c.uri)
			if err != nil {
				t.Fatalf("LoadSchemaSetFromURI(%q): %v", c.uri, err)
			}
			if ss.Name != c.name {
				t.Errorf("Name = %q, want %q", ss.Name, c.name)
			}
			if ss.Registry == nil {
				t.Error("nil Registry")
			}
		})
	}
}

// TestLoadSchemaSet_Embed_MergesCleanly confirms all four embedded families
// merge without conflict — same invariant as TestMergedSchema_ThreeWay but
// driven through the embed resolver.
func TestLoadSchemaSet_Embed_MergesCleanly(t *testing.T) {
	uris := []string{
		"embed://graph-schema/core",
		"embed://graph-schema/threat-hunting",
		"embed://graph-schema/fracta-mcp-gateway",
		"embed://graph-schema/knowledge-garden",
	}
	sets := make([]*SchemaSet, 0, len(uris))
	for _, uri := range uris {
		ss, err := LoadSchemaSetFromURI(uri)
		if err != nil {
			t.Fatalf("loading %s: %v", uri, err)
		}
		sets = append(sets, ss)
	}
	merged, err := MergeSchemas(sets...)
	if err != nil {
		t.Fatalf("MergeSchemas: %v", err)
	}
	if len(merged.Nodes) == 0 || len(merged.Edges) == 0 {
		t.Errorf("merged registry empty: %d nodes, %d edges", len(merged.Nodes), len(merged.Edges))
	}
}
