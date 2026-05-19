// Command overcast is the unified CLI for the Overcast AWS emulator.
//
// When running natively (installed via brew, scoop, or `go install`), all
// subcommands are available:
//
//   - overcast serve        — start the emulator daemon
//   - overcast bridge       — publish *.local domains via mDNS + port-80 proxy
//   - overcast trust        — manage the local trust store for TLS certificates
//   - overcast status       — inspect a running daemon
//
// The Docker image uses `overcast serve` as its entrypoint. Host-only
// commands (bridge, trust) require host-network access and are not useful
// inside a container.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overwritten at build time via -ldflags.
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "overcast",
		Short:         "AWS service emulator — daemon and host-side tooling",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("endpoint", "http://localhost:4566", "overcast daemon base URL")

	root.AddCommand(newServeCmd())
	root.AddCommand(newBridgeCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newTrustCmd())
	root.AddCommand(newImportCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "overcast:", err)
		os.Exit(1)
	}
}
