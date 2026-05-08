package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/prereq"
	"github.com/darkquasar/fracta/internal/state/sqlitestore"
)

// defaultFractaYAML is the default fracta.yaml content written by fracta init.
// Uses project/agents/runtime sections per spec-15.
const defaultFractaYAML = `project:
  default_base_branch: main
  allowed_tools:
    - Read
    - Edit
    - Write
    - Glob
    - Grep
    - "Bash(git *)"
    - "Bash(git add * && git commit *)"
    - "Bash(git add . && git commit -m *)"
    - "Bash(git add *)"
    - "Bash(git commit *)"
    - "Bash(git merge green)"
    - "Bash(git merge main)"
    - "Bash(git merge *)"
    - "Bash(git status)"
    - "Bash(git status *)"
    - "Bash(git diff *)"
    - "Bash(git log *)"
    - "Bash(git branch *)"
    - "Bash(go *)"
    - "Bash(make *)"
    - "Bash(mkdir -p * && cd * && go mod init *)"
    - "Bash(mkdir *)"
    - "Bash(ls *)"
    - "Bash(cat *)"
    - "Bash(ls -a *)"
    - "Bash(ls -l *)"
    - "Bash(ls -la *)"
    - "Bash(cp *)"
    - "Bash(mv *)"
    - "Bash(rm *)"
    - "Bash(echo *)"
    - "Bash(touch *)"
    - "Bash(pwd)"
    - "Bash(wc *)"
    - "Bash(head *)"
    - "Bash(tail *)"
    - "Bash(diff *)"
    - "Bash(which *)"
    - "Bash(sort *)"
    - "Bash(uniq *)"
    - "Bash(tree *)"
    - "Bash(find *)"
    - "Bash(grep *)"
    - "Bash(sed *)"
    - "Bash(jq *)"

agents:
  default_runtime: claude
  default_mode: batch
  agent_runtimes:
    claude:
      adapter: claude
      model_tiers:
        heavy: opus
        medium: global.anthropic.claude-sonnet-4-5-20250929-v1:0
        light: haiku

runtime:
  backend: local
`

// Init initializes a fracta project at the given root directory.
// It verifies the directory is a git repo, checks dependencies,
// creates the .fracta directory structure, writes fracta.yaml defaults,
// initializes the SQLite database, and updates .gitignore.
func Init(root string) error {
	// Verify this is a git repo
	gitCheck := exec.Command("git", "rev-parse", "--git-dir")
	gitCheck.Dir = root
	if err := gitCheck.Run(); err != nil {
		return fmt.Errorf("current directory is not a git repository")
	}

	// Check dependencies
	if err := prereq.EnsureDeps(); err != nil {
		return err
	}

	fractaDir := filepath.Join(root, model.FractaDir)
	logsDir := filepath.Join(fractaDir, model.LogsDir)

	// Create directories
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("creating directories: %w", err)
	}

	// Write fracta.yaml if it doesn't exist (do NOT write .fracta/config.json).
	fractaYAMLPath := filepath.Join(root, "fracta.yaml")
	if _, err := os.Stat(fractaYAMLPath); os.IsNotExist(err) {
		if err := os.WriteFile(fractaYAMLPath, []byte(defaultFractaYAML), 0644); err != nil {
			return fmt.Errorf("writing fracta.yaml: %w", err)
		}
	}

	// Initialize SQLite database
	dbPath := filepath.Join(fractaDir, "state.db")
	store, err := sqlitestore.New(dbPath)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	store.Close()

	// Update .gitignore
	if err := ensureGitignore(root); err != nil {
		return fmt.Errorf("updating .gitignore: %w", err)
	}

	return nil
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
