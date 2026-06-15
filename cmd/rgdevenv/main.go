// Command rgdevenv is an HTTPS reverse proxy that manages multiple virtual dev
// environments on a developer host. `serve` runs the daemon; other subcommands
// (added in Phase 2) are thin REST clients.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rgdevenv: error:", err)
		os.Exit(1)
	}
}
