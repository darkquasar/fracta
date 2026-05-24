package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAML intermediate types for unmarshalling.

type yamlMeta struct {
	Name        string              `yaml:"name"`
	Version     int                 `yaml:"version"`
	Description string              `yaml:"description"`
	Authority   map[string][]string `yaml:"authority"` // category -> []label
}

type yamlSemanticEntry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type yamlSemantics struct {
	Vocabulary []yamlSemanticEntry `yaml:"vocabulary"`
}

type yamlProperty struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Unique      bool   `yaml:"unique"`
	Description string `yaml:"description"`
	Ref         string `yaml:"ref"`
}

type yamlIndex struct {
	Properties []string `yaml:"properties"`
	Type       string   `yaml:"type"`
}

type yamlNode struct {
	Label       string                  `yaml:"label"`
	Layer       string                  `yaml:"layer"`
	Description string                  `yaml:"description"`
	Properties  map[string]yamlProperty `yaml:"properties"`
	Indexes     []yamlIndex             `yaml:"indexes"`
}

type yamlEndpoints struct {
	From []string `yaml:"from"`
	To   []string `yaml:"to"`
}

type yamlEdge struct {
	Type        string                  `yaml:"type"`
	Description string                  `yaml:"description"`
	Endpoints   yamlEndpoints           `yaml:"endpoints"`
	Properties  map[string]yamlProperty `yaml:"properties"`
}

const supportedVersion = 2

// validAuthorityCategories defines the set of recognized authority categories.
var validAuthorityCategories = map[string]bool{
	"inventory":  true,
	"scaffold":   true,
	"discovered": true,
	"computed":   true,
}

// LoadSchema reads a graph-schema/ directory and returns a SchemaRegistry.
func LoadSchema(dir string) (*SchemaRegistry, error) {
	reg := &SchemaRegistry{
		Nodes:     make(map[string]*NodeTypeDef),
		Edges:     make(map[string]*EdgeTypeDef),
		Semantics: make(map[string]*SemanticDef),
	}

	// 1. Read _meta.yaml
	if _, err := loadMeta(dir, reg); err != nil {
		return nil, err
	}

	// 2. Read semantics.yaml
	if err := loadSemantics(dir, reg); err != nil {
		return nil, err
	}

	// 3. Read nodes/*.yaml
	if err := loadNodeDir(filepath.Join(dir, "nodes"), "universal", reg); err != nil {
		return nil, err
	}

	// 4. Read particulars/*.yaml
	if err := loadNodeDir(filepath.Join(dir, "particulars"), "particular", reg); err != nil {
		return nil, err
	}

	// 5. Read edges/*.yaml
	if err := loadEdgeDir(filepath.Join(dir, "edges"), reg); err != nil {
		return nil, err
	}

	// 6. Cross-validate
	if err := crossValidate(reg); err != nil {
		return nil, err
	}

	return reg, nil
}

func loadMeta(dir string, reg *SchemaRegistry) (*yamlMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "_meta.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading _meta.yaml: %w", err)
	}
	var meta yamlMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing _meta.yaml: %w", err)
	}
	if meta.Version > supportedVersion {
		return nil, fmt.Errorf("schema version %d not supported (max %d)", meta.Version, supportedVersion)
	}
	reg.Version = meta.Version
	return &meta, nil
}

func loadSemantics(dir string, reg *SchemaRegistry) error {
	data, err := os.ReadFile(filepath.Join(dir, "semantics.yaml"))
	if err != nil {
		return fmt.Errorf("reading semantics.yaml: %w", err)
	}
	var sem yamlSemantics
	if err := yaml.Unmarshal(data, &sem); err != nil {
		return fmt.Errorf("parsing semantics.yaml: %w", err)
	}
	for _, entry := range sem.Vocabulary {
		if entry.Name == "" {
			return fmt.Errorf("semantics.yaml: entry with empty name")
		}
		if _, exists := reg.Semantics[entry.Name]; exists {
			return fmt.Errorf("semantics.yaml: duplicate entry %q", entry.Name)
		}
		reg.Semantics[entry.Name] = &SemanticDef{
			Name:        entry.Name,
			Description: entry.Description,
		}
	}
	return nil
}

func loadNodeDir(dir, expectedLayer string, reg *SchemaRegistry) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var yn yamlNode
		if err := yaml.Unmarshal(data, &yn); err != nil {
			return fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		if yn.Label == "" {
			return fmt.Errorf("%s: missing label", entry.Name())
		}
		if yn.Layer != expectedLayer {
			return fmt.Errorf("%s: expected layer %q, got %q", entry.Name(), expectedLayer, yn.Layer)
		}
		if _, exists := reg.Nodes[yn.Label]; exists {
			return fmt.Errorf("duplicate node label %q", yn.Label)
		}

		nt := &NodeTypeDef{
			Label:       yn.Label,
			Layer:       yn.Layer,
			Description: yn.Description,
			Properties:  make(map[string]PropertyDef, len(yn.Properties)),
		}
		for name, yp := range yn.Properties {
			nt.Properties[name] = PropertyDef{
				Name:        name,
				Type:        yp.Type,
				Required:    yp.Required,
				Unique:      yp.Unique,
				Description: yp.Description,
				Ref:         yp.Ref,
			}
		}
		for _, yi := range yn.Indexes {
			nt.Indexes = append(nt.Indexes, IndexDef(yi))
		}
		reg.Nodes[yn.Label] = nt
	}
	return nil
}

