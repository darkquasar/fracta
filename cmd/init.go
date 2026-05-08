package cmd

import (
	"fmt"
	"os"

	"github.com/darkquasar/fracta/internal/project"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize fracta in the current git repository",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	root := projectRoot
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	if err := project.Init(root); err != nil {
		return err
	}

	fmt.Println("Fracta initialized successfully.")
	return nil
}
