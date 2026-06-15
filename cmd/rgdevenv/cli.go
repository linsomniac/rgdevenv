package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

// cliFlags holds the persistent CLI client flags (shared by all REST subcommands).
type cliFlags struct {
	configPath string
	api        string
	token      string
	insecure   bool
	json       bool
}

var cli cliFlags

// addClientFlags registers the persistent flags on the root command.
func addClientFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringVar(&cli.configPath, "cli-config", client.DefaultConfigPath(), "path to CLI config (cli.toml)")
	pf.StringVar(&cli.api, "api", "", "management API base URL (overrides config/env)")
	pf.StringVar(&cli.token, "token", "", "bearer token (overrides config/env)")
	pf.BoolVar(&cli.insecure, "insecure", false, "skip TLS verification (dev only)")
	pf.BoolVar(&cli.json, "json", false, "output JSON instead of a table")
}

// newClient builds a client from cli.toml + env, overlaid with explicit flags.
func newClient(cmd *cobra.Command) (*client.Client, error) {
	cfg, err := client.Load(cli.configPath)
	if err != nil {
		return nil, err
	}
	// AIDEV-NOTE: --json controls output format only; it is not a client transport
	// field and is intentionally excluded from the cfg overlay below.
	if cmd.Flags().Changed("api") {
		cfg.API = cli.api
	}
	if cmd.Flags().Changed("token") {
		cfg.Token = cli.token
	}
	if cmd.Flags().Changed("insecure") {
		cfg.Insecure = cli.insecure
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return client.New(cfg)
}

// renderJSON writes v as indented JSON.
func renderJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderTable writes a simple aligned table.
// AIDEV-NOTE: tabwriter aligns columns; header row is written first, then data rows.
func renderTable(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	writeRow(tw, header)
	for _, r := range rows {
		writeRow(tw, r)
	}
	_ = tw.Flush()
}

func writeRow(w io.Writer, cols []string) {
	for i, c := range cols {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, c)
	}
	fmt.Fprintln(w)
}

// newRoot builds the root command with all subcommands and client flags. main()
// and tests both use it so the wiring is identical.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "rgdevenv",
		Short:         "HTTPS reverse proxy for managing dev environments",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addClientFlags(root)
	root.AddCommand(newServeCmd())
	root.AddCommand(newLBCmd(), newMapCmd(), newPortCmd(), newCACmd(), newStatusCmd())
	return root
}
