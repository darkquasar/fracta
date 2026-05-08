package git

import (
	"os/exec"
	"strings"
)

type Runner struct {
	root string
}

func NewRunner(root string) *Runner {
	return &Runner{root: root}
}

func (r *Runner) AddWorktree(path, branch, base string) error {
	cmd := exec.Command("git", "worktree", "add", path, "-b", branch, base)
	cmd.Dir = r.root
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func (r *Runner) RemoveWorktree(path string) error {
	cmd := exec.Command("git", "worktree", "remove", path, "--force")
	cmd.Dir = r.root
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func (r *Runner) MergeBranch(branch string) error {
	cmd := exec.Command("git", "merge", "--no-commit", "--no-ff", branch)
	cmd.Dir = r.root
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func (r *Runner) AbortMerge() error {
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = r.root
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func (r *Runner) DeleteBranch(branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = r.root
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// CurrentBranch returns the name of the currently checked-out branch.
func (r *Runner) CurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = r.root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
