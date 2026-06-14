package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/proxy"
	"github.com/realgo/rgdevenv/internal/registry"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newServeCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the rgdevenv proxy daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/rgdevenv/config.toml", "path to config file")
	return cmd
}

func runServe(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, levelVar := newLogger(cfg.Log.Level)

	srv, st, err := setupServer(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	for _, d := range srv.Apply(st.Snapshot()) {
		logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
	}
	logger.Info("rgdevenv listening", "https_port", cfg.HTTPSPort, "http_port", cfg.HTTPPort, "bind", cfg.BindAddr)

	return runSignals(configPath, srv, st, logger, levelVar)
}

// setupServer opens + validates + reconciles the store and builds the proxy
// server. It does NOT bind sockets (call srv.Apply for that), so it is testable.
func setupServer(cfg *config.Config, logger *slog.Logger) (*proxy.Server, *store.Store, error) {
	st, err := store.Open(cfg.StateFile)
	if err != nil {
		return nil, nil, err
	}
	snap := st.Snapshot()
	if err := store.Validate(snap); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("state invalid: %w", err)
	}
	if allocs, changed := registry.Reconcile(snap); changed {
		snap.PortAllocations = allocs
		if err := st.Save(snap); err != nil {
			st.Close()
			return nil, nil, fmt.Errorf("persist reconciled state: %w", err)
		}
		st.Publish(snap)
		logger.Info("reconciled orphaned port allocations")
	}

	resolver, err := proxy.NewCertResolver(cfg.AllCertPairs())
	if err != nil {
		st.Close()
		return nil, nil, err
	}

	limits := proxy.DefaultLimits()
	srv := proxy.NewServer(proxy.ServerConfig{
		BindAddr:    cfg.BindAddr,
		HTTPSPort:   cfg.HTTPSPort,
		HTTPPort:    cfg.HTTPPort,
		CADir:       cfg.CADir,
		MgmtHost:    cfg.ManagementHostname,
		DialTimeout: limits.DialTimeout,
	}, upstream.NewPolicy(cfg.Upstreams.Allow), resolver, limits, logger)
	return srv, st, nil
}

// runSignals blocks until SIGTERM/SIGINT, handling SIGHUP reloads in between.
func runSignals(configPath string, srv *proxy.Server, st *store.Store, logger *slog.Logger, levelVar *slog.LevelVar) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	for sig := range sigs {
		switch sig {
		case syscall.SIGHUP:
			// Runtime-safe reload only: log level, certs, CA file contents.
			// Ports/bind changes require a restart (§9).
			newCfg, err := config.Load(configPath)
			if err != nil {
				logger.Error("config reload failed; keeping previous config", "error", err)
				break
			}
			levelVar.Set(parseLevel(newCfg.Log.Level))
			// AIDEV-NOTE: validate-before-swap (§7) — on failure the old certs
			// are retained and verification is never downgraded.
			if err := srv.Resolver().Reload(newCfg.AllCertPairs()); err != nil {
				logger.Error("cert reload failed; keeping previous certs", "error", err)
			} else {
				logger.Info("certificates reloaded")
			}
			for _, d := range srv.Apply(st.Snapshot()) {
				logger.Warn("mapping degraded after reload", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
			}
		case syscall.SIGTERM, syscall.SIGINT:
			logger.Info("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := srv.Shutdown(ctx)
			cancel()
			return err
		}
	}
	return nil
}

func newLogger(level string) (*slog.Logger, *slog.LevelVar) {
	lv := new(slog.LevelVar)
	lv.Set(parseLevel(level))
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})), lv
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