func loadEdgeDir(dir string, reg *SchemaRegistry) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var ye yamlEdge
		if err := yaml.Unmarshal(data, &ye); err != nil {
			return fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		if ye.Type == "" {
			return fmt.Errorf("%s: missing type", entry.Name())
		}
		if _, exists := reg.Edges[ye.Type]; exists {
			return fmt.Errorf("duplicate edge type %q", ye.Type)
		}

		et := &EdgeTypeDef{
			Type:        ye.Type,
			Description: ye.Description,
			From:        ye.Endpoints.From,
			To:          ye.Endpoints.To,
			Properties:  make(map[string]PropertyDef, len(ye.Properties)),
		}
		for name, yp := range ye.Properties {
			et.Properties[name] = PropertyDef{
				Name:        name,
				Type:        yp.Type,
				Required:    yp.Required,
				Unique:      yp.Unique,
				Description: yp.Description,
				Ref:         yp.Ref,
			}
		}
		reg.Edges[ye.Type] = et
	}
	return nil
}

// LoadSchemaSet reads a schema set directory (with _meta.yaml containing name/description),
// loads the schema registry and optional checkpoint rules. Cross-validation is NOT
// performed here because edge endpoints may reference nodes from other sets.
// Use MergeSchemas for cross-set validation after loading all sets.
func LoadSchemaSet(dir string) (*SchemaSet, error) {
	reg := &SchemaRegistry{
		Nodes:     make(map[string]*NodeTypeDef),
		Edges:     make(map[string]*EdgeTypeDef),
		Semantics: make(map[string]*SemanticDef),
	}

	meta, err := loadMeta(dir, reg)
	if err != nil {
		return nil, err
	}

	if err := loadSemantics(dir, reg); err != nil {
		return nil, err
	}
	if err := loadNodeDir(filepath.Join(dir, "nodes"), "universal", reg); err != nil {
		return nil, err
	}
	if err := loadNodeDir(filepath.Join(dir, "particulars"), "particular", reg); err != nil {
		return nil, err
	}
	if err := loadEdgeDir(filepath.Join(dir, "edges"), reg); err != nil {
		return nil, err
	}

	// No cross-validation here — edges may reference nodes from other sets.
	// MergeSchemas validates after union.

	rules, err := LoadCheckpointRules(dir)
	if err != nil {
		return nil, err
	}

	// Validate authority section if present.
	authority := meta.Authority
	if authority == nil {
		authority = make(map[string][]string)
	}
	if err := validateAuthority(authority); err != nil {
		return nil, fmt.Errorf("_meta.yaml authority: %w", err)
	}

	return &SchemaSet{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		Authority:   authority,
		Registry:    reg,
		Checkpoint:  rules,
	}, nil
}

// validateAuthority checks that authority categories are valid and no label appears
// in multiple categories.
func validateAuthority(authority map[string][]string) error {
	seen := make(map[string]string) // label -> category
	for category, labels := range authority {
		if !validAuthorityCategories[category] {
			return fmt.Errorf("unknown category %q (valid: inventory, scaffold, discovered, computed)", category)
		}
		for _, label := range labels {
			if prev, ok := seen[label]; ok {
				return fmt.Errorf("label %q appears in both %q and %q", label, prev, category)
			}
			seen[label] = category
		}
	}
	return nil
}

// MergeSchemas combines multiple SchemaSet registries into a single SchemaRegistry.
// Duplicate node labels or edge types across sets produce an error.
// Duplicate semantic names are silently merged (same concept).
// Cross-validation runs on the merged result so edges can reference nodes from other sets.
func MergeSchemas(sets ...*SchemaSet) (*SchemaRegistry, error) {
	merged := &SchemaRegistry{
		Nodes:     make(map[string]*NodeTypeDef),
		Edges:     make(map[string]*EdgeTypeDef),
		Semantics: make(map[string]*SemanticDef),
	}

	for _, ss := range sets {
		if ss.Version > merged.Version {
			merged.Version = ss.Version
		}

		for label, nt := range ss.Registry.Nodes {
			if _, exists := merged.Nodes[label]; exists {
				return nil, fmt.Errorf("duplicate node label %q across schema sets", label)
			}
			merged.Nodes[label] = nt
		}

		for edgeType, et := range ss.Registry.Edges {
			if _, exists := merged.Edges[edgeType]; exists {
				return nil, fmt.Errorf("duplicate edge type %q across schema sets", edgeType)
			}
			merged.Edges[edgeType] = et
		}

		for name, sem := range ss.Registry.Semantics {
			// Semantics may overlap — same name = same concept, silently merge
			if _, exists := merged.Semantics[name]; !exists {
				merged.Semantics[name] = sem
			}
		}
	}

	// Cross-validate on the merged result (edges may reference nodes from other sets)
	if err := crossValidate(merged); err != nil {
		return nil, err
	}

	return merged, nil
}

func crossValidate(reg *SchemaRegistry) error {
	// Validate semantic references in node properties
	for label, nt := range reg.Nodes {
		for name, prop := range nt.Properties {
			if prop.Ref == "semantics" {
				// This is valid -- it means the property value at runtime
				// must reference a valid semantic. Nothing to check at schema load.
				_ = name
				_ = label
			}
		}
	}

	// Validate edge endpoint labels exist as node types
	for edgeType, et := range reg.Edges {
		for _, fromLabel := range et.From {
			if _, ok := reg.Nodes[fromLabel]; !ok {
				return fmt.Errorf("edge %s: from label %q not defined as node type", edgeType, fromLabel)
			}
		}
		for _, toLabel := range et.To {
			if _, ok := reg.Nodes[toLabel]; !ok {
				return fmt.Errorf("edge %s: to label %q not defined as node type", edgeType, toLabel)
			}
		}
	}

	return nil
}
