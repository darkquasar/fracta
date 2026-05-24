// Package resolve maps schema source URIs to an fs.FS + base path the schema
// loader can consume. Today it supports embed:// (schemas baked into the
// binary) and file:// (local filesystem overrides). Future schemes
// (s3://, https://, configmap://) register the same way without touching
// the loader.
package resolve

import (
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Resolver opens a backing fs.FS and reports the base path within it where
// a schema set's _meta.yaml, semantics.yaml, nodes/, particulars/, edges/,
// and checkpoint.yaml live.
type Resolver interface {
	// Source returns the original URI string for logging.
	Source() string
	// Open returns the fs.FS that holds the schema files and the base path
	// inside that FS rooted at the family directory.
	Open() (fsys fs.FS, base string, err error)
}

// Factory builds a Resolver from a parsed URL. Schemes register a Factory
// during init().
type Factory func(*url.URL) (Resolver, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a new URI scheme available to Parse. Idempotent at the
// register level — re-registering the same scheme replaces the prior factory
// (only realistic in tests).
func Register(scheme string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[scheme] = f
}

// schemes returns the sorted set of registered schemes for error messages.
func schemes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for s := range factories {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Parse turns a URI string into a Resolver. Bare strings (no scheme) are
// rejected with a message pointing operators at the explicit forms.
func Parse(raw string) (Resolver, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty schema URI; expected one of %s", strings.Join(schemes(), ", "))
	}
	if !strings.Contains(raw, "://") {
		return nil, fmt.Errorf("schema URI %q has no scheme; use one of %s (e.g. embed://graph-schema/core or file:///abs/path)",
			raw, strings.Join(schemes(), ", "))
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing schema URI %q: %w", raw, err)
	}

	mu.RLock()
	factory, ok := factories[u.Scheme]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown schema URI scheme %q (supported: %s)", u.Scheme, strings.Join(schemes(), ", "))
	}

	return factory(u)
}
