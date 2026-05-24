package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// RAGContext holds the structured graph context for an agent prompt.
type RAGContext struct {
	DomainSources []Record // All domain sources with their fields and joins
	Strategies    []Record // Strategies matching the relevant semantics
	PriorHunts    []Record // Past hunts involving the given entities
}

// String serializes the RAG context as structured text for LLM injection.
func (r RAGContext) String() string {
	var b strings.Builder

	b.WriteString("=== Graph Context ===\n\n")

	if len(r.DomainSources) > 0 {
		b.WriteString("## Domain Sources & Join Paths\n")
		for _, rec := range r.DomainSources {
			fmt.Fprintf(&b, "- %v (field: %v) joins with %v.%v [confidence: %v, method: %v]\n",
				rec["source"], rec["field"], rec["target"], rec["target_field"],
				rec["confidence"], rec["method"])
		}
		b.WriteString("\n")
	}

	if len(r.Strategies) > 0 {
		b.WriteString("## Relevant Strategies\n")
		for _, rec := range r.Strategies {
			fmt.Fprintf(&b, "- %v: %v (sources: %v)\n",
				rec["name"], rec["description"], rec["sources"])
		}
		b.WriteString("\n")
	}

	if len(r.PriorHunts) > 0 {
		b.WriteString("## Prior Hunts\n")
		for _, rec := range r.PriorHunts {
			fmt.Fprintf(&b, "- %v [%v]: %v\n",
				rec["id"], rec["status"], rec["hypothesis"])
		}
		b.WriteString("\n")
	}

	if len(r.DomainSources) == 0 && len(r.Strategies) == 0 && len(r.PriorHunts) == 0 {
		b.WriteString("(no relevant context found)\n")
	}

	return b.String()
}

// GraphRAGContext retrieves graph context relevant to a set of semantic types.
// This is the Phase 1-2 implementation: exact/tag matching only.
// Semantics are validated by the graph itself; unknown values return empty results.
func GraphRAGContext(ctx context.Context, client GraphClient, semantics []string) (*RAGContext, error) {
	rc := &RAGContext{}

	// 1. Domain sources and join paths for the given semantics.
	if len(semantics) > 0 {
		// Build a Cypher IN clause with quoted values.
		// Safe because all values are validated against the closed vocabulary above.
		quoted := make([]string, len(semantics))
		for i, s := range semantics {
			quoted[i] = fmt.Sprintf("'%s'", s)
		}
		inClause := strings.Join(quoted, ", ")

		joinQuery := fmt.Sprintf(`
			MATCH (d:DomainSource)-[:HAS_FIELD]->(f1:FieldType)-[j:JOINS_WITH]->(f2:FieldType)<-[:HAS_FIELD]-(d2:DomainSource)
			WHERE f1.semantic IN [%s] OR f2.semantic IN [%s]
			RETURN d.name AS source, f1.name AS field, f2.name AS target_field,
			       d2.name AS target, j.confidence AS confidence, j.method AS method
		`, inClause, inClause)
		recs, err := client.Query(ctx, joinQuery, nil)
		if err != nil {
			return nil, fmt.Errorf("query join paths: %w", err)
		}
		rc.DomainSources = recs
	}

	// 2. Strategies that use domain sources with matching field semantics.
	if len(semantics) > 0 {
		quoted := make([]string, len(semantics))
		for i, s := range semantics {
			quoted[i] = fmt.Sprintf("'%s'", s)
		}
		inClause := strings.Join(quoted, ", ")

		stratQuery := fmt.Sprintf(`
			MATCH (s:Strategy)-[:USES_SOURCE]->(d:DomainSource)-[:HAS_FIELD]->(f:FieldType)
			WHERE f.semantic IN [%s]
			RETURN DISTINCT s.name AS name, s.description AS description,
			       collect(DISTINCT d.name) AS sources
		`, inClause)
		recs, err := client.Query(ctx, stratQuery, nil)
		if err != nil {
			return nil, fmt.Errorf("query strategies: %w", err)
		}
		rc.Strategies = recs
	}

	// 3. Prior hunts (entities matched by value — placeholder for now).
	// Full entity extraction from hypothesis text is a Phase 2+ feature.
	// For now, return all active hunts as context.
	huntQuery := `
		MATCH (h:Hunt)
		WHERE h.status <> 'closed'
		RETURN h.id AS id, h.hypothesis AS hypothesis, h.status AS status
		LIMIT 10
	`
	recs, err := client.Query(ctx, huntQuery, nil)
	if err != nil {
		// Hunts may not exist yet; treat as empty rather than error.
		fractalog.Component("rag").Debug("hunt query skipped", "error", err)
		rc.PriorHunts = nil
	} else {
		rc.PriorHunts = recs
	}

	return rc, nil
}
