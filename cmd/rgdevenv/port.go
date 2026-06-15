package main

import "github.com/spf13/cobra"

func newPortCmd() *cobra.Command {
	return &cobra.Command{Use: "port", Short: "Manage port reservations"}
}
