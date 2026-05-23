package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// --- commandHasAnnotation ---

func TestCommandHasAnnotation_DirectMatch(t *testing.T) {
	c := &cobra.Command{
		Use:         "x",
		Annotations: map[string]string{RequiresFractaYAMLAnnotation: "true"},
	}
	if !commandHasAnnotation(c, RequiresFractaYAMLAnnotation) {
		t.Error("expected direct annotation match")
	}
}

func TestCommandHasAnnotation_ParentInheritance(t *testing.T) {
	parent := &cobra.Command{
		Use:         "parent",
		Annotations: map[string]string{RequiresFractaYAMLAnnotation: "true"},
	}
	child := &cobra.Command{Use: "child"}
	parent.AddCommand(child)

	if !commandHasAnnotation(child, RequiresFractaYAMLAnnotation) {
		t.Error("expected child to inherit parent's annotation")
	}
}

func TestCommandHasAnnotation_MissingDefaultsFalse(t *testing.T) {
	c := &cobra.Command{Use: "x"} // no Annotations map at all
	if commandHasAnnotation(c, RequiresFractaYAMLAnnotation) {
		t.Error("missing annotation should return false")
	}
}

func TestCommandHasAnnotation_OtherKeyDoesNotMatch(t *testing.T) {
	c := &cobra.Command{
		Use:         "x",
		Annotations: map[string]string{RequiresFractaYAMLAnnotation: "true"},
	}
	if commandHasAnnotation(c, RequiresGitWorktreeAnnotation) {
		t.Error("different annotation key should not match")
	}
}

// --- assertGitWorktree ---

func TestAssertGitWorktree_DirectorySucceeds(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := assertGitWorktree(root); err != nil {
		t.Errorf("expected success with .git dir, got: %v", err)
	}
}

func TestAssertGitWorktree_FilePointerSucceeds(t *testing.T) {
	// fracta's own worktrees have .git as a file pointing to a gitdir.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := assertGitWorktree(root); err != nil {
		t.Errorf("expected success with .git file (worktree pointer), got: %v", err)
	}
}

func TestAssertGitWorktree_MissingFails(t *testing.T) {
	root := t.TempDir() // no .git
	err := assertGitWorktree(root)
	if err == nil {
		t.Fatal("expected error when .git is missing")
	}
	if !strings.Contains(err.Error(), "local-process deployments require a git repository") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- PersistentPreRunE — integration via the real rootCmd hook ---

// runHookWith executes rootCmd.PersistentPreRunE against the given target
// command from inside dir (it changes CWD to dir and restores afterwards).
func runHookWith(t *testing.T, dir string, target *cobra.Command) error {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	// projectRoot is package-level — reset between tests.
	oldRoot := projectRoot
	projectRoot = ""
	t.Cleanup(func() { projectRoot = oldRoot })
	return rootCmd.PersistentPreRunE(target, nil)
}

func writeFractaProject(t *testing.T, dir string, fractaYAMLContent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".fracta"), 0755); err != nil {
		t.Fatal(err)
	}
	if fractaYAMLContent != "" {
		if err := os.WriteFile(filepath.Join(dir, "fracta.yaml"), []byte(fractaYAMLContent), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPersistentPreRunE_NoAnnotation_NoLookup(t *testing.T) {
	dir := t.TempDir() // no .fracta, no .git
	target := &cobra.Command{Use: "general"}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	if err := runHookWith(t, dir, target); err != nil {
		t.Errorf("commands with no annotation should skip project lookup; got: %v", err)
	}
}

func TestPersistentPreRunE_FractaYAMLAnnotation_LoadsProject(t *testing.T) {
	dir := t.TempDir()
	writeFractaProject(t, dir, "runtime:\n  backend: kubernetes\n  staging_dir: /tmp/fracta-staging\n")

	target := &cobra.Command{
		Use:         "needs-project",
		Annotations: map[string]string{RequiresFractaYAMLAnnotation: "true"},
	}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	if err := runHookWith(t, dir, target); err != nil {
		t.Errorf("hook should succeed when .fracta exists; got: %v", err)
	}
}

func TestPersistentPreRunE_FractaYAMLAnnotation_MissingProjectErrors(t *testing.T) {
	dir := t.TempDir() // no .fracta

	target := &cobra.Command{
		Use:         "needs-project",
		Annotations: map[string]string{RequiresFractaYAMLAnnotation: "true"},
	}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	err := runHookWith(t, dir, target)
	if err == nil {
		t.Fatal("expected error when fracta.yaml annotation set but no project found")
	}
	if !strings.Contains(err.Error(), "not a fracta project") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPersistentPreRunE_GitWorktree_LocalAsserted(t *testing.T) {
	dir := t.TempDir()
	// Default runtime = local; no .git → assertion should fire.
	writeFractaProject(t, dir, "runtime:\n  backend: local\n")

	target := &cobra.Command{
		Use: "needs-worktree",
		Annotations: map[string]string{
			RequiresFractaYAMLAnnotation:  "true",
			RequiresGitWorktreeAnnotation: "true",
		},
	}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	err := runHookWith(t, dir, target)
	if err == nil {
		t.Fatal("expected git-worktree assertion to fire in local mode without .git")
	}
	if !strings.Contains(err.Error(), "local-process deployments require a git repository") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPersistentPreRunE_GitWorktree_KubernetesSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFractaProject(t, dir, "runtime:\n  backend: kubernetes\n  staging_dir: /tmp/fracta-staging\n")
	// NO .git on purpose.

	target := &cobra.Command{
		Use: "needs-worktree",
		Annotations: map[string]string{
			RequiresFractaYAMLAnnotation:  "true",
			RequiresGitWorktreeAnnotation: "true",
		},
	}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	if err := runHookWith(t, dir, target); err != nil {
		t.Errorf("kubernetes profile should skip .git assertion; got: %v", err)
	}
}

func TestPersistentPreRunE_GitWorktree_ComposeSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFractaProject(t, dir, "runtime:\n  backend: local\n") // local backend …
	// … but a docker-compose.yml on disk reclassifies as compose.
	if err := os.MkdirAll(filepath.Join(dir, "deployment"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deployment", "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	target := &cobra.Command{
		Use: "needs-worktree",
		Annotations: map[string]string{
			RequiresFractaYAMLAnnotation:  "true",
			RequiresGitWorktreeAnnotation: "true",
		},
	}
	rootCmd.AddCommand(target)
	t.Cleanup(func() { rootCmd.RemoveCommand(target) })

	if err := runHookWith(t, dir, target); err != nil {
		t.Errorf("docker-compose project should skip .git assertion; got: %v", err)
	}
}
