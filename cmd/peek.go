package cmd

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
)

var peekCmd = &cobra.Command{
	Use:   "peek <name>",
	Short: "Peek at an agent's log output",
	Args:  cobra.ExactArgs(1),
	RunE:  runPeek,
}

func init() {
	rootCmd.AddCommand(peekCmd)
}

func runPeek(cmd *cobra.Command, args []string) error {
	task := args[0]

	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.Peek(context.Background(), cpapi.PeekRequest{Name: task})
	if err != nil {
		return err
	}

	fmt.Print(resp.Output)
	return nil
}
