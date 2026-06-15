package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
)

func newPortCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "port", Short: "Manage port reservations"}
	cmd.AddCommand(newPortGetCmd(), newPortReturnCmd(), newPortLsCmd())
	return cmd
}

func newPortGetCmd() *cobra.Command {
	var owner, label string
	c := &cobra.Command{
		Use:   "get",
		Short: "Allocate a port (prints id and port)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			a, err := cl.AllocatePort(cmd.Context(), owner, label)
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), a)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d\n", a.ID, a.Port)
			return nil
		},
	}
	c.Flags().StringVar(&owner, "owner", "", "owner of the reservation")
	c.Flags().StringVar(&label, "label", "", "label for the reservation")
	return c
}

func newPortReturnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "return <port>",
		Short: "Return a reserved port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("port must be an integer: %q", args[0])
			}
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.ReturnPort(cmd.Context(), port); err != nil {
				return err
			}
			return printDeleted(cmd, fmt.Sprintf("port %d", port))
		},
	}
}

func newPortLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the port pool and allocations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			pp, err := cl.ListPorts(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), pp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pool %d-%d  used %d  free %d\n", pp.Start, pp.End, pp.Used, pp.Free)
			allocs := pp.Allocations
			sort.Slice(allocs, func(i, j int) bool { return allocs[i].Port < allocs[j].Port })
			rows := make([][]string, 0, len(allocs))
			for _, a := range allocs {
				rows = append(rows, []string{fmt.Sprintf("%d", a.Port), a.ID, a.Owner, a.Label, fmt.Sprintf("%t", a.Auto)})
			}
			renderTable(cmd.OutOrStdout(), []string{"PORT", "ID", "OWNER", "LABEL", "AUTO"}, rows)
			return nil
		},
	}
}
