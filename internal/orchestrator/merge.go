package orchestrator

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/darkquasar/fracta/internal/workspace"
)

// IntegrateBranch merges an agent's feature branch into the current HEAD
// (whatever branch the main repo is on). Non-destructive: the agent's workspace,
// state, and mailbox are left intact so it can continue working.
// Returns workspace.ErrMergeNotSupported if the workspace type has no branch support.
func (o *Orchestrator) IntegrateBranch(task string) error {
	integ, ok := o.Workspace.(workspace.Integrator)
	if !ok {
		return workspace.ErrMergeNotSupported
	}

	ctx := context.Background()
	agent, err := o.Store.FindAgent(ctx, task)
	if err != nil {
		return fmt.Errorf("looking up agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("agent %q not found", task)
	}

	integrationBranch, err := integ.CurrentBranch()
	if err != nil || integrationBranch == "" {
		integrationBranch = agent.BaseBranch
	}
	if integrationBranch == "" {
		integrationBranch = "main"
	}

	wsInfo := &workspace.Info{
		Path:       agent.WorkspacePath,
		BranchName: agent.BranchName,
		BaseBranch: agent.BaseBranch,
	}

	// Merge the agent's branch into current HEAD
	if err := integ.IntegrateBranch(wsInfo); err != nil {
		integ.AbortMerge()
		return fmt.Errorf("merge conflict on branch %q: %w", agent.BranchName, err)
	}

	commitMsg := fmt.Sprintf("Merge %s", agent.BranchName)
	cmd := exec.Command("git", "commit", "-m", commitMsg)
	cmd.Dir = o.Root
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("committing merge: %w", err)
	}

	o.updateChessmasterStatus(
		"Merge complete",
		fmt.Sprintf("Merged %s into %s", agent.BranchName, integrationBranch),
	)

	if err := o.broadcastMergeNotice(agent.Task, integrationBranch); err != nil {
		return err
	}

	return nil
}

func (o *Orchestrator) broadcastMergeNotice(sourceTask, integrationBranch string) error {
	st, err := o.Store.Load(context.Background())
	if err != nil {
		return err
	}

	message := fmt.Sprintf("Merged %s into %s. Run: git merge %s to sync.", sourceTask, integrationBranch, integrationBranch)
	confirm := fmt.Sprintf("Your work is now on %s. Let the chessmaster know if you need follow-up tasks.", integrationBranch)

	for _, agent := range st.Agents {
		var body string
		if agent.Task == sourceTask {
			body = confirm
		} else {
			body = message
		}
		if err := o.SendMessage("chessmaster", agent.Task, body); err != nil {
			return err
		}
	}

	return nil
}
