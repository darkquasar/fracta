package scaffolds

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxScaffoldTarball caps response bodies for GitHub / HTTPS sources to limit
// init-time memory + disk consumption (spec-42 §11 R8). Raise via code change,
// not a flag.
const maxScaffoldTarball = 50 << 20 // 50 MB

// downloadTarball fetches url under a 50 MB cap (R8) and returns the raw bytes
// suitable for handing to extractTarballGz. The cap error names the URL and
// the limit.
func downloadTarball(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("scaffolds: build request for %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scaffolds: downloading %s: %w; check connectivity or use a local --source path", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scaffolds: downloading %s: HTTP %d", url, resp.StatusCode)
	}
	// LimitReader plus one — read one byte beyond the cap so we can detect
	// the overflow case (vs. a body that fits exactly).
	limited := io.LimitReader(resp.Body, maxScaffoldTarball+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("scaffolds: reading body from %s: %w", url, err)
	}
	if int64(len(body)) > maxScaffoldTarball {
		return nil, fmt.Errorf("scaffolds: tarball at %s exceeds size limit of %d bytes; raise the cap in source.go if intentional", url, maxScaffoldTarball)
	}
	return body, nil
}

// extractTarballGz untars a gzipped archive into destDir. Path-traversal
// hardened (R7): rejects entries with absolute paths or `..` segments before
// any write. Returns the list of created top-level directory names so
// callers can locate the extracted root.
func extractTarballGz(data []byte, destDir string) error {
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("scaffolds: gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("scaffolds: abs %q: %w", destDir, err)
	}
	cleanDest = filepath.Clean(cleanDest)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("scaffolds: tar read: %w", err)
		}
		if err := guardPath(hdr.Name); err != nil {
			return err
		}
		target := filepath.Join(cleanDest, filepath.FromSlash(hdr.Name))
		if !withinDest(cleanDest, target) {
			return fmt.Errorf("scaffolds: refusing entry %q: escapes destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("scaffolds: mkdir %s: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("scaffolds: mkdir %s: %w", filepath.Dir(target), err)
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("scaffolds: create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("scaffolds: write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip symlinks (and hard links) defensively. They're a
			// classic escape vector and scaffolds don't need them.
			continue
		default:
			// Skip unknown entry types (devices, fifos, etc.). Scaffolds
			// are file/dir trees; nothing else is expected.
			continue
		}
	}
	return nil
}
