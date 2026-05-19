package cmd

import "github.com/spf13/cobra"

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debugging and diagnostic commands.",
}

func init() {
	rootCmd.AddCommand(debugCmd)
} 
