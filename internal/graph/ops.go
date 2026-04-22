package graph

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ValidationError is returned when graph operation inputs are invalid.
// HTTP handlers should map this to 400 Bad Request, not 500.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// SchemaResult holds the structured output from a graph schema introspection.
type SchemaResult struct {
	Labels            []string
	RelationshipTypes []string
	PropertyKeys      []string
}

// reservedProvenanceKeys are parameter names injected by provenance tracking.
// If a user's params contain any of these, InjectProvenance returns an error.
var reservedProvenanceKeys = []string{"source", "confidence", "correlation_key", "updated_at"}

// InjectProvenance checks for reserved key conflicts and injects provenance
// fields into params. Returns the merged params map and any conflict error.
// If all provenance fields are empty, returns the original params unchanged.
func InjectProvenance(params map[string]any, source, confidence, correlationKey string) (map[string]any, error) {
	if source == "" && confidence == "" && correlationKey == "" {
		return params, nil
	}

	// Check for conflicts: user params must not contain reserved provenance keys.
	if params != nil {
		for _, key := range reservedProvenanceKeys {
			if _, exists := params[key]; exists {
				return nil, &ValidationError{
					Message: fmt.Sprintf("params must not contain reserved provenance key %q — use the dedicated parameter instead", key),
				}
			}
		}
	}

	// Copy-on-write: never mutate the caller's map.
	merged := make(map[string]any, len(params)+4)
	for k, v := range params {
		merged[k] = v
	}
	if source != "" {
		merged["source"] = source
		merged["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if confidence != "" {
		merged["confidence"] = confidence
	}
	if correlationKey != "" {
		merged["correlation_key"] = correlationKey
	}

	return merged, nil
}

// QuerySchema runs the three FalkorDB introspection queries and returns
// structured labels, relationship types, and property keys.
func QuerySchema(ctx context.Context, gc GraphClient) (*SchemaResult, error) {
	result := &SchemaResult{}

	labels, err := gc.Query(ctx, "CALL db.labels()", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching labels: %w", err)
	}
	for _, r := range labels {
		if v, ok := r["label"]; ok {
			result.Labels = append(result.Labels, fmt.Sprint(v))
		}
	}

	relTypes, err := gc.Query(ctx, "CALL db.relationshipTypes()", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching relationship types: %w", err)
	}
	for _, r := range relTypes {
		if v, ok := r["relationshipType"]; ok {
			result.RelationshipTypes = append(result.RelationshipTypes, fmt.Sprint(v))
		}
	}

	propKeys, err := gc.Query(ctx, "CALL db.propertyKeys()", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching property keys: %w", err)
	}
	for _, r := range propKeys {
		if v, ok := r["propertyKey"]; ok {
			result.PropertyKeys = append(result.PropertyKeys, fmt.Sprint(v))
		}
	}

	return result, nil
}

// BuildPathQuery validates identifiers and returns the Cypher + params for
// a shortest-path query. Caller executes via gc.Query().
func BuildPathQuery(fromLabel, fromKey, fromValue, toLabel, toKey, toValue string) (string, map[string]any, error) {
	if err := ValidateIdentifier(fromLabel, "label"); err != nil {
		return "", nil, err
	}
	if err := ValidateIdentifier(fromKey, "property key"); err != nil {
		return "", nil, err
	}
	if err := ValidateIdentifier(toLabel, "label"); err != nil {
		return "", nil, err
	}
	if err := ValidateIdentifier(toKey, "property key"); err != nil {
		return "", nil, err
	}

	cypher := fmt.Sprintf(
		"MATCH (a:%s {%s: $from_val}), (b:%s {%s: $to_val}), p = shortestPath((a)-[*]-(b)) RETURN p",
		fromLabel, fromKey, toLabel, toKey,
	)
	params := map[string]any{
		"from_val": fromValue,
		"to_val":   toValue,
	}

	return cypher, params, nil
}

// BuildNeighborsQuery validates identifiers/edge types and returns the Cypher +
// params for a neighborhood traversal. edgeTypes is pre-parsed ([]string).
// Depth values < 1 are silently coerced to 1 (matching current graph_tools.go behavior).
func BuildNeighborsQuery(label, key, value string, depth int, edgeTypes []string) (string, map[string]any, error) {
	if err := ValidateIdentifier(label, "label"); err != nil {
		return "", nil, err
	}
	if err := ValidateIdentifier(key, "property key"); err != nil {
		return "", nil, err
	}

	if depth < 1 {
		depth = 1
	}

	var relPattern string
	if len(edgeTypes) > 0 {
		if err := ValidateEdgeTypes(edgeTypes); err != nil {
			return "", nil, err
		}
		typed := make([]string, len(edgeTypes))
		for i, t := range edgeTypes {
			typed[i] = ":" + t
		}
		relPattern = fmt.Sprintf("[%s*1..%d]", strings.Join(typed, "|"), depth)
	} else {
		relPattern = fmt.Sprintf("[*1..%d]", depth)
	}

	cypher := fmt.Sprintf(
		"MATCH (n:%s {%s: $val})-%s-(m) RETURN DISTINCT labels(m) AS labels, properties(m) AS props",
		label, key, relPattern,
	)
	params := map[string]any{"val": value}

	return cypher, params, nil
}

// RecordsToMaps converts []Record to []map[string]any for the cpapi boundary.
func RecordsToMaps(rs []Record) []map[string]any {
	out := make([]map[string]any, len(rs))
	for i, r := range rs {
		out[i] = map[string]any(r)
	}
	return out
}
