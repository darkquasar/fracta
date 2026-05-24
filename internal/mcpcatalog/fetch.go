package mcpcatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// DefaultFetchSourceSpec is the canonical catalog source used when no
// explicit spec is given and no <root>/mcp-servers/.fracta-source memo exists.
const DefaultFetchSourceSpec = "github:darkquasar/fracta@main"

// ErrEmptyFetchSource is returned by Fetch when opts.Source is empty. The
// binary ships no embedded catalog (spec §4 R3) — substitute via
// ResolveFetchSource before calling.
var ErrEmptyFetchSource = errors.New("fetch requires an explicit source; the binary ships no embedded catalog")

// FetchOpts controls a catalog fetch.
type FetchOpts struct {
	// Source is the resolved source spec. Use ResolveFetchSource to
	// substitute the recorded `.fracta-source` value or the default when
	// the user did not pass an explicit positional argument.
	Source string
	// Merge: when true, preserves local-only entries by id and replaces
	// only the ids present in both local and remote.
	Merge bool
	// Filter applied during fetch. Entries failing the filter are not
	// written. An empty filter accepts everything.
	Filter Filter
	// SourceChecksum is pass-through to HttpsSource for raw https://...tar.gz
	// sources. Silently ignored on github / git@ / path sources so CI can
	// keep FRACTA_SOURCE_CHECKSUM set globally without breaking github fetches.
	SourceChecksum string
}

// FetchResult summarises the outcome of a Fetch call.
type FetchResult struct {
	SourceUsed     string
	CatalogVersion string
	LocalBefore    *Catalog
	RemoteCatalog  *Catalog
	Added          []string
	Removed        []string
	Changed        []string
}

// ResolveFetchSource returns the source spec the caller should pass to Fetch:
//
//  1. explicit if non-empty
//  2. else the contents of <root>/mcp-servers/.fracta-source if present
//  3. else DefaultFetchSourceSpec
func ResolveFetchSource(projectRoot, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	recorded, err := ReadFractaSource(projectRoot)
	if err != nil {
		return "", err
	}
	if recorded != "" {
		return recorded, nil
	}
	return DefaultFetchSourceSpec, nil
}

// Fetch resolves opts.Source, walks the resulting catalog tree, and writes it
// to <root>/mcp-servers/.
//
// Staging: each fetch first writes to <root>/mcp-servers/.staging/, fsyncs,
// then renames the staging directory over the live one. On --merge the
// staging dir is built from the union (local-only + remote) before rename.
// On any failure mid-fetch, .staging/ is removed by defer.
//
// On a successful plain (non-merge) fetch, writes <root>/mcp-servers/.fracta-source
// with the resolved source spec. On --merge, .fracta-source is NOT touched.
//
// Returns ErrEmptyFetchSource if opts.Source is empty.
func Fetch(ctx context.Context, projectRoot string, opts FetchOpts) (*FetchResult, error) {
	log := fractalog.Component("mcpcatalog")

	if opts.Source == "" {
		return nil, ErrEmptyFetchSource
	}

	// Load local catalog (if any) — used for --merge and for the diff in
	// the result.
	var localBefore *Catalog
	if cat, err := LoadProjectCatalog(projectRoot); err == nil {
		localBefore = cat
	} else if !errors.Is(err, ErrNoCatalog) {
		return nil, fmt.Errorf("mcpcatalog: load local catalog: %w", err)
	}

	// Resolve remote.
	src, err := resolveCatalogSource(ctx, opts.Source, opts.SourceChecksum)
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: resolve source %q: %w", opts.Source, err)
	}
	defer src.Close()

	rootFS, err := src.RootFS()
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: open source root: %w", err)
	}
	catalogFS, err := fs.Sub(rootFS, "mcp-servers")
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: source %s does not contain mcp-servers/: %w", opts.Source, err)
	}
	if _, err := fs.Stat(catalogFS, "catalog.yaml"); err != nil {
		return nil, fmt.Errorf("mcpcatalog: source %s does not contain mcp-servers/catalog.yaml", opts.Source)
	}

	remoteCat, err := LoadCatalog(catalogFS)
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: validate remote catalog: %w", err)
	}

	// Build staging directory.
	catRoot := filepath.Join(projectRoot, "mcp-servers")
	staging := filepath.Join(catRoot, ".staging")
	if err := os.MkdirAll(catRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mcpcatalog: mkdir mcp-servers: %w", err)
	}
	// Best-effort cleanup of any prior staging dir before we start.
	_ = os.RemoveAll(staging)
	defer os.RemoveAll(staging)

	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, fmt.Errorf("mcpcatalog: mkdir staging: %w", err)
	}

	// Determine ids to include from remote, filtered.
	includeRemote := map[string]bool{}
	for id, e := range remoteCat.Entries {
		if opts.Filter.IsEmpty() || opts.Filter.Match(e) {
			includeRemote[id] = true
		}
	}

	// Copy remote tree into staging.
	if err := copyCatalogFS(catalogFS, staging, includeRemote); err != nil {
		return nil, err
	}

	// On --merge: also stage local-only entries.
	if opts.Merge && localBefore != nil {
		for id, e := range localBefore.Entries {
			if _, ok := remoteCat.Entries[id]; ok {
				continue
			}
			// Copy local entry to staging.
			if err := copyLocalEntry(catRoot, staging, id, e); err != nil {
				return nil, err
			}
		}
		// Rebuild catalog.yaml in staging to include the merged servers list.
		if err := rewriteStagedCatalogYAML(staging, remoteCat, localBefore); err != nil {
			return nil, err
		}
	}

	// Promote staging → live: remove existing contents (except .staging
	// itself), then move staging contents up. We do a directory swap to
	// preserve atomicity-ish on a single filesystem.
	if err := swapStagingIntoLive(catRoot, staging); err != nil {
		return nil, fmt.Errorf("mcpcatalog: promote staging: %w", err)
	}

	// Plain fetch: record source. Merge: do not touch.
	if !opts.Merge {
		if err := os.WriteFile(filepath.Join(catRoot, fractaSourceFile), []byte(opts.Source+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("mcpcatalog: write .fracta-source: %w", err)
		}
	}

	// Ensure .gitignore exists with .staging/.
	if err := ensureCatalogGitignore(catRoot); err != nil {
		return nil, err
	}

	// Build result.
	delta := Diff(localBefore, remoteCat)
	out := &FetchResult{
		SourceUsed:     opts.Source,
		CatalogVersion: remoteCat.Version,
		LocalBefore:    localBefore,
		RemoteCatalog:  remoteCat,
	}
	for _, e := range delta.Added {
		out.Added = append(out.Added, e.ID)
	}
	for _, e := range delta.Removed {
		out.Removed = append(out.Removed, e.ID)
	}
	for _, p := range delta.Changed {
		out.Changed = append(out.Changed, p.Remote.ID)
	}
	log.Info("fetched catalog",
		"source", opts.Source,
		"version", remoteCat.Version,
		"added", len(out.Added),
		"removed", len(out.Removed),
		"changed", len(out.Changed),
		"merge", opts.Merge,
	)
	return out, nil
}

