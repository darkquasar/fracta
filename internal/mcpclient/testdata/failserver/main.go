// failserver is a minimal binary that writes to stderr and exits non-zero.
// Used by pool diagnostics tests to verify enriched error messages.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "failserver: config file not found")
	os.Exit(42)
}
