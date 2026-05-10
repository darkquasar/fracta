package scaffolds

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// pathSource serves a scaffold tree from a local filesystem path. The path
// is expected to contain a directory named exactly <kind>; FS() returns the
// rebased view of <path>/<kind>, RootFS() returns <path> itself (un-rebased
// — see spec-43 sibling-tree use case).
type pathSource struct {
	root   string // absolute, cleaned
	kind   Kind
	rootFS fs.FS  // os.DirFS(root)
}

// PathSource returns a Source rooted at `<path>/<kind>/`. The kind directory
// MUST exist. Description is "local:<absolute-path>".
func PathSource(path string, kind Kind) (Source, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("scaffolds: abs path %q: %w", path, err)
	}
	abs = filepath.Clean(abs)
	kindDir := filepath.Join(abs, kind.String())
	info, err := os.Stat(kindDir)
	if err != nil {
		if os.IsNotExist(err) {
			entries, _ := os.ReadDir(abs)
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return nil, fmt.Errorf("scaffolds: source %q does not contain a %q directory; got: %v", abs, kind.String(), names)
		}
		return nil, fmt.Errorf("scaffolds: stat %q: %w", kindDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scaffolds: %q exists but is not a directory", kindDir)
	}
	return &pathSource{
		root:   abs,
		kind:   kind,
		rootFS: os.DirFS(abs),
	}, nil
}

func (p *pathSource) RootFS() (fs.FS, error) {
	return p.rootFS, nil
}

func (p *pathSource) FS() (fs.FS, error) {
	root, err := p.RootFS()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, p.kind.String())
	if err != nil {
		return nil, fmt.Errorf("scaffolds: sub path %q: %w", p.kind, err)
	}
	return sub, nil
}

func (p *pathSource) Description() string {
	return "local:" + p.root
}

func (p *pathSource) Close() error { return nil }
