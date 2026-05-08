package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/cpapi"
	"github.com/spf13/cobra"
)

var sayCmd = &cobra.Command{
	Use:   "say <name> <message>",
	Short: "Send a follow-up message to an agent, resuming its session",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runSay,
}

func init() {
	rootCmd.AddCommand(sayCmd)
}

func runSay(cmd *cobra.Command, args []string) error {
	task := args[0]
	message := strings.Join(args[1:], " ")

	client, cleanup, err := buildCLIClient(projectRoot)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := client.Say(context.Background(), cpapi.SayRequest{
		Name:    task,
		Message: message,
	})
	if err != nil {
		return err
	}

	fmt.Println(resp.Message)
	return nil
}
