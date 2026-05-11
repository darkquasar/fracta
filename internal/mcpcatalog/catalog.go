package mcpcatalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ErrNoCatalog is returned by LoadProjectCatalog when <root>/mcp-servers/ is
// missing or contains no catalog.yaml.
var ErrNoCatalog = errors.New("no catalog found at <root>/mcp-servers/")

// ServerRef is one row of the top-level `catalog.yaml` `servers:` list.
type ServerRef struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

// catalogIndex is the on-disk shape of `catalog.yaml`. Version is decoded as a
// node so we preserve the literal scalar (`1`, `1.4`, `v2`).
type catalogIndex struct {
	Version     yaml.Node   `yaml:"version"`
	Description string      `yaml:"description"`
	Servers     []ServerRef `yaml:"servers"`
}

// Catalog is the in-memory representation of a single mcp-servers/ tree.
//
// FS-pure: LoadCatalog does NOT read `.fracta-source`. That memo file is
// project-local state — callers retrieve it via the standalone
// ReadFractaSource helper (see source_helpers.go).
type Catalog struct {
	Version     string
	Description string
	Servers     []ServerRef
	Entries     map[string]*Entry
}

// Get returns an entry by id.
func (c *Catalog) Get(id string) (*Entry, bool) {
	e, ok := c.Entries[id]
	return e, ok
}

// IDs returns entry ids in catalog (`catalog.yaml` `servers:`) order. Entries
// that decoded successfully are included; ids whose server.yaml is missing
// are omitted from the iteration order but they were already rejected at
// LoadCatalog time.
func (c *Catalog) IDs() []string {
	out := make([]string, 0, len(c.Servers))
	for _, s := range c.Servers {
		if _, ok := c.Entries[s.ID]; ok {
			out = append(out, s.ID)
		}
	}
	return out
}

// SortedIDs returns entry ids in lexicographic order (used for stable list
// output regardless of catalog.yaml ordering).
func (c *Catalog) SortedIDs() []string {
	out := make([]string, 0, len(c.Entries))
	for id := range c.Entries {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// LoadCatalog reads `catalog.yaml` + every per-server `server.yaml` from fsys.
// fsys is rooted at the catalog directory (i.e. <root>/mcp-servers/ for an
// operator project, or fs.Sub(remoteRootFS, "mcp-servers") for a fetch source).
//
// A malformed index or missing per-server file is a hard error — half-loaded
// catalogs are not allowed (operators see one diagnostic, not 14).
func LoadCatalog(fsys fs.FS) (*Catalog, error) {
	raw, err := fs.ReadFile(fsys, "catalog.yaml")
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: read catalog.yaml: %w", err)
	}
	var idx catalogIndex
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("mcpcatalog: decode catalog.yaml: %w", err)
	}

	cat := &Catalog{
		Version:     versionString(idx.Version),
		Description: idx.Description,
		Servers:     idx.Servers,
		Entries:     make(map[string]*Entry, len(idx.Servers)),
	}

	for _, s := range idx.Servers {
		if s.ID == "" || s.Path == "" {
			return nil, fmt.Errorf("mcpcatalog: catalog.yaml has entry with empty id or path: %+v", s)
		}
		entryBytes, err := fs.ReadFile(fsys, s.Path)
		if err != nil {
			return nil, fmt.Errorf("mcpcatalog: read %s: %w", s.Path, err)
		}
		var e Entry
		if err := yaml.Unmarshal(entryBytes, &e); err != nil {
			return nil, fmt.Errorf("mcpcatalog: decode %s: %w", s.Path, err)
		}
		if e.ID == "" {
			return nil, fmt.Errorf("mcpcatalog: %s has empty id field", s.Path)
		}
		if e.ID != s.ID {
			return nil, fmt.Errorf("mcpcatalog: %s declares id %q but catalog.yaml lists it as %q", s.Path, e.ID, s.ID)
		}
		cat.Entries[e.ID] = &e
	}

	return cat, nil
}

// LoadProjectCatalog loads the catalog from <root>/mcp-servers/. Returns
// ErrNoCatalog when the directory is missing or no catalog.yaml exists, so
// the CLI can suggest "fracta config mcp fetch".
func LoadProjectCatalog(projectRoot string) (*Catalog, error) {
	catDir := filepath.Join(projectRoot, "mcp-servers")
	info, err := os.Stat(catDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoCatalog
		}
		return nil, fmt.Errorf("mcpcatalog: stat %s: %w", catDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mcpcatalog: %s exists but is not a directory", catDir)
	}
	if _, err := os.Stat(filepath.Join(catDir, "catalog.yaml")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoCatalog
		}
		return nil, fmt.Errorf("mcpcatalog: stat catalog.yaml: %w", err)
	}
	return LoadCatalog(os.DirFS(catDir))
}

// versionString returns the literal scalar from a yaml.Node, preserving "1",
// "1.4", "v2" without numeric coercion.
func versionString(n yaml.Node) string {
	if n.Kind == 0 {
		return ""
	}
	return n.Value
}
