package graph

import (
	"fmt"
	"regexp"
)

// identifierRe matches valid Cypher identifiers: starts with a letter or underscore,
// followed by up to 63 alphanumeric or underscore characters.
var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ValidateIdentifier checks that s is a safe Cypher identifier (label, property key, etc.).
// context describes what kind of identifier is being validated (e.g., "label", "edge type").
func ValidateIdentifier(s, context string) error {
	if !identifierRe.MatchString(s) {
		return &ValidationError{Message: fmt.Sprintf("invalid %s %q: must match %s", context, s, identifierRe.String())}
	}
	return nil
}

// ValidateEdgeTypes validates each element in types as a Cypher identifier.
func ValidateEdgeTypes(types []string) error {
	for _, t := range types {
		if err := ValidateIdentifier(t, "edge type"); err != nil {
			return err
		}
	}
	return nil
}
