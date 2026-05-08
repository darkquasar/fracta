// One-shot graph migration runner. Usage: go run ./cmd/migrate [addr]
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/darkquasar/fracta/internal/graph"
)

func main() {
	addr := "localhost:6379"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	client := graph.NewFalkorDBClient(addr, graph.WithGraphName("fracta_knowledge"))
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach FalkorDB at %s: %v\n", addr, err)
		os.Exit(1)
	}

	fmt.Println("Running MigrateGraph...")
	if err := graph.MigrateGraph(ctx, client); err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Migration complete.")
}