// resolveCatalogSource maps a fetch source spec to a scaffolds.Source.
//
// Unlike scaffolds.ResolveSource, this dispatcher takes no Kind — fetch only
// consumes Source.RootFS() and never Source.FS(), so the kind-driven rebase
// is irrelevant. The empty spec is rejected (ErrEmptyFetchSource); the
// binary ships no embedded MCP catalog (spec §4 R3).
func resolveCatalogSource(ctx context.Context, spec, checksum string) (scaffolds.Source, error) {
	if spec == "" {
		return nil, ErrEmptyFetchSource
	}
	switch {
	case strings.HasPrefix(spec, "github:"):
		// KindNone skips the kind-subdir validation in the source
		// constructor — fetch only consumes RootFS() and rebases to
		// "mcp-servers/" explicitly.
		return scaffolds.GithubSource(ctx, spec, scaffolds.KindNone)
	case strings.HasPrefix(spec, "https://"):
		// Try the github browser URL form first.
		if owner, repo, ref, ok := scaffolds.ParseGithubURL(spec); ok {
			return scaffolds.GithubSourceFromParts(ctx, owner, repo, ref, scaffolds.KindNone)
		}
		// Helpful error for browser viewer URLs (/tree/<ref>, /blob/<path>).
		if strings.Contains(spec, "github.com/") &&
			(strings.Contains(spec, "/tree/") || strings.Contains(spec, "/blob/")) {
			return nil, fmt.Errorf("github browser URL %q is not a fetch source; "+
				"use github:owner/repo@<ref>, https://github.com/owner/repo@<ref>, "+
				"or git@github.com:owner/repo[.git][@<ref>]", spec)
		}
		// archive/... codeload URLs and arbitrary HTTPS tarballs fall
		// through to HttpsSource.
		return scaffolds.HttpsSource(ctx, spec, scaffolds.KindNone, checksum)
	case strings.HasPrefix(spec, "git@"):
		owner, repo, ref, ok := scaffolds.ParseGithubSSH(spec)
		if !ok {
			return nil, fmt.Errorf("SSH source %q not recognized; only git@github.com:owner/repo[.git][@ref] is supported (fetched over HTTPS codeload)", spec)
		}
		return scaffolds.GithubSourceFromParts(ctx, owner, repo, ref, scaffolds.KindNone)
	default:
		return scaffolds.PathSource(spec, scaffolds.KindNone)
	}
}

