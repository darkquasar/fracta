package main

import (
	"fmt"
	"os"

	"github.com/darkquasar/fracta/cmd"
)

// version is the binary version. Overridden at build time by:
//
//	go build -ldflags "-X main.version=v1.2.3" .
//
// The default "dev" applies to local builds with plain `go build` or `make build`.
var version = "dev"

func main() {
	cmd.SetVersion(version)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
