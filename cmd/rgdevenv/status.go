package main

import "github.com/spf13/cobra"

func newCACmd() *cobra.Command     { return &cobra.Command{Use: "ca", Short: "Custom CAs"} }
func newStatusCmd() *cobra.Command { return &cobra.Command{Use: "status", Short: "Show server status"} }
