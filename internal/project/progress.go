package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/model"
)

// WriteProgressSnapshot renders a lightweight Markdown summary of agent progress
// and writes it to .fracta/progress.md so humans and agents can inspect status at
// a glance.
func WriteProgressSnapshot(root string, st model.State) error {
	if root == "" {
		return fmt.Errorf("project root not provided")
	}

	var b strings.Builder
	b.WriteString("# Fracta Agent Progress\n\n")

	if len(st.Agents) == 0 {
		b.WriteString("_No agents currently active._\n")
	} else {
		for _, agent := range st.Agents {
			b.WriteString(fmt.Sprintf("## %s\n", agent.Task))
			b.WriteString(fmt.Sprintf("- Status: %s", agent.Status))
			if agent.Mode != "" {
				b.WriteString(fmt.Sprintf(" (%s)", strings.ToLower(agent.Mode)))
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("- Branch: %s\n", agent.BranchName))
			if agent.CurrentIntent != "" {
				b.WriteString(fmt.Sprintf("- Intent: %s\n", agent.CurrentIntent))
			} else {
				b.WriteString("- Intent: (not set)\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Chessmaster\n")
	status := "Idle"
	if st.Chessmaster.Status != "" {
		status = st.Chessmaster.Status
	}
	b.WriteString(fmt.Sprintf("- Status: %s\n", status))
	if st.Chessmaster.LastAction != "" {
		b.WriteString(fmt.Sprintf("- Last action: %s\n", st.Chessmaster.LastAction))
	}
	if !st.Chessmaster.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- Updated: %s\n", formatAgo(st.Chessmaster.UpdatedAt)))
	}

	path := filepath.Join(root, model.FractaDir, "progress.md")
	return os.WriteFile(path, []byte(strings.TrimSpace(b.String())+"\n"), 0644)
}

func formatAgo(ts time.Time) string {
	delta := time.Since(ts)
	if delta < time.Minute {
		return "just now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%d minutes ago", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%d hours ago", int(delta.Hours()))
	}
	return ts.Format(time.RFC3339)
}
