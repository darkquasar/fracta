package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all agents",
	RunE:  runList,
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.ListAgents(context.Background(), cpapi.ListAgentsRequest{})
	if err != nil {
		return err
	}

	if len(resp.Agents) == 0 {
		fmt.Println("No agents running.")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "AGENT\tSTATUS\tMODE\tBRANCH\tINTENT\tUNREAD")

		for _, agent := range resp.Agents {
			intent := agent.CurrentIntent
			if intent == "" {
				intent = "-"
			}
			fmt.Fprintf(
				w,
				"%s\t%s\t%s\t%s\t%s\t%d\n",
				agent.Name,
				agent.Status,
				agent.Mode,
				agent.Branch,
				intent,
				agent.UnreadMessages,
			)
		}

		if err := w.Flush(); err != nil {
			return err
		}
	}

	return nil
}

func relativeTime(ts time.Time) string {
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
