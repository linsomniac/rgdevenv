// Command rgdevenv is an HTTPS reverse proxy that manages multiple virtual dev
// environments on a developer host. `serve` runs the daemon; other subcommands
// (added in Phase 2) are thin REST clients.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "rgdevenv",
		Short:         "HTTPS reverse proxy for managing dev environments",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addClientFlags(root)
	root.AddCommand(newServeCmd())
	root.AddCommand(newLBCmd(), newMapCmd(), newPortCmd(), newCACmd(), newStatusCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rgdevenv: error:", err)
		os.Exit(1)
	}
}
