package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirectoryWorkspace creates agent directories as plain filesystem directories.
// It does NOT implement Integrator — merge attempts will fail with a clear error.
// Used for K8s mode where agents work in PVC-backed directories without git.
type DirectoryWorkspace struct {
	base string // base directory under which agent dirs are created
}

// Compile-time interface check — intentionally does NOT include Integrator.
var _ Workspace = (*DirectoryWorkspace)(nil)

// NewDirectoryWorkspace creates a DirectoryWorkspace. Agent directories will be
// created under base/<agentID>/.
func NewDirectoryWorkspace(base string) *DirectoryWorkspace {
	return &DirectoryWorkspace{base: base}
}

func (d *DirectoryWorkspace) Create(agentID string, _ string) (*Info, error) {
	path := filepath.Join(d.base, agentID)
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}
	return &Info{
		Path: path,
		// BranchName and BaseBranch intentionally empty — no git.
	}, nil
}

func (d *DirectoryWorkspace) Remove(info *Info, keepFiles bool) error {
	if keepFiles {
		return nil
	}
	if err := os.RemoveAll(info.Path); err != nil {
		return fmt.Errorf("removing workspace directory: %w", err)
	}
	return nil
}
