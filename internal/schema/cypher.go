package schema

import (
	"fmt"
	"sort"
	"strings"
)

// UniqueConstraint describes a single-property uniqueness rule.
// FalkorDB enforces these via the GRAPH.CONSTRAINT Redis command, not Cypher,
// so we return structured records and let the graph client issue the right
// command — see (*graph.FalkorDBClient).CreateUniqueConstraint.
type UniqueConstraint struct {
	Label    string
	Property string
}

// GenerateIndexCypher returns CREATE INDEX statements for all node types that
// declare indexes. FalkorDB supports single-property indexes only; multi-prop
// index blocks are expanded one statement per property.
//
// Unique constraints are NOT emitted here — they go through GenerateConstraints
// because FalkorDB rejects the Neo4j-style CREATE CONSTRAINT ... ASSERT syntax
// inside GRAPH.QUERY. See GenerateConstraints + (*FalkorDBClient).CreateUniqueConstraint.
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

		for _, idx := range nt.Indexes {
			if len(idx.Properties) == 0 {
				continue
			}
			for _, prop := range idx.Properties {
				stmt := fmt.Sprintf("CREATE INDEX FOR (n:%s) ON (n.%s)", label, prop)
				stmts = append(stmts, stmt)
			}
		}
	}

	return stmts
}

// GenerateConstraints returns the set of unique constraints declared by the
// schema. The caller is responsible for issuing the FalkorDB-specific
// GRAPH.CONSTRAINT CREATE command for each entry.
func (r *SchemaRegistry) GenerateConstraints() []UniqueConstraint {
	var out []UniqueConstraint

	labels := make([]string, 0, len(r.Nodes))
	for label := range r.Nodes {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		nt := r.Nodes[label]
		for _, name := range sortedPropNames(nt.Properties) {
			if nt.Properties[name].Unique {
				out = append(out, UniqueConstraint{Label: label, Property: name})
			}
		}
	}

	return out
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
