package cmd

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/graph"
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Knowledge graph management commands",
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

var (
	seedDir  string
	seedAddr string
)

var graphSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Load seed Cypher files into FalkorDB",
	RunE:  runGraphSeed,
}

func init() {
	graphSeedCmd.Flags().StringVar(&seedDir, "dir", "strategies/seeds", "directory containing .cypher seed files")
	graphSeedCmd.Flags().StringVar(&seedAddr, "addr", "localhost:6379", "FalkorDB address")
	graphCmd.AddCommand(graphSeedCmd)
	rootCmd.AddCommand(graphCmd)
}

func runGraphSeed(cmd *cobra.Command, args []string) error {
	gc := graph.NewFalkorDBClient(seedAddr)
	defer gc.Close()

	count, err := graph.SeedFromDir(context.Background(), gc, seedDir)
	if err != nil {
		return fmt.Errorf("seeding failed: %w", err)
	}

	fmt.Printf("Seeded %d statements from %s\n", count, seedDir)
	return nil
}
