package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkquasar/fracta/internal/schema/embedfs"
)

// loadEmbeddedFamily loads a schema set out of EmbeddedFS by family name.
// Family tests now route through the embed:// resolver chain — there is no
// repo-root graph-schema/ directory to walk; the canonical location is
// internal/schema/graph-schema/ which the //go:embed directive baked in.
func loadEmbeddedFamily(t *testing.T, family string) *SchemaSet {
	t.Helper()
	ss, err := LoadSchemaSet(embedfs.FS, "graph-schema/"+family)
	if err != nil {
		t.Fatalf("LoadSchemaSet(embed graph-schema/%s): %v", family, err)
	}
	return ss
}

// loadMergedSchema loads both core and threat-hunting schema sets and merges them.
func loadMergedSchema(t *testing.T) *SchemaRegistry {
	t.Helper()
	coreSet := loadEmbeddedFamily(t, "core")
	thSet := loadEmbeddedFamily(t, "threat-hunting")

	merged, err := MergeSchemas(coreSet, thSet)
	if err != nil {
		t.Fatalf("MergeSchemas: %v", err)
	}
	return merged
}

func TestLoadSchemaSet_Core(t *testing.T) {
	ss := loadEmbeddedFamily(t, "core")
	if ss.Name != "core" {
		t.Errorf("Name = %q, want 'core'", ss.Name)
	}
	if ss.Version != 2 {
		t.Errorf("Version = %d, want 2", ss.Version)
	}
	if ss.Description == "" {
		t.Error("Description is empty")
	}
	// Core: 6 nodes (Semantic, MCPField, MCPTool, MCPServer, DataStore, DomainSource)
	if got := len(ss.Registry.Nodes); got != 6 {
		t.Errorf("core node count = %d, want 6", got)
	}
	// Core: 5 edges (RETURNS_FIELD, MAPS_TO, PROVIDES, QUERYABLE_VIA, STORED_IN)
	if got := len(ss.Registry.Edges); got != 5 {
		t.Errorf("core edge count = %d, want 5", got)
	}

	// Authority section
	if ss.Authority == nil {
		t.Fatal("Authority is nil")
	}
	inv := ss.Authority["inventory"]
	if !contains(inv, "MCPTool") || !contains(inv, "MCPServer") {
		t.Errorf("inventory = %v, want [MCPTool, MCPServer]", inv)
	}
	scaf := ss.Authority["scaffold"]
	if !contains(scaf, "DomainSource") || !contains(scaf, "DataStore") ||
		!contains(scaf, "MCPField") || !contains(scaf, "Semantic") {
		t.Errorf("scaffold = %v, want [DomainSource, DataStore, MCPField, Semantic]", scaf)
	}
}

func TestLoadSchemaSet_ThreatHunting(t *testing.T) {
	ss := loadEmbeddedFamily(t, "threat-hunting")
	if ss.Name != "threat-hunting" {
		t.Errorf("Name = %q, want 'threat-hunting'", ss.Name)
	}
	if ss.Version != 2 {
		t.Errorf("Version = %d, want 2", ss.Version)
	}
	// Threat-hunting: 5 universal (FieldType, Strategy, StrategyColumn, StrategyVersion, StrategyRun)
	//               + 9 particular = 14 nodes
	// (LogSource moved to core as DomainSource)
	if got := len(ss.Registry.Nodes); got != 14 {
		t.Errorf("threat-hunting node count = %d, want 14", got)
	}
	// Threat-hunting: 22 edges (QUERYABLE_VIA moved to core)
	if got := len(ss.Registry.Edges); got != 22 {
		t.Errorf("threat-hunting edge count = %d, want 22", got)
	}
	// Should contain StrategyVersion and StrategyRun
	if _, ok := ss.Registry.Nodes["StrategyVersion"]; !ok {
		t.Error("missing node: StrategyVersion")
	}
	if _, ok := ss.Registry.Nodes["StrategyRun"]; !ok {
		t.Error("missing node: StrategyRun")
	}
	if _, ok := ss.Registry.Edges["HAS_VERSION"]; !ok {
		t.Error("missing edge: HAS_VERSION")
	}
	if _, ok := ss.Registry.Edges["HAS_RUN"]; !ok {
		t.Error("missing edge: HAS_RUN")
	}

	// Authority section
	if ss.Authority == nil {
		t.Fatal("Authority is nil")
	}
	scaf := ss.Authority["scaffold"]
	if !contains(scaf, "FieldType") || !contains(scaf, "Strategy") {
		t.Errorf("scaffold = %v, want FieldType and Strategy", scaf)
	}
	comp := ss.Authority["computed"]
	if !contains(comp, "StrategyRun") {
		t.Errorf("computed = %v, want [StrategyRun]", comp)
	}
	disc := ss.Authority["discovered"]
	if !contains(disc, "Hunt") || !contains(disc, "Event") {
		t.Errorf("discovered = %v, want Hunt and Event", disc)
	}
}

