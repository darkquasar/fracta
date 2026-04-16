package schema

// PropertyDef describes a single property on a node or edge type.
type PropertyDef struct {
	Name        string
	Type        string // string, int, float, bool, string_list
	Required    bool
	Unique      bool
	Description string
	Ref         string // cross-reference (e.g., "semantics")
}

// IndexDef describes an index on a node type.
type IndexDef struct {
	Properties []string
	Type       string // exact, fulltext, vector
}

// NodeTypeDef describes a node type loaded from YAML.
type NodeTypeDef struct {
	Label       string
	Layer       string // universal, particular
	Description string
	Properties  map[string]PropertyDef
	Indexes     []IndexDef
}

// EdgeTypeDef describes an edge type loaded from YAML.
type EdgeTypeDef struct {
	Type        string
	Description string
	From        []string // allowed source labels
	To          []string // allowed target labels
	Properties  map[string]PropertyDef
}

// SemanticDef describes a semantic vocabulary entry.
type SemanticDef struct {
	Name        string
	Description string
}

// CheckpointRule describes a single graph validation rule loaded from checkpoint.yaml.
type CheckpointRule struct {
	Name            string
	Layer           string // "universal" or "particular"
	Severity        string // "error" or "warning"
	Query           string // Cypher that returns rows = gaps
	GapType         string
	GapDescription  string // template with {column} placeholders
	SuggestedAction string // template with {column} placeholders
}

// SchemaSet wraps a SchemaRegistry with ontology identity, authority map, and checkpoint rules.
type SchemaSet struct {
	Name        string
	Version     int
	Description string
	Authority   map[string][]string // category -> []label (e.g., "inventory" -> ["MCPTool", "MCPServer"])
	Registry    *SchemaRegistry
	Checkpoint  []CheckpointRule
}

// SchemaRegistry holds the complete graph schema loaded from YAML.
type SchemaRegistry struct {
	Version   int
	Nodes     map[string]*NodeTypeDef // label -> definition
	Edges     map[string]*EdgeTypeDef // type -> definition
	Semantics map[string]*SemanticDef // name -> definition
}

// ValidSemantics returns the set of valid semantic names.
func (r *SchemaRegistry) ValidSemantics() map[string]bool {
	result := make(map[string]bool, len(r.Semantics))
	for name := range r.Semantics {
		result[name] = true
	}
	return result
}
