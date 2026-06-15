package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

func newLBCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "lb", Short: "Manage load balancers"}
	cmd.AddCommand(newLBAddCmd(), newLBSetCmd(), newLBRmCmd(), newLBLsCmd())
	return cmd
}

func newLBAddCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a load balancer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.CreateLB(cmd.Context(), args[0], label)
			if err != nil {
				return err
			}
			return printLB(cmd, lb)
		},
	}
	c.Flags().StringVar(&label, "label", "", "human-readable label")
	return c
}

func newLBSetCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "set <name> --label TEXT",
		Short: "Update a load balancer's label",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.SetLBLabel(cmd.Context(), args[0], label)
			if err != nil {
				return err
			}
			return printLB(cmd, lb)
		},
	}
	c.Flags().StringVar(&label, "label", "", "new label")
	_ = c.MarkFlagRequired("label")
	return c
}

func newLBRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a load balancer (and its mappings)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.DeleteLB(cmd.Context(), args[0]); err != nil {
				return err
			}
			return printDeleted(cmd, "load balancer "+args[0])
		},
	}
}

func newLBLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List load balancers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lbs, err := cl.ListLBs(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), lbs)
			}
			sort.Slice(lbs, func(i, j int) bool { return lbs[i].Name < lbs[j].Name })
			rows := make([][]string, 0, len(lbs))
			for _, lb := range lbs {
				rows = append(rows, []string{lb.Name, lb.Label, fmt.Sprintf("%d", len(lb.Mappings))})
			}
			renderTable(cmd.OutOrStdout(), []string{"NAME", "LABEL", "MAPPINGS"}, rows)
			return nil
		},
	}
}

// printLB outputs a single load balancer — table format or JSON per --json flag.
func printLB(cmd *cobra.Command, lb client.LoadBalancer) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), lb)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", lb.Name, lb.Label)
	return nil
}

// printDeleted outputs a deletion confirmation — shared by lb rm, map rm, port return.
// AIDEV-NOTE: intentionally lives here (lb.go) to avoid duplication; Tasks 9-10 call this.
func printDeleted(cmd *cobra.Command, what string) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), map[string]string{"deleted": what})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", what)
	return nil
}
