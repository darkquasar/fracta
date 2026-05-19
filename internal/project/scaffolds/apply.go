package scaffolds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConflictPolicy controls what happens when an entry already exists at the
// destination path.
type ConflictPolicy int

const (
	// ConflictFail aborts on the first existing file. Default for safety.
	ConflictFail ConflictPolicy = iota
	// ConflictSkipExisting leaves existing files untouched and continues.
	ConflictSkipExisting
	// ConflictOverwrite replaces existing files.
	ConflictOverwrite
)

// ApplyOpts modifies how Apply walks the source.
type ApplyOpts struct {
	// DryRun reports what would be written without touching the filesystem.
	DryRun bool
	// OnConflict is the policy for files that already exist at dest.
	OnConflict ConflictPolicy
}

// Result summarises a walk.
type Result struct {
	Written []string // relative paths under dest
	Skipped []string // relative paths under dest (existing, SkipExisting policy)
	Source  string   // Source.Description()
}

// authHelperPathPrefixes lists path components under which any file MUST be
// 0755 regardless of source-reported mode (spec-42 §6 invariant). The check is
// against any path segment named "auth-helpers" so the rule applies whether
// the helpers live at .fracta/auth-helpers/ (local) or fracta/auth-helpers/
// (compose, k8s).
var authHelperPathPrefixes = []string{"auth-helpers"}

// Apply materializes src into dest. It is responsible for:
//
//   - Walking the source FS (fs.WalkDir).
//   - Refusing path-traversal entries (R7).
//   - Honoring ConflictPolicy.
//   - Setting file modes per spec-42 §6.
//
// .gitkeep files are intentionally not written (they exist in the source tree
// only to keep otherwise-empty directories alive in git).
func Apply(ctx context.Context, src Source, dest string, opts ApplyOpts) (Result, error) {
	res := Result{Source: src.Description()}

	srcFS, err := src.FS()
	if err != nil {
		return res, err
	}

	cleanDest, err := filepath.Abs(dest)
	if err != nil {
		return res, fmt.Errorf("scaffolds: abs dest %q: %w", dest, err)
	}
	cleanDest = filepath.Clean(cleanDest)

	walkErr := fs.WalkDir(srcFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		// Skip placeholder files used to keep empty embed dirs alive.
		if filepath.Base(p) == ".gitkeep" {
			return nil
		}
		// Refuse hostile entries (R7). filepath.Clean normalises slashes;
		// reject anything that would escape dest.
		if err := guardPath(p); err != nil {
			return err
		}
		target := filepath.Join(cleanDest, filepath.FromSlash(p))
		if !withinDest(cleanDest, target) {
			return fmt.Errorf("scaffolds: refusing entry %q: escapes destination", p)
		}

		if d.IsDir() {
			if opts.DryRun {
				return nil
			}
			return os.MkdirAll(target, 0o755)
		}

		// File: respect conflict policy.
		if _, statErr := os.Lstat(target); statErr == nil {
			switch opts.OnConflict {
			case ConflictFail:
				return fmt.Errorf("scaffolds: %s already exists (use --force to overwrite)", target)
			case ConflictSkipExisting:
				res.Skipped = append(res.Skipped, p)
				return nil
			case ConflictOverwrite:
				// fall through and rewrite
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("scaffolds: stat %s: %w", target, statErr)
		}

		mode := resolveMode(p, d)

		if opts.DryRun {
			res.Written = append(res.Written, p)
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("scaffolds: mkdir %s: %w", filepath.Dir(target), err)
		}
		in, err := srcFS.Open(p)
		if err != nil {
			return fmt.Errorf("scaffolds: open source %s: %w", p, err)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			_ = in.Close()
			return fmt.Errorf("scaffolds: create %s: %w", target, err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = in.Close()
			_ = out.Close()
			return fmt.Errorf("scaffolds: write %s: %w", target, err)
		}
		if err := in.Close(); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		// O_CREATE honours the umask; chmod explicitly so 0755 helpers stay
		// 0755 even with a restrictive umask (e.g. 0077 on hardened CI).
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("scaffolds: chmod %s: %w", target, err)
		}
		res.Written = append(res.Written, p)
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}

	// Stable order for tests + diff-friendly output.
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	return res, nil
}

// guardPath rejects entries whose path would escape the destination tree.
// Called on the slash-form path produced by fs.WalkDir.
func guardPath(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("scaffolds: refusing absolute entry %q", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("scaffolds: refusing entry with parent traversal: %q", p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return fmt.Errorf("scaffolds: refusing entry with embedded '..': %q", p)
		}
	}
	return nil
}

// withinDest verifies that target resolves to a path inside dest (after
// cleaning). Both args must already be absolute + clean.
func withinDest(dest, target string) bool {
	// Allow target == dest (root) and any descendant.
	if target == dest {
		return true
	}
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}

// resolveMode applies the spec-42 §6 file-mode invariant:
//
//  1. Files under any "auth-helpers" path component MUST be 0755 (the spec
//     promises every helper is executable post-init; a 0644 helper would be
//     silently broken).
//  2. Otherwise, prefer the source-reported mode if it carries any
//     permission bits — covers tarball entries and os.DirFS sources.
//  3. Fall back to 0644 for sources that strip modes (notably embed.FS).
func resolveMode(p string, d fs.DirEntry) os.FileMode {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		for _, prefix := range authHelperPathPrefixes {
			if seg == prefix {
				return 0o755
			}
		}
	}
	if info, err := d.Info(); err == nil {
		perm := info.Mode().Perm()
		if perm != 0 {
			// embed.FS reports files as 0444 (read-only in-binary), but
			// scaffold output must be operator-editable. Promote to 0644.
			if perm == 0o444 {
				return 0o644
			}
			return perm
		}
	}
	return 0o644
} 
