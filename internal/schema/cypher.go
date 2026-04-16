package schema

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateIndexCypher returns CREATE INDEX and CREATE CONSTRAINT statements
// for all node types that have indexes or unique properties.
func (r *SchemaRegistry) GenerateIndexCypher() []string {
	var stmts []string

	// Sort labels for deterministic output.
	labels := make([]string, 0, len(r.Nodes))
	for label := range r.Nodes {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		nt := r.Nodes[label]

		// Generate indexes from the indexes list.
		for _, idx := range nt.Indexes {
			if len(idx.Properties) == 0 {
				continue
			}
			// FalkorDB supports single-property indexes.
			// For multi-property indexes, create one per property.
			for _, prop := range idx.Properties {
				stmt := fmt.Sprintf("CREATE INDEX FOR (n:%s) ON (n.%s)", label, prop)
				stmts = append(stmts, stmt)
			}
		}

		// Generate unique constraints for properties marked unique.
		propNames := sortedPropNames(nt.Properties)
		for _, name := range propNames {
			prop := nt.Properties[name]
			if prop.Unique {
				stmt := fmt.Sprintf("CREATE CONSTRAINT ON (n:%s) ASSERT n.%s IS UNIQUE", label, name)
				stmts = append(stmts, stmt)
			}
		}
	}

	return stmts
}

// GenerateSeedCypher returns MERGE statements for Semantic vocabulary nodes.
func (r *SchemaRegistry) GenerateSeedCypher() []string {
	var stmts []string

	// Sort for deterministic output.
	names := make([]string, 0, len(r.Semantics))
	for name := range r.Semantics {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sem := r.Semantics[name]
		desc := strings.ReplaceAll(sem.Description, "'", "\\'")

		stmt := fmt.Sprintf(
			"MERGE (s:Semantic {name: '%s'}) SET s.description = '%s'",
			name, desc,
		)
		stmts = append(stmts, stmt)
	}

	return stmts
}

func sortedPropNames(props map[string]PropertyDef) []string {
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
