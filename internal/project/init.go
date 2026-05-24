package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/prereq"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
)

// InitOpts modifies how Init scaffolds the project.
type InitOpts struct {
	// Scaffold selects which template tree to materialize.
	Scaffold scaffolds.Kind
	// Source is the resolved template source. nil = use embedded.
	Source scaffolds.Source
	// OnConflict controls behavior for files that already exist at root.
	OnConflict scaffolds.ConflictPolicy
}

// Init initializes a fracta project at root by materializing the requested
// scaffold tree. For local-process scaffolds it verifies the directory is a
// git repo (worktrees need a git store); kubernetes and docker-compose
// scaffolds run agents as Jobs/services and don't need .git. It runs prereq
// checks for the chosen scaffold, walks the source tree (honoring the
// conflict policy), initializes a SQLite state.db for KindLocal, and ensures
// .gitignore has the standard fracta entries.
//
// Caller is responsible for closing opts.Source.
func Init(root string, opts InitOpts) (scaffolds.Result, error) {
	// Verify this is a git repo, but only for the local scaffold. Kubernetes
	// and docker-compose deployments don't use git worktrees, so requiring a
	// .git store there would be a false-positive (spec-49 §1).
	if opts.Scaffold == scaffolds.KindLocal {
		gitCheck := exec.Command("git", "rev-parse", "--git-dir")
		gitCheck.Dir = root
		if err := gitCheck.Run(); err != nil {
			return scaffolds.Result{}, fmt.Errorf("local-process scaffolds require a git repository at %s; run 'git init' first or pick --scaffold k8s|docker-compose", root)
		}
	}

	// Check kind-specific dependencies.
	if err := prereq.EnsureDepsFor(opts.Scaffold); err != nil {
		return scaffolds.Result{}, err
	}

	src := opts.Source
	if src == nil {
		src = scaffolds.EmbeddedSource(opts.Scaffold)
	}

	// Default conflict policy is SkipExisting so re-running init never
	// silently clobbers operator edits (spec-42 §11 R6).
	policy := opts.OnConflict
	// Note: the zero value is ConflictFail; callers that want SkipExisting
	// must set it explicitly. This keeps Init programmatically strict; the
	// CLI layer (cmd/init.go) translates --force on/off into the right
	// policy for end-user ergonomics.

	res, err := scaffolds.Apply(context.Background(), src, root, scaffolds.ApplyOpts{
		OnConflict: policy,
	})
	if err != nil {
		return res, fmt.Errorf("applying scaffold: %w", err)
	}

	if opts.Scaffold == scaffolds.KindK8s {
		if err := fixupWorkspacePVC(root); err != nil {
			return res, fmt.Errorf("fixing workspace PVC path: %w", err)
		}
	}

	// SQLite is only meaningful for local mode — compose and k8s use
	// postgres-backed state in their deployed services.
	if opts.Scaffold == scaffolds.KindLocal {
		fractaDir := filepath.Join(root, model.FractaDir)
		if err := os.MkdirAll(filepath.Join(fractaDir, model.LogsDir), 0755); err != nil {
			return res, fmt.Errorf("creating directories: %w", err)
		}
		dbPath := filepath.Join(fractaDir, "state.db")
		store, err := sqlitestore.New(dbPath)
		if err != nil {
			return res, fmt.Errorf("initializing database: %w", err)
		}
		store.Close()
	}

	if err := ensureGitignore(root); err != nil {
		return res, fmt.Errorf("updating .gitignore: %w", err)
	}

	return res, nil
}

func fixupWorkspacePVC(root string) error {
	pvcPath := filepath.Join(root, "deployment", "k8s", "manifests", "workspace-pvc.yaml")

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	f, err := os.OpenFile(pvcPath, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open workspace-pvc.yaml: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read workspace-pvc.yaml: %w", err)
	}

	replaced := strings.ReplaceAll(string(data), "__PROJECT_ROOT__", absRoot)
	if replaced == string(data) {
		return nil
	}

	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteString(replaced)
	return err
}

func ensureGitignore(root string) error {
	gitignorePath := filepath.Join(root, ".gitignore")
	entries := []string{model.FractaDir + "/", model.WorktreeDir + "/"}

	var existing string
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	var toAdd []string
	for _, entry := range entries {
		if !strings.Contains(existing, entry) {
			toAdd = append(toAdd, entry)
		}
	}

	if len(toAdd) == 0 {
		return nil
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if existing != "" && !strings.HasSuffix(existing, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	for _, entry := range toAdd {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}

	return nil
}
