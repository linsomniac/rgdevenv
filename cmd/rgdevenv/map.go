package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

func newMapCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "map", Short: "Manage mappings"}
	cmd.AddCommand(newMapAddCmd(), newMapSetCmd(), newMapRmCmd(), newMapLsCmd())
	return cmd
}

// mapFlags are shared by add and set.
type mapFlags struct {
	upstream    string
	listenPort  int
	noTLS       bool
	upstreamTLS string
	caName      string
	allocate    bool
	label       string
}

func (f mapFlags) request(cmd *cobra.Command) (client.MappingRequest, error) {
	port := f.listenPort
	tls := !f.noTLS
	req := client.MappingRequest{ListenPort: &port, ListenTLS: &tls, Allocate: f.allocate, Label: f.label}
	if f.allocate {
		if f.upstream != "" {
			return req, fmt.Errorf("--allocate and --upstream are mutually exclusive")
		}
		return req, nil
	}
	if f.upstream == "" {
		return req, fmt.Errorf("--upstream URL is required (or use --allocate)")
	}
	scheme, host, uport, err := client.ParseUpstreamURL(f.upstream)
	if err != nil {
		return req, err
	}
	mode := f.upstreamTLS
	if mode == "" {
		mode = "verify"
	}
	req.Upstream = &client.UpstreamRequest{
		Scheme: scheme, Host: host, Port: uport,
		TLS: client.UpstreamTLSRequest{Mode: mode, CAName: f.caName},
	}
	return req, nil
}

func addMapFlags(c *cobra.Command, f *mapFlags) {
	c.Flags().StringVar(&f.upstream, "upstream", "", "upstream URL, e.g. http://localhost:9011")
	c.Flags().IntVar(&f.listenPort, "listen-port", 443, "front-end listen port")
	c.Flags().BoolVar(&f.noTLS, "no-tls", false, "serve this listen port as plain HTTP")
	c.Flags().StringVar(&f.upstreamTLS, "upstream-tls", "", "upstream TLS mode: verify|skip|ca")
	c.Flags().StringVar(&f.caName, "ca-name", "", "custom CA name (for --upstream-tls ca)")
	c.Flags().StringVar(&f.label, "label", "", "label for an auto-allocated port")
}

func newMapAddCmd() *cobra.Command {
	var f mapFlags
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a mapping to a load balancer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.request(cmd)
			if err != nil {
				return err
			}
			m, err := cl.PutMapping(cmd.Context(), args[0], req, false)
			if err != nil {
				return err
			}
			return printMapping(cmd, m)
		},
	}
	addMapFlags(c, &f)
	c.Flags().BoolVar(&f.allocate, "allocate", false, "allocate a port and map :listen-port → http://localhost:<port>")
	return c
}

func newMapSetCmd() *cobra.Command {
	var f mapFlags
	c := &cobra.Command{
		Use:   "set <name> --listen-port N",
		Short: "Replace a mapping (by listen port)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.request(cmd)
			if err != nil {
				return err
			}
			m, err := cl.PutMapping(cmd.Context(), args[0], req, true)
			if err != nil {
				return err
			}
			return printMapping(cmd, m)
		},
	}
	addMapFlags(c, &f)
	c.Flags().BoolVar(&f.allocate, "allocate", false, "allocate a port for the replacement")
	_ = c.MarkFlagRequired("listen-port")
	return c
}

func newMapRmCmd() *cobra.Command {
	var listenPort int
	c := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a mapping (by listen port)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.DeleteMapping(cmd.Context(), args[0], listenPort); err != nil {
				return err
			}
			return printDeleted(cmd, fmt.Sprintf("mapping %s:%d", args[0], listenPort))
		},
	}
	c.Flags().IntVar(&listenPort, "listen-port", 443, "listen port of the mapping to remove")
	return c
}

func newMapLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <name>",
		Short: "List a load balancer's mappings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.GetLB(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), lb.Mappings)
			}
			ms := lb.Mappings
			sort.Slice(ms, func(i, j int) bool { return ms[i].ListenPort < ms[j].ListenPort })
			rows := make([][]string, 0, len(ms))
			for _, m := range ms {
				up := fmt.Sprintf("%s://%s:%d", m.Upstream.Scheme, m.Upstream.Host, m.Upstream.Port)
				rows = append(rows, []string{
					fmt.Sprintf("%d", m.ListenPort), fmt.Sprintf("%t", m.ListenTLS),
					up, m.Upstream.TLS.Mode, healthOrDash(m.Health),
				})
			}
			renderTable(cmd.OutOrStdout(), []string{"LISTEN", "TLS", "UPSTREAM", "UP-TLS", "HEALTH"}, rows)
			return nil
		},
	}
}

func printMapping(cmd *cobra.Command, m client.Mapping) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), m)
	}
	fmt.Fprintf(cmd.OutOrStdout(), ":%d → %s://%s:%d (%s)\n",
		m.ListenPort, m.Upstream.Scheme, m.Upstream.Host, m.Upstream.Port, healthOrDash(m.Health))
	return nil
}

func healthOrDash(h string) string {
	if h == "" {
		return "-"
	}
	return h
}