func TestMergedSchema(t *testing.T) {
	reg := loadMergedSchema(t)

	if reg.Version != 2 {
		t.Errorf("Version = %d, want 2", reg.Version)
	}

	// Semantics: 23 (all in threat-hunting, core has empty vocab)
	if len(reg.Semantics) != 23 {
		t.Errorf("Semantics count = %d, want 23", len(reg.Semantics))
	}
	if _, ok := reg.Semantics["ip_address"]; !ok {
		t.Error("missing semantic: ip_address")
	}
	if sem := reg.Semantics["ip_address"]; sem.Description == "" {
		t.Error("ip_address.Description is empty")
	}

	// Universal nodes: 6 core + 5 threat-hunting = 11
	universalLabels := []string{
		"Semantic", "MCPField", "MCPTool", "MCPServer", "DataStore", "DomainSource",
		"FieldType", "Strategy", "StrategyColumn",
		"StrategyVersion", "StrategyRun",
	}
	for _, label := range universalLabels {
		nt, ok := reg.Nodes[label]
		if !ok {
			t.Errorf("missing universal node: %s", label)
			continue
		}
		if nt.Layer != "universal" {
			t.Errorf("node %s: layer = %q, want 'universal'", label, nt.Layer)
		}
	}

	// Particular nodes: 9
	particularLabels := []string{
		"IP", "Identity", "Session", "System", "Event",
		"Investigation", "Finding", "Hunt", "ExpectedObservation",
	}
	for _, label := range particularLabels {
		nt, ok := reg.Nodes[label]
		if !ok {
			t.Errorf("missing particular node: %s", label)
			continue
		}
		if nt.Layer != "particular" {
			t.Errorf("node %s: layer = %q, want 'particular'", label, nt.Layer)
		}
	}

	// Total: 11 universal + 9 particular = 20
	if got := len(reg.Nodes); got != 20 {
		t.Errorf("node count = %d, want 20", got)
	}

	// Edges: 5 core + 22 threat-hunting = 27
	expectedEdges := []string{
		"PROVIDES", "RETURNS_FIELD", "MAPS_TO", "QUERYABLE_VIA", "STORED_IN",
		"HAS_FIELD", "JOINS_WITH",
		"USES_SOURCE", "USES_TOOL", "EXPECTS_COLUMN",
		"INSTANCE_OF", "OBSERVED_IN", "ASSUMED", "ACCESSED",
		"INVOLVES", "PRODUCED_BY", "CORRELATES_WITH", "EXPECTS",
		"HAS_VERSION", "HAS_RUN",
		"DETECTED_ON", "EXFILS_TO", "AFFECTS_IDENTITY",
		"HAS_EVENT", "TARGETS", "USES", "POTENTIALLY_RELATED",
	}
	for _, edgeType := range expectedEdges {
		if _, ok := reg.Edges[edgeType]; !ok {
			t.Errorf("missing edge type: %s", edgeType)
		}
	}
	if got := len(reg.Edges); got != 27 {
		t.Errorf("edge count = %d, want 27", got)
	}
}

func TestMergedSchema_Properties(t *testing.T) {
	reg := loadMergedSchema(t)

	// DomainSource.name should be required + unique.
	ds := reg.Nodes["DomainSource"]
	nameProp := ds.Properties["name"]
	if !nameProp.Required {
		t.Error("DomainSource.name should be required")
	}
	if !nameProp.Unique {
		t.Error("DomainSource.name should be unique")
	}

	// MCPTool.name should be required + unique.
	mt := reg.Nodes["MCPTool"]
	mtName := mt.Properties["name"]
	if !mtName.Required {
		t.Error("MCPTool.name should be required")
	}
	if !mtName.Unique {
		t.Error("MCPTool.name should be unique")
	}

	// DataStore.uri should be required + unique.
	dstore := reg.Nodes["DataStore"]
	uriProp := dstore.Properties["uri"]
	if !uriProp.Required {
		t.Error("DataStore.uri should be required")
	}
	if !uriProp.Unique {
		t.Error("DataStore.uri should be unique")
	}

	// FieldType.semantic should have ref: semantics
	ft := reg.Nodes["FieldType"]
	semProp := ft.Properties["semantic"]
	if semProp.Ref != "semantics" {
		t.Errorf("FieldType.semantic.Ref = %q, want 'semantics'", semProp.Ref)
	}

	// EXPECTS_COLUMN.table should be required
	ec := reg.Edges["EXPECTS_COLUMN"]
	tableProp := ec.Properties["table"]
	if !tableProp.Required {
		t.Error("EXPECTS_COLUMN.table should be required")
	}
}

