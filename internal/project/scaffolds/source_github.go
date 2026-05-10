package scaffolds

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// githubSpecRE parses `github:owner/repo[@ref]` (ref defaults to "main").
var githubSpecRE = regexp.MustCompile(`^github:([^/]+)/([^@]+)(?:@(.+))?$`)

// shaLikeRE matches refs that look like commit SHAs (≥7 hex chars).
var shaLikeRE = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// githubBaseURL is the codeload host. Tests override this to point at a
// local httptest.Server.
var githubBaseURL = "https://codeload.github.com"

// githubSource downloads a tarball from GitHub via codeload, extracts it to a
// tmpdir, and exposes the kind subtree. RootFS() returns the extracted repo
// root (e.g. <tmpdir>/<repo>-<ref>/), so spec-43 can fs.Sub() into sibling
// trees outside templates/.
type githubSource struct {
	owner, repo, ref string
	tmpdir           string
	rootDir          string // <tmpdir>/<repo>-<ref-with-slashes-as-dashes>/
	kind             Kind
}

// GithubSource parses spec ("github:owner/repo[@ref]") and downloads the
// tarball. Network-bound; pass a context with a deadline if you care.
//
// On non-SHA refs (anything not matching ≥7 hex chars), emits a
// reproducibility warning to stderr — orgs in CI/CD should pin to commit
// SHAs or release tags, not branch names.
func GithubSource(ctx context.Context, spec string, kind Kind) (Source, error) {
	m := githubSpecRE.FindStringSubmatch(spec)
	if m == nil {
		return nil, fmt.Errorf("scaffolds: invalid github spec %q; want github:owner/repo@ref", spec)
	}
	owner, repo, ref := m[1], m[2], m[3]
	if ref == "" {
		ref = "main"
	}
	if !shaLikeRE.MatchString(ref) {
		fmt.Fprintf(os.Stderr, "scaffolds: warning: ref %q is not a pinned commit SHA; consider --source github:%s/%s@<sha> or @<tag> for reproducibility\n", ref, owner, repo)
	}

	url := fmt.Sprintf("%s/%s/%s/tar.gz/%s", githubBaseURL, owner, repo, ref)
	body, err := downloadTarball(ctx, url)
	if err != nil {
		return nil, err
	}

	tmpdir, err := os.MkdirTemp("", "fracta-scaffold-github-")
	if err != nil {
		return nil, fmt.Errorf("scaffolds: mkdtemp: %w", err)
	}
	if err := extractTarballGz(body, tmpdir); err != nil {
		_ = os.RemoveAll(tmpdir)
		return nil, err
	}

	// codeload tarballs always have a single top-level directory:
	// <repo>-<ref-with-slashes-as-dashes>. Find it dynamically rather than
	// reconstructing — refs with slashes (e.g. release branches) get
	// substituted in unpredictable ways by codeload.
	entries, err := os.ReadDir(tmpdir)
	if err != nil {
		_ = os.RemoveAll(tmpdir)
		return nil, fmt.Errorf("scaffolds: readdir %q: %w", tmpdir, err)
	}
	var rootName string
	for _, e := range entries {
		if e.IsDir() {
			rootName = e.Name()
			break
		}
	}
	if rootName == "" {
		_ = os.RemoveAll(tmpdir)
		return nil, fmt.Errorf("scaffolds: github tarball %s has no top-level directory", url)
	}
	rootDir := filepath.Join(tmpdir, rootName)

	// Validate the kind subdirectory exists; if not, fail with a directory
	// listing per spec-42 §7.
	kindDir := filepath.Join(rootDir, kind.String())
	if info, err := os.Stat(kindDir); err != nil || !info.IsDir() {
		topEntries, _ := os.ReadDir(rootDir)
		names := make([]string, 0, len(topEntries))
		for _, e := range topEntries {
			names = append(names, e.Name())
		}
		_ = os.RemoveAll(tmpdir)
		return nil, fmt.Errorf("scaffolds: github source %s does not contain a %q directory; got: %v", spec, kind.String(), names)
	}

	return &githubSource{
		owner:  owner,
		repo:   repo,
		ref:    ref,
		tmpdir: tmpdir,
		rootDir: rootDir,
		kind:    kind,
	}, nil
}

func (g *githubSource) RootFS() (fs.FS, error) {
	return os.DirFS(g.rootDir), nil
}

func (g *githubSource) FS() (fs.FS, error) {
	root, err := g.RootFS()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, g.kind.String())
	if err != nil {
		return nil, fmt.Errorf("scaffolds: github sub %q: %w", g.kind, err)
	}
	return sub, nil
}

func (g *githubSource) Description() string {
	return strings.Join([]string{"github:", g.owner, "/", g.repo, "@", g.ref}, "")
}

func (g *githubSource) Close() error {
	if g.tmpdir == "" {
		return nil
	}
	err := os.RemoveAll(g.tmpdir)
	g.tmpdir = ""
	return err
}
