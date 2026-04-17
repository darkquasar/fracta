// Package assets provides a generic loader for embedded text assets.
// It reads from an fs.FS and supports both static files and Go text/template rendering.
package assets

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"text/template"
)

// Store loads named assets from an fs.FS rooted at a subdirectory.
type Store struct {
	fs   fs.FS
	root string
}

// New creates a Store that reads from fsys under the given root subdirectory.
// Pass "" for root to read from the filesystem root.
func New(fsys fs.FS, root string) *Store {
	return &Store{fs: fsys, root: root}
}

// MustLoad reads a static file and returns its content as a string.
// Panics if the file is not found — embedded assets are compile-time guarantees.
func (s *Store) MustLoad(name string) string {
	p := name
	if s.root != "" {
		p = path.Join(s.root, name)
	}
	data, err := fs.ReadFile(s.fs, p)
	if err != nil {
		panic(fmt.Sprintf("assets: failed to load %q: %v", p, err))
	}
	return string(data)
}

// MustRender parses a file as a Go text/template and executes it with data.
// Uses missingkey=error — fails hard if the template references a key not in data.
// Panics on missing file, parse error, or render error.
func (s *Store) MustRender(name string, data any) string {
	raw := s.MustLoad(name)
	t, err := template.New(name).Option("missingkey=error").Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("assets: failed to parse template %q: %v", name, err))
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("assets: failed to render template %q: %v", name, err))
	}
	return buf.String()
}