func TestMergedSchema_EdgeEndpoints(t *testing.T) {
	reg := loadMergedSchema(t)

	// HAS_FIELD: DomainSource -> FieldType
	hf := reg.Edges["HAS_FIELD"]
	if !contains(hf.From, "DomainSource") {
		t.Error("HAS_FIELD.From should contain DomainSource")
	}
	if !contains(hf.To, "FieldType") {
		t.Error("HAS_FIELD.To should contain FieldType")
	}

	// USES_SOURCE: [Strategy, Hunt] -> [DomainSource]
	us := reg.Edges["USES_SOURCE"]
	if !contains(us.From, "Strategy") || !contains(us.From, "Hunt") {
		t.Errorf("USES_SOURCE.From = %v, want [Strategy, Hunt]", us.From)
	}
	if !contains(us.To, "DomainSource") {
		t.Error("USES_SOURCE.To should contain DomainSource")
	}

	// USES_TOOL: Strategy -> MCPTool
	ut := reg.Edges["USES_TOOL"]
	if !contains(ut.From, "Strategy") {
		t.Error("USES_TOOL.From should contain Strategy")
	}
	if !contains(ut.To, "MCPTool") {
		t.Error("USES_TOOL.To should contain MCPTool")
	}

	// PROVIDES: MCPServer -> MCPTool
	prov := reg.Edges["PROVIDES"]
	if !contains(prov.From, "MCPServer") {
		t.Error("PROVIDES.From should contain MCPServer")
	}
	if !contains(prov.To, "MCPTool") {
		t.Error("PROVIDES.To should contain MCPTool")
	}

	// QUERYABLE_VIA: DataStore -> MCPServer
	qv := reg.Edges["QUERYABLE_VIA"]
	if !contains(qv.From, "DataStore") {
		t.Error("QUERYABLE_VIA.From should contain DataStore")
	}
	if !contains(qv.To, "MCPServer") {
		t.Error("QUERYABLE_VIA.To should contain MCPServer")
	}

	// STORED_IN: DomainSource -> DataStore
	si := reg.Edges["STORED_IN"]
	if !contains(si.From, "DomainSource") {
		t.Error("STORED_IN.From should contain DomainSource")
	}
	if !contains(si.To, "DataStore") {
		t.Error("STORED_IN.To should contain DataStore")
	}

	// RETURNS_FIELD: MCPTool -> MCPField
	rf := reg.Edges["RETURNS_FIELD"]
	if !contains(rf.From, "MCPTool") {
		t.Error("RETURNS_FIELD.From should contain MCPTool")
	}
	if !contains(rf.To, "MCPField") {
		t.Error("RETURNS_FIELD.To should contain MCPField")
	}

	// HAS_VERSION: Strategy -> StrategyVersion
	hv := reg.Edges["HAS_VERSION"]
	if !contains(hv.From, "Strategy") {
		t.Error("HAS_VERSION.From should contain Strategy")
	}
	if !contains(hv.To, "StrategyVersion") {
		t.Error("HAS_VERSION.To should contain StrategyVersion")
	}

	// HAS_RUN: StrategyVersion -> StrategyRun
	hr := reg.Edges["HAS_RUN"]
	if !contains(hr.From, "StrategyVersion") {
		t.Error("HAS_RUN.From should contain StrategyVersion")
	}
	if !contains(hr.To, "StrategyRun") {
		t.Error("HAS_RUN.To should contain StrategyRun")
	}
}

