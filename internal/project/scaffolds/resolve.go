package scaffolds

import (
	"context"
	"strings"
)

// ResolveSource maps a --source spec string to a Source implementation:
//
//   - "" (empty)              → EmbeddedSource (binary-baked templates)
//   - "github:owner/repo@ref" → GithubSource (codeload tarball)
//   - "https://..."           → HttpsSource (raw HTTPS tarball, optional checksum)
//   - anything else           → PathSource (local filesystem)
//
// The checksum argument is only meaningful for the https:// scheme; other
// schemes ignore it (passing a non-empty checksum to a non-https source is
// not an error — operators may have it set in CI/CD as a default).
func ResolveSource(ctx context.Context, spec string, kind Kind, checksum string) (Source, error) {
	switch {
	case spec == "":
		return EmbeddedSource(kind), nil
	case strings.HasPrefix(spec, "github:"):
		return GithubSource(ctx, spec, kind)
	case strings.HasPrefix(spec, "https://"):
		return HttpsSource(ctx, spec, kind, checksum)
	default:
		return PathSource(spec, kind)
	}
}
