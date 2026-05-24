package resolve

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"github.com/darkquasar/fracta/internal/schema/embedfs"
)

func init() {
	Register("embed", newEmbedResolver)
}

// embedResolver serves a schema family out of embedfs.FS.
//
// URI form: embed://<base-path>. The host component (per net/url) is the
// first segment after the //, and the path is the rest. We re-join them
// because the embedded FS layout puts the family directory at e.g.
// graph-schema/core, not /graph-schema/core.
//
// Example: embed://graph-schema/knowledge-garden
//   - u.Host = "graph-schema"
//   - u.Path = "/knowledge-garden"
//   - resolved base = "graph-schema/knowledge-garden"
type embedResolver struct {
	uri  string
	base string
}

func newEmbedResolver(u *url.URL) (Resolver, error) {
	base := path.Join(u.Host, strings.TrimPrefix(u.Path, "/"))
	base = strings.Trim(base, "/")
	if base == "" {
		return nil, fmt.Errorf("embed:// URI requires a path component (e.g. embed://graph-schema/core)")
	}
	return &embedResolver{uri: u.String(), base: base}, nil
}

func (r *embedResolver) Source() string { return r.uri }

func (r *embedResolver) Open() (fs.FS, string, error) {
	// Sanity-check the base exists in the embedded FS so we fail with a
	// clear error before the loader hits a missing _meta.yaml.
	if _, err := fs.Stat(embedfs.FS, r.base); err != nil {
		return nil, "", fmt.Errorf("embed source %q not found in baked schemas: %w", r.uri, err)
	}
	return embedfs.FS, r.base, nil
}
