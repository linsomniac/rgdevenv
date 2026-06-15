package main

import "github.com/spf13/cobra"

func newMapCmd() *cobra.Command { return &cobra.Command{Use: "map", Short: "Manage mappings"} }
