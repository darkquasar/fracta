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

// githubURLRE matches https://github.com/owner/repo[.git][@ref][/] (browser
// URL form). Trailing slash and .git suffix tolerated; ref optional. Per
// spec-43 §7, ref defaults to "main" when omitted. Browser viewer URLs
// (/tree/<ref>, /blob/<path>) and archive URLs (/archive/...) are NOT
// matched by this regex — they fall through to the caller (which emits a
// helpful error for tree/blob and routes archive URLs to HttpsSource).
var githubURLRE = regexp.MustCompile(
	`^https://github\.com/([^/]+)/([^/@]+?)(?:\.git)?(?:@([^/]+))?/?$`,
)

// githubSSHRE matches git@github.com:owner/repo[.git][@ref]. The SSH form is
// recognized for operator UX (paste from `git remote -v`) but fetched over
// HTTPS codeload — fracta does not run SSH transport.
var githubSSHRE = regexp.MustCompile(
	`^git@github\.com:([^/]+)/([^/@]+?)(?:\.git)?(?:@(.+))?$`,
)

// githubBaseURL is the codeload host. Tests override this to point at a
// local httptest.Server.
var githubBaseURL = "https://codeload.github.com"

// SetGithubBaseURLForTest swaps the codeload base URL and returns the
// previous value. Test-only — production callers must never use this. Lives
// in production source rather than an _test.go file so cross-package tests
// (e.g. mcpcatalog/fetch_test) can drive the codeload host through this
// seam without copy-pasting the variable.
func SetGithubBaseURLForTest(url string) string {
	prev := githubBaseURL
	githubBaseURL = url
	return prev
}

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
	return githubSourceImpl(ctx, owner, repo, ref, kind)
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

// ParseGithubURL parses the browser URL form of a github source spec:
//
//	https://github.com/owner/repo            → ("owner", "repo", "main", true)
//	https://github.com/owner/repo.git        → .git stripped, ref "main"
//	https://github.com/owner/repo@v1.2.3     → ref "v1.2.3"
//	https://github.com/owner/repo.git@v1     → both
//	https://github.com/owner/repo/           → trailing slash tolerated
//
// Returns ok=false (with zero values) for any non-matching URL. Specifically
// rejected: /tree/<ref>, /blob/<path>, and archive/... codeload URLs.
// Per spec-43 §7, callers may emit a clearer error for /tree/ and /blob/
// shapes (see the catalog package's resolveCatalogSource).
func ParseGithubURL(spec string) (owner, repo, ref string, ok bool) {
	m := githubURLRE.FindStringSubmatch(spec)
	if m == nil {
		return "", "", "", false
	}
	owner = m[1]
	repo = m[2]
	ref = m[3]
	if ref == "" {
		ref = "main"
	}
	return owner, repo, ref, true
}

// ParseGithubSSH parses the SSH form pasted from `git remote -v`:
//
//	git@github.com:owner/repo            → ("owner", "repo", "main", true)
//	git@github.com:owner/repo.git        → .git stripped
//	git@github.com:owner/repo@v1.2.3     → ref "v1.2.3"
//	git@github.com:owner/repo.git@v1     → both
//
// Only github.com is recognized. Returns ok=false for any other SSH host or
// the full `ssh://git@github.com/...` URL form.
func ParseGithubSSH(spec string) (owner, repo, ref string, ok bool) {
	m := githubSSHRE.FindStringSubmatch(spec)
	if m == nil {
		return "", "", "", false
	}
	owner = m[1]
	repo = m[2]
	ref = m[3]
	if ref == "" {
		ref = "main"
	}
	return owner, repo, ref, true
}

// GithubSourceFromParts constructs a Source from the already-split (owner,
// repo, ref) tuple. Equivalent to GithubSource but skips the `github:owner/
// repo@ref` regex parse — used by ParseGithubURL / ParseGithubSSH callers.
//
// The kind argument selects which `<kind>/` subdirectory FS() returns; for
// callers that only consume RootFS() (spec-43 fetch dispatcher), the kind
// is unused after construction. Validation still runs: the constructor
// requires the kind subdir to exist in the extracted tree.
func GithubSourceFromParts(ctx context.Context, owner, repo, ref string, kind Kind) (Source, error) {
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("scaffolds: github source requires owner and repo; got owner=%q repo=%q", owner, repo)
	}
	if ref == "" {
		ref = "main"
	}
	if !shaLikeRE.MatchString(ref) {
		fmt.Fprintf(os.Stderr, "scaffolds: warning: ref %q is not a pinned commit SHA; consider github:%s/%s@<sha> or @<tag> for reproducibility\n", ref, owner, repo)
	}
	return githubSourceImpl(ctx, owner, repo, ref, kind)
}

// githubSourceImpl is the shared body called by both GithubSource (regex
// path) and GithubSourceFromParts (already-split path). Extracted to keep
// the public constructors thin.
func githubSourceImpl(ctx context.Context, owner, repo, ref string, kind Kind) (Source, error) {
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

	if kind != KindNone {
		kindDir := filepath.Join(rootDir, kind.String())
		if info, err := os.Stat(kindDir); err != nil || !info.IsDir() {
			topEntries, _ := os.ReadDir(rootDir)
			names := make([]string, 0, len(topEntries))
			for _, e := range topEntries {
				names = append(names, e.Name())
			}
			_ = os.RemoveAll(tmpdir)
			return nil, fmt.Errorf("scaffolds: github source github:%s/%s@%s does not contain a %q directory; got: %v", owner, repo, ref, kind.String(), names)
		}
	}

	return &githubSource{
		owner:   owner,
		repo:    repo,
		ref:     ref,
		tmpdir:  tmpdir,
		rootDir: rootDir,
		kind:    kind,
	}, nil
}
