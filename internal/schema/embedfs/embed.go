// Package embedfs holds the schema YAML families baked into the fracta
// binary at compile time. It is a leaf package — depended on by both
// internal/schema (for tests) and internal/schema/resolve (for the embed://
// resolver) — and intentionally has no fracta imports of its own so it can
// break the import cycle between those two.
//
// The graph-schema/ directory lives inside this package because Go's
// go:embed directive cannot reach upward via "..". Adding a new family
// means dropping its directory under internal/schema/embedfs/graph-schema/
// and rebuilding — no resolver or scaffold-template changes needed beyond
// the operator-side ontology config entry.
package embedfs

import "embed"

// FS contains every schema family that ships with the fracta binary.
//
//go:embed all:graph-schema
var FS embed.FS
