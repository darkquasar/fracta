package cmd

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
)

var keepFiles bool

var killCmd = &cobra.Command{
	Use:   "kill <name>",
	Short: "Kill an agent, removing its worktree and state",
	Args:  cobra.ExactArgs(1),
	RunE:  runKill,
}

func init() {
	killCmd.Flags().BoolVar(&keepFiles, "keep-files", false, "keep the worktree files after killing the agent")
	rootCmd.AddCommand(killCmd)
}

func runKill(cmd *cobra.Command, args []string) error {
	task := args[0]

	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	_, err = client.Kill(context.Background(), cpapi.KillRequest{
		Name:      task,
		KeepFiles: keepFiles,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Agent %q killed.\n", task)
	return nil
}
