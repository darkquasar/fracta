package schema

import (
	"fmt"

	"github.com/darkquasar/fracta/internal/schema/resolve"
)

// LoadSchemaSetFromURI is the operator-facing loader entry. It parses a URI
// like "embed://graph-schema/core" or "file:///etc/fracta/graph-schema/core",
// opens the backing fs.FS via the resolver registry, and delegates to
// LoadSchemaSet.
//
// Called by cmd/serve.go for both the ontology.schemas: config block and the
// --schema-dir CLI flag (which wraps bare paths in file:// before calling).
func LoadSchemaSetFromURI(uri string) (*SchemaSet, error) {
	r, err := resolve.Parse(uri)
	if err != nil {
		return nil, err
	}
	fsys, base, err := r.Open()
	if err != nil {
		return nil, err
	}
	ss, err := LoadSchemaSet(fsys, base)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", r.Source(), err)
	}
	return ss, nil
}
