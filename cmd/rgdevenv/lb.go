package main

import "github.com/spf13/cobra"

func newLBCmd() *cobra.Command { return &cobra.Command{Use: "lb", Short: "Manage load balancers"} }
