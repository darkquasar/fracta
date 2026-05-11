package scaffolds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// httpsSource downloads a tarball from an arbitrary https:// URL, extracts
// it, and exposes the kind subtree. Unlike GithubSource, the archive is
// expected to have <kind>/ at its top level — there is no `<repo>-<ref>/`
// wrapper. RootFS() returns the extracted root (so spec-43's sibling-tree
// case works the same way).
type httpsSource struct {
	url    string
	tmpdir string
	kind   Kind
}

// HttpsSource downloads url, extracts to a tmpdir, validates that <kind>/ is
// present at the archive root. Optional checksum (sha256:<hex>) is verified
// before extraction; a missing checksum prints a loud warning to stderr.
func HttpsSource(ctx context.Context, url string, kind Kind, checksum string) (Source, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("scaffolds: https source requires https:// scheme; got %q", url)
	}

	body, err := downloadTarball(ctx, url)
	if err != nil {
		return nil, err
	}

	if err := verifyChecksum(body, checksum, url); err != nil {
		return nil, err
	}

	tmpdir, err := os.MkdirTemp("", "fracta-scaffold-https-")
	if err != nil {
		return nil, fmt.Errorf("scaffolds: mkdtemp: %w", err)
	}
	if err := extractTarballGz(body, tmpdir); err != nil {
		_ = os.RemoveAll(tmpdir)
		return nil, err
	}

	if kind != KindNone {
		kindDir := filepath.Join(tmpdir, kind.String())
		if info, err := os.Stat(kindDir); err != nil || !info.IsDir() {
			topEntries, _ := os.ReadDir(tmpdir)
			names := make([]string, 0, len(topEntries))
			for _, e := range topEntries {
				names = append(names, e.Name())
			}
			_ = os.RemoveAll(tmpdir)
			return nil, fmt.Errorf("scaffolds: https source %s does not contain a %q directory at archive root; got: %v", url, kind.String(), names)
		}
	}

	return &httpsSource{url: url, tmpdir: tmpdir, kind: kind}, nil
}

// verifyChecksum validates body against checksum (`sha256:<hex>`). Empty
// checksum prints a stderr warning and proceeds — operators are expected to
// pin checksums in CI/CD.
func verifyChecksum(body []byte, checksum, url string) error {
	if checksum == "" {
		fmt.Fprintf(os.Stderr, "scaffolds: warning: no --source-checksum supplied for %s; tarball integrity is not verified\n", url)
		return nil
	}
	if !strings.HasPrefix(checksum, "sha256:") {
		return fmt.Errorf("scaffolds: --source-checksum must be sha256:<hex>; got %q", checksum)
	}
	want := strings.TrimPrefix(checksum, "sha256:")
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("scaffolds: checksum mismatch for %s: got sha256:%s, want sha256:%s", url, got, want)
	}
	return nil
}

func (h *httpsSource) RootFS() (fs.FS, error) {
	return os.DirFS(h.tmpdir), nil
}

func (h *httpsSource) FS() (fs.FS, error) {
	root, err := h.RootFS()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, h.kind.String())
	if err != nil {
		return nil, fmt.Errorf("scaffolds: https sub %q: %w", h.kind, err)
	}
	return sub, nil
}

func (h *httpsSource) Description() string {
	return h.url
}

func (h *httpsSource) Close() error {
	if h.tmpdir == "" {
		return nil
	}
	err := os.RemoveAll(h.tmpdir)
	h.tmpdir = ""
	return err
}
