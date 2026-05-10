package scaffolds

import (
	"fmt"
	"io/fs"
)

// Source is one rooted scaffold tree available to the walker. Concrete
// implementations cover embedded (binary), filesystem path, and the
// downloaded GitHub / HTTPS tarball cases (added in Phase 5).
//
// Lifecycle: a Source is opened (FS()), walked (apply.Apply), and closed
// (Close). For tarball-backed sources, Close() removes the temp directory; for
// embedded and os.DirFS sources, Close() is a no-op.
type Source interface {
	// FS returns an fs.FS rooted at the scaffold tree (i.e. the contents of
	// the kind directory itself, not the parent). Default implementation is
	// fs.Sub(RootFS(), "<root-prefix>/<kind>") — see each source for what
	// "<root-prefix>" means in that source's layout.
	FS() (fs.FS, error)

	// RootFS returns the un-rebased fs.FS — the entire extracted tree
	// (tarball root, repo root, embed FS root). Spec-43 uses this to reach
	// into sibling subdirectories like deployment/mcp-servers without
	// re-downloading or re-extracting. Implementations are one-liners.
	RootFS() (fs.FS, error)

	// Description is a human-readable identifier for init output. Examples:
	// "embedded", "local:/abs/path", "github:owner/repo@ref".
	Description() string

	// Close releases any resources (temp dirs, etc.). Safe to call multiple
	// times.
	Close() error
}

// embeddedSource serves the templates baked into the binary at build time.
type embeddedSource struct {
	kind Kind
}

// EmbeddedSource returns a Source backed by the in-binary `embed.FS` for the
// given kind. FS() is rooted at `templates/<kind>/`; RootFS() is the full
// `EmbeddedFS` (i.e. its root contains `templates/`).
func EmbeddedSource(kind Kind) Source {
	return &embeddedSource{kind: kind}
}

func (e *embeddedSource) RootFS() (fs.FS, error) {
	return EmbeddedFS, nil
}

func (e *embeddedSource) FS() (fs.FS, error) {
	root, err := e.RootFS()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, "templates/"+e.kind.String())
	if err != nil {
		return nil, fmt.Errorf("embedded scaffold %s: %w", e.kind, err)
	}
	return sub, nil
}

func (e *embeddedSource) Description() string {
	return "embedded"
}

func (e *embeddedSource) Close() error { return nil }