// copyCatalogFS copies the catalog tree from src (rooted at mcp-servers/) to
// dst (also a mcp-servers root). Only includes per-server entries whose id
// is in include — but always copies catalog.yaml (which is rewritten when
// filtering is in play; spec-43 doesn't filter catalog.yaml directly).
func copyCatalogFS(src fs.FS, dst string, include map[string]bool) error {
	// First pass: copy top-level files (catalog.yaml, README.md, etc.).
	topEntries, err := fs.ReadDir(src, ".")
	if err != nil {
		return fmt.Errorf("mcpcatalog: read source root: %w", err)
	}
	for _, e := range topEntries {
		name := e.Name()
		// Skip dotfiles in the source — never propagate .fracta-source
		// or .gitignore from a remote tree.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			id := name
			if !include[id] {
				continue
			}
			if err := copyFSDir(src, name, filepath.Join(dst, name)); err != nil {
				return err
			}
			continue
		}
		// File at top — copy verbatim.
		raw, err := fs.ReadFile(src, name)
		if err != nil {
			return fmt.Errorf("mcpcatalog: read %s: %w", name, err)
		}
		if err := writeFile(filepath.Join(dst, name), raw); err != nil {
			return err
		}
	}
	return nil
}

// copyFSDir recursively copies a directory subtree from fs.FS to a real path.
func copyFSDir(src fs.FS, srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(src, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return writeFile(target, raw)
	})
}

// copyLocalEntry copies a single local entry's directory into staging.
func copyLocalEntry(catRoot, staging, id string, _ *Entry) error {
	srcDir := filepath.Join(catRoot, id)
	dstDir := filepath.Join(staging, id)
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		target := filepath.Join(dstDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFile(target, raw)
	})
}

// rewriteStagedCatalogYAML produces a catalog.yaml in staging that lists the
// merged set of servers (remote ∪ local-only). Used only on --merge.
func rewriteStagedCatalogYAML(staging string, remote, local *Catalog) error {
	// Build a union, preserving remote ordering then appending local-only.
	seen := map[string]bool{}
	type entry struct{ ID, Path string }
	var rows []entry
	for _, s := range remote.Servers {
		rows = append(rows, entry{s.ID, s.Path})
		seen[s.ID] = true
	}
	for _, s := range local.Servers {
		if seen[s.ID] {
			continue
		}
		rows = append(rows, entry{s.ID, s.Path})
	}

	var buf strings.Builder
	if remote.Version != "" {
		buf.WriteString("version: ")
		buf.WriteString(remote.Version)
		buf.WriteString("\n")
	}
	if remote.Description != "" {
		buf.WriteString("description: ")
		buf.WriteString(remote.Description)
		buf.WriteString("\n")
	}
	buf.WriteString("servers:\n")
	for _, r := range rows {
		buf.WriteString("  - id: ")
		buf.WriteString(r.ID)
		buf.WriteString("\n    path: ")
		buf.WriteString(r.Path)
		buf.WriteString("\n")
	}
	return writeFile(filepath.Join(staging, "catalog.yaml"), []byte(buf.String()))
}

// swapStagingIntoLive removes the current contents of catRoot (except
// .staging/ and .fracta-source / .gitignore) then moves everything from
// staging up into catRoot. Single filesystem; rename(2) is atomic per file
// but the overall swap is best-effort.
func swapStagingIntoLive(catRoot, staging string) error {
	// Remove existing top-level entries except .staging, .fracta-source,
	// .gitignore (memo files we manage explicitly).
	existing, err := os.ReadDir(catRoot)
	if err != nil {
		return err
	}
	preserve := map[string]bool{
		".staging":       true,
		fractaSourceFile: true,
		".gitignore":     true,
	}
	for _, e := range existing {
		if preserve[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(catRoot, e.Name())); err != nil {
			return err
		}
	}
	// Move each top-level entry from staging into catRoot.
	stagingEntries, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, e := range stagingEntries {
		src := filepath.Join(staging, e.Name())
		dst := filepath.Join(catRoot, e.Name())
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// ensureCatalogGitignore creates or updates <root>/mcp-servers/.gitignore to
// include the `.staging/` line — so orphan staging dirs (after kill -9)
// don't pollute `git status`.
func ensureCatalogGitignore(catRoot string) error {
	path := filepath.Join(catRoot, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mcpcatalog: read .gitignore: %w", err)
	}
	body := string(raw)
	if strings.Contains(body, ".staging/") {
		return nil
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += ".staging/\n"
	return writeFile(path, []byte(body))
}

// writeFile creates parents and writes content atomically (temp+rename).
func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, strings.NewReader(string(content))); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
