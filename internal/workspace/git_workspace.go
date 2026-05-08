package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitWorkspace creates agent directories as git worktrees on feature branches.
// It also implements the Integrator interface for branch merging.
type GitWorkspace struct {
	root string // project root (where .git lives)
}

// Compile-time interface checks.
var (
	_ Workspace  = (*GitWorkspace)(nil)
	_ Integrator = (*GitWorkspace)(nil)
)

// NewGitWorkspace creates a GitWorkspace rooted at the given project directory.
func NewGitWorkspace(root string) *GitWorkspace {
	return &GitWorkspace{root: root}
}

func (g *GitWorkspace) Create(agentID string, baseBranch string) (*Info, error) {
	path := filepath.Join(g.root, ".worktrees", agentID)
	branch := "feature/" + agentID

	cmd := exec.Command("git", "worktree", "add", path, "-b", branch, baseBranch)
	cmd.Dir = g.root
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}

	return &Info{
		Path:       path,
		BranchName: branch,
		BaseBranch: baseBranch,
	}, nil
}

func (g *GitWorkspace) Remove(info *Info, keepFiles bool) error {
	if keepFiles {
		return nil
	}

	cmd := exec.Command("git", "worktree", "remove", info.Path, "--force")
	cmd.Dir = g.root
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}

	// Delete the feature branch (best-effort — may already be merged/deleted).
	if info.BranchName != "" {
		del := exec.Command("git", "branch", "-D", info.BranchName)
		del.Dir = g.root
		del.Run() // ignore error
	}

	return nil
}

// IntegrateBranch merges the agent's feature branch into the current HEAD.
func (g *GitWorkspace) IntegrateBranch(info *Info) error {
	cmd := exec.Command("git", "merge", "--no-commit", "--no-ff", info.BranchName)
	cmd.Dir = g.root
	return cmd.Run()
}

// AbortMerge aborts a merge in progress.
func (g *GitWorkspace) AbortMerge() error {
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = g.root
	return cmd.Run()
}

// CurrentBranch returns the name of the currently checked-out branch.
func (g *GitWorkspace) CurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = g.root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
