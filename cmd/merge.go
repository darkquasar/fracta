package cmd

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
)

var mergeCmd = &cobra.Command{
	Use:   "merge <name>",
	Short: "Merge an agent's feature branch into the current branch (non-destructive)",
	Args:  cobra.ExactArgs(1),
	RunE:  runMerge,
}

func init() {
	rootCmd.AddCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) error {
	task := args[0]

	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.Merge(context.Background(), cpapi.MergeRequest{Name: task})
	if err != nil {
		return err
	}

	fmt.Println(resp.Message)
	return nil
}
