// Package workspace defines interfaces for agent working directory lifecycle.
// Implementations handle directory creation, cleanup, and optionally branch-based
// merge operations. Agent bootstrapping (writing .claude/settings.json, .mcp.json,
// CLAUDE.md) is NOT part of this interface — that stays in the orchestrator.
package workspace

import "fmt"

// Workspace manages directory and branch lifecycle for agent working directories.
type Workspace interface {
	// Create sets up an isolated directory for an agent and returns metadata.
	// baseBranch is a hint for git-based workspaces; directory workspaces ignore it.
	Create(agentID string, baseBranch string) (*Info, error)

	// Remove tears down the workspace directory and any associated metadata
	// (e.g., git branch). If keepFiles is true, the directory is preserved.
	Remove(info *Info, keepFiles bool) error
}

// Integrator is an optional capability for branch-based merge operations.
// Only git-based workspaces implement this. Callers should type-assert:
//
//	if integ, ok := ws.(workspace.Integrator); ok { ... }
//
// Non-git workspaces must NOT implement this interface — merge attempts
// should fail with a clear error via the orchestrator, not silently no-op.
type Integrator interface {
	// IntegrateBranch merges the agent's branch into the current HEAD.
	IntegrateBranch(info *Info) error

	// AbortMerge aborts a merge in progress.
	AbortMerge() error

	// CurrentBranch returns the name of the current branch at the project root.
	CurrentBranch() (string, error)
}

// Info holds metadata about a created workspace.
type Info struct {
	// Path is the absolute path to the agent's working directory.
	Path string

	// BranchName is the git branch name (e.g., "feature/agent-01").
	// Empty for non-git workspaces.
	BranchName string

	// BaseBranch is the branch this workspace was created from.
	// Empty for non-git workspaces.
	BaseBranch string
}

// ErrMergeNotSupported is returned when merge is attempted on a workspace
// that does not support branch integration.
var ErrMergeNotSupported = fmt.Errorf("merge not supported: workspace type does not support branch integration")
