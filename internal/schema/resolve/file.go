package resolve

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Register("file", newFileResolver)
}

// fileResolver serves a schema family from the local filesystem.
//
// URI forms:
//   - file:///abs/path/to/graph-schema/core   (absolute, recommended)
//   - file://./relative/path                  (relative to cwd; for tests / dev)
//
// In both cases the resolved base for fs.FS is "." (the family directory
// itself is the root of the os.DirFS).
type fileResolver struct {
	uri  string
	dir  string // local path on disk
}

func newFileResolver(u *url.URL) (Resolver, error) {
	// url.Parse on file:///abs/path → Host="", Path="/abs/path"
	// url.Parse on file://./rel    → Host=".", Path="/rel"
	var dir string
	switch {
	case u.Host == "" || u.Host == "localhost":
		dir = u.Path
	case u.Host == ".":
		dir = "." + u.Path // produce "./rel" form
	default:
		// file://host/path is unusual; treat host as the first path segment
		// rather than silently dropping it.
		dir = "/" + u.Host + u.Path
	}
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return nil, fmt.Errorf("file:// URI requires a path component (e.g. file:///etc/fracta/graph-schema/core)")
	}
	return &fileResolver{uri: u.String(), dir: dir}, nil
}

func (r *fileResolver) Source() string { return r.uri }

func (r *fileResolver) Open() (fs.FS, string, error) {
	abs, err := filepath.Abs(r.dir)
	if err != nil {
		return nil, "", fmt.Errorf("resolving %q to absolute path: %w", r.dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("file source %q not accessible: %w", r.uri, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("file source %q must be a directory", r.uri)
	}
	return os.DirFS(abs), ".", nil
}
