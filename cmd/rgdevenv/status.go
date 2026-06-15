package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newCACmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ca", Short: "Custom CAs"}
	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List available custom-CA names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			cas, err := cl.ListCAs(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), cas)
			}
			sort.Strings(cas)
			for _, name := range cas {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	})
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server status and upstream health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			s, err := cl.Status(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), s)
			}
			out := cmd.OutOrStdout()
			ports := make([]string, 0, len(s.ActiveListeners))
			for _, p := range s.ActiveListeners {
				ports = append(ports, fmt.Sprintf("%d", p))
			}
			fmt.Fprintf(out, "version %s\n", s.Version)
			fmt.Fprintf(out, "listeners %s\n", strings.Join(ports, ","))
			fmt.Fprintf(out, "load_balancers %d  mappings %d  allocations %d\n", s.LoadBalancers, s.Mappings, s.Allocations)
			if len(s.Upstreams) > 0 {
				rows := make([][]string, 0, len(s.Upstreams))
				for _, u := range s.Upstreams {
					rows = append(rows, []string{fmt.Sprintf("%s://%s:%d", u.Scheme, u.Host, u.Port), u.TLSMode, u.Health})
				}
				renderTable(out, []string{"UPSTREAM", "TLS", "HEALTH"}, rows)
			}
			return nil
		},
	}
}
