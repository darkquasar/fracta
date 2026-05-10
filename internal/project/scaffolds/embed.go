package scaffolds

import "embed"

// EmbeddedFS holds the templates baked into the binary at build time.
// Operators get the full deployment tree without network access by default;
// `fracta init --source ...` overrides this with a remote tree.
//
// Note: embed.FS strips file modes — the walker (apply.go) re-applies the
// correct mode based on path (auth-helpers/* → 0755) per spec-42 §6.
//
//go:embed all:templates/local all:templates/docker-compose all:templates/k8s
var EmbeddedFS embed.FS