func TestMergeSchemas_DuplicateNodeLabel(t *testing.T) {
	set1 := &SchemaSet{
		Name:    "a",
		Version: 1,
		Registry: &SchemaRegistry{
			Nodes:     map[string]*NodeTypeDef{"Foo": {Label: "Foo", Layer: "universal"}},
			Edges:     map[string]*EdgeTypeDef{},
			Semantics: map[string]*SemanticDef{},
		},
	}
	set2 := &SchemaSet{
		Name:    "b",
		Version: 1,
		Registry: &SchemaRegistry{
			Nodes:     map[string]*NodeTypeDef{"Foo": {Label: "Foo", Layer: "universal"}},
			Edges:     map[string]*EdgeTypeDef{},
			Semantics: map[string]*SemanticDef{},
		},
	}
	_, err := MergeSchemas(set1, set2)
	if err == nil || !strings.Contains(err.Error(), "duplicate node label") {
		t.Errorf("expected duplicate node error, got: %v", err)
	}
}

func TestMergeSchemas_DuplicateSemanticOK(t *testing.T) {
	set1 := &SchemaSet{
		Name:    "a",
		Version: 1,
		Registry: &SchemaRegistry{
			Nodes:     map[string]*NodeTypeDef{},
			Edges:     map[string]*EdgeTypeDef{},
			Semantics: map[string]*SemanticDef{"ip_address": {Name: "ip_address", Description: "An IP"}},
		},
	}
	set2 := &SchemaSet{
		Name:    "b",
		Version: 1,
		Registry: &SchemaRegistry{
			Nodes:     map[string]*NodeTypeDef{},
			Edges:     map[string]*EdgeTypeDef{},
			Semantics: map[string]*SemanticDef{"ip_address": {Name: "ip_address", Description: "An IP address"}},
		},
	}
	merged, err := MergeSchemas(set1, set2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(merged.Semantics) != 1 {
		t.Errorf("expected 1 semantic after merge, got %d", len(merged.Semantics))
	}
}

func TestLoadSchema_InvalidVersion(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte("version: 999\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "semantics.yaml"), []byte("vocabulary: []\n"), 0o644)

	_, err := LoadSchema(dir)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected version error, got: %v", err)
	}
}

func TestLoadSchema_InvalidEdgeEndpoint(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte("version: 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "semantics.yaml"), []byte("vocabulary: []\n"), 0o644)
	if err := os.MkdirAll(filepath.Join(dir, "edges"), 0o755); err != nil {
		t.Fatalf("mkdir edges: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "edges", "bad.yaml"), []byte(
		"type: BAD_EDGE\nendpoints:\n  from: [NonExistent]\n  to: [AlsoFake]\nproperties: {}\n",
	), 0o644)

	_, err := LoadSchema(dir)
	if err == nil || !strings.Contains(err.Error(), "not defined as node type") {
		t.Errorf("expected cross-validation error, got: %v", err)
	}
}

func TestLoadSchemaSet_GatewayFamily(t *testing.T) {
	ss := loadEmbeddedFamily(t, "fracta-mcp-gateway")
	if ss.Name != "fracta-mcp-gateway" {
		t.Errorf("Name = %q, want 'fracta-mcp-gateway'", ss.Name)
	}
	if ss.Version != 1 {
		t.Errorf("Version = %d, want 1", ss.Version)
	}
	// No nodes or edges in this family
	if got := len(ss.Registry.Nodes); got != 0 {
		t.Errorf("gateway node count = %d, want 0", got)
	}
	if got := len(ss.Registry.Edges); got != 0 {
		t.Errorf("gateway edge count = %d, want 0", got)
	}
	// 8 checkpoint rules (5 original + 3 authority-aware)
	if got := len(ss.Checkpoint); got != 8 {
		t.Errorf("gateway checkpoint count = %d, want 8", got)
	}
	// Verify specific rule names
	ruleNames := make(map[string]bool)
	for _, r := range ss.Checkpoint {
		ruleNames[r.Name] = true
	}
	expected := []string{
		"mcptool_missing_provenance",
		"mcptool_name_not_namespaced",
		"stale_null_source_inventory",
		"orphaned_legacy_toolref",
		"datastore_pending_uri",
		"inventory_wrong_source",
		"scaffold_inventory_source",
		"inventory_pending_migration",
	}
	for _, name := range expected {
		if !ruleNames[name] {
			t.Errorf("missing checkpoint rule: %s", name)
		}
	}
}

func TestLoadSchemaSet_AuthorityValidation(t *testing.T) {
	t.Run("invalid category", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte(
			"name: test\nversion: 2\ndescription: test\nauthority:\n  magical:\n    - Foo\n",
		), 0o644)
		os.WriteFile(filepath.Join(dir, "semantics.yaml"), []byte("vocabulary: []\n"), 0o644)

		_, err := LoadSchemaSet(os.DirFS(dir), ".")
		if err == nil || !strings.Contains(err.Error(), "unknown category") {
			t.Errorf("expected unknown category error, got: %v", err)
		}
	})

	t.Run("duplicate label across categories", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte(
			"name: test\nversion: 2\ndescription: test\nauthority:\n  inventory:\n    - Foo\n  scaffold:\n    - Foo\n",
		), 0o644)
		os.WriteFile(filepath.Join(dir, "semantics.yaml"), []byte("vocabulary: []\n"), 0o644)

		_, err := LoadSchemaSet(os.DirFS(dir), ".")
		if err == nil || !strings.Contains(err.Error(), "appears in both") {
			t.Errorf("expected duplicate label error, got: %v", err)
		}
	})

	t.Run("no authority is ok", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "_meta.yaml"), []byte(
			"name: test\nversion: 1\ndescription: test\n",
		), 0o644)
		os.WriteFile(filepath.Join(dir, "semantics.yaml"), []byte("vocabulary: []\n"), 0o644)

		ss, err := LoadSchemaSet(os.DirFS(dir), ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ss.Authority == nil {
			t.Error("Authority should be empty map, not nil")
		}
		if len(ss.Authority) != 0 {
			t.Errorf("Authority should be empty, got %v", ss.Authority)
		}
	})
}

