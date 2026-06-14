package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// newServeCmd returns the `serve` subcommand. The body is implemented in Task 19.
func newServeCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the rgdevenv proxy daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// AIDEV-TODO: implemented in Task 19 (load → lock → Apply → run).
			_ = configPath
			return errors.New("serve: not implemented yet")
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/rgdevenv/config.toml", "path to config file")
	return cmd
}
