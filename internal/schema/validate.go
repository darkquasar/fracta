package schema

import "fmt"

// ValidateNode checks that a node write conforms to the schema.
func (r *SchemaRegistry) ValidateNode(label string, props map[string]any) error {
	def, ok := r.Nodes[label]
	if !ok {
		return fmt.Errorf("unknown node label %q", label)
	}

	// Check required properties.
	for name, prop := range def.Properties {
		if prop.Required {
			if _, ok := props[name]; !ok {
				return fmt.Errorf("node %s: required property %q missing", label, name)
			}
		}
	}

	// Check semantic references at runtime.
	for name, prop := range def.Properties {
		if prop.Ref == "semantics" {
			if val, ok := props[name]; ok {
				if s, ok := val.(string); ok && s != "" {
					if _, valid := r.Semantics[s]; !valid {
						return fmt.Errorf("node %s.%s: unknown semantic %q", label, name, s)
					}
				}
			}
		}
	}

	return nil
}

// ValidateEdge checks that an edge write conforms to the schema.
func (r *SchemaRegistry) ValidateEdge(edgeType, fromLabel, toLabel string, props map[string]any) error {
	def, ok := r.Edges[edgeType]
	if !ok {
		return fmt.Errorf("unknown edge type %q", edgeType)
	}

	// Check endpoint constraints.
	if !contains(def.From, fromLabel) {
		return fmt.Errorf("edge %s: source label %q not allowed (expected one of %v)", edgeType, fromLabel, def.From)
	}
	if !contains(def.To, toLabel) {
		return fmt.Errorf("edge %s: target label %q not allowed (expected one of %v)", edgeType, toLabel, def.To)
	}

	// Check required properties on the edge.
	for name, prop := range def.Properties {
		if prop.Required {
			if _, ok := props[name]; !ok {
				return fmt.Errorf("edge %s: required property %q missing", edgeType, name)
			}
		}
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