func TestMergedSchema_ThreeWay(t *testing.T) {
	coreSet := loadEmbeddedFamily(t, "core")
	thSet := loadEmbeddedFamily(t, "threat-hunting")
	gwSet := loadEmbeddedFamily(t, "fracta-mcp-gateway")

	merged, err := MergeSchemas(coreSet, thSet, gwSet)
	if err != nil {
		t.Fatalf("MergeSchemas: %v", err)
	}

	// Same node/edge counts as 2-way merge (gateway has no nodes/edges)
	if got := len(merged.Nodes); got != 20 {
		t.Errorf("node count = %d, want 20", got)
	}
	if got := len(merged.Edges); got != 27 {
		t.Errorf("edge count = %d, want 27", got)
	}
}

func TestLoadSchemaSet_KnowledgeGarden(t *testing.T) {
	ss := loadEmbeddedFamily(t, "knowledge-garden")
	if ss.Name != "knowledge-garden" {
		t.Errorf("Name = %q, want 'knowledge-garden'", ss.Name)
	}
	if ss.Version != 2 {
		t.Errorf("Version = %d, want 2", ss.Version)
	}
	// 1 universal (Topic) + 7 particulars (Document, Highlight, Concept, Entity, Claim, Question, Publication) = 8 nodes
	if got := len(ss.Registry.Nodes); got != 8 {
		t.Errorf("knowledge-garden node count = %d, want 8", got)
	}
	// 9 edges: MENTIONS, EVIDENCES, CONTRADICTS, REFINES, PART_OF, AUTHORED_BY, CAPTURED_FROM, RELATES_TO, PUBLISHED_AS
	if got := len(ss.Registry.Edges); got != 9 {
		t.Errorf("knowledge-garden edge count = %d, want 9", got)
	}
	expectedNodes := []string{"Topic", "Document", "Highlight", "Concept", "Entity", "Claim", "Question", "Publication"}
	for _, label := range expectedNodes {
		if _, ok := ss.Registry.Nodes[label]; !ok {
			t.Errorf("missing node: %s", label)
		}
	}
	expectedEdges := []string{"MENTIONS", "EVIDENCES", "CONTRADICTS", "REFINES", "PART_OF", "AUTHORED_BY", "CAPTURED_FROM", "RELATES_TO", "PUBLISHED_AS"}
	for _, label := range expectedEdges {
		if _, ok := ss.Registry.Edges[label]; !ok {
			t.Errorf("missing edge: %s", label)
		}
	}

	// Authority section
	if ss.Authority == nil {
		t.Fatal("Authority is nil")
	}
	scaf := ss.Authority["scaffold"]
	if !contains(scaf, "Topic") {
		t.Errorf("scaffold = %v, want Topic", scaf)
	}
	disc := ss.Authority["discovered"]
	for _, label := range []string{"Document", "Highlight", "Concept", "Entity", "Claim", "Question", "Publication"} {
		if !contains(disc, label) {
			t.Errorf("discovered missing %s; got %v", label, disc)
		}
	}
}
