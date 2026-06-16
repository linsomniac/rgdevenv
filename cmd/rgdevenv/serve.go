package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/api"
	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/proxy"
	"github.com/realgo/rgdevenv/internal/registry"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// AIDEV-NOTE: must be a `var`, not a `const` — release builds stamp the git tag
// in via `-ldflags -X github.com/realgo/rgdevenv/cmd/rgdevenv.version=...`
// (see .goreleaser.yaml), and `-X` cannot overwrite a const. "0.1.0" is the
// dev/default value used by plain `go build`.
var version = "0.1.0"

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

	srv, st, mgmtHandler, tracker, err := setupServer(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	applyAndTrack(srv, tracker, st.Snapshot(), logger)
	healthCtx, cancelHealth := context.WithCancel(context.Background())
	defer cancelHealth()
	go tracker.Run(healthCtx)

	var mgmtBind *http.Server
	if cfg.Management.Bind != "" {
		mgmtBind, err = startMgmtBind(cfg.Management.Bind, mgmtHandler, proxy.DefaultLimits(), logger)
		if err != nil {
			return err
		}
	}

	logger.Info("rgdevenv listening", "https_port", cfg.HTTPSPort, "http_port", cfg.HTTPPort, "bind", cfg.BindAddr)
	return runSignals(configPath, srv, st, tracker, mgmtBind, logger, levelVar)
}

// setupServer opens + validates + reconciles the store, builds the proxy server,
// and installs the management plane (auth + transaction + REST API) at the
// MgmtHost seam. It does NOT bind sockets (call srv.Apply); it returns the
// management handler so the caller can also serve it on the optional bind.
func setupServer(cfg *config.Config, logger *slog.Logger) (*proxy.Server, *store.Store, http.Handler, *health.Tracker, error) {
	st, err := store.Open(cfg.StateFile)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	snap := st.Snapshot()
	if err := store.Validate(snap); err != nil {
		st.Close()
		return nil, nil, nil, nil, fmt.Errorf("state invalid: %w", err)
	}
	if allocs, changed := registry.Reconcile(snap); changed {
		snap.PortAllocations = allocs
		if err := st.Save(snap); err != nil {
			st.Close()
			return nil, nil, nil, nil, fmt.Errorf("persist reconciled state: %w", err)
		}
		st.Publish(snap)
		logger.Info("reconciled orphaned port allocations")
	}

	resolver, err := proxy.NewCertResolver(cfg.AllCertPairs())
	if err != nil {
		st.Close()
		return nil, nil, nil, nil, err
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

	tracker := health.New(health.Config{
		Enabled:   cfg.Health.Enabled,
		Interval:  cfg.HealthInterval(),
		Timeout:   cfg.HealthTimeout(),
		Path:      cfg.Health.Path,
		Threshold: cfg.Health.Threshold,
	}, cfg.CADir, logger)
	srv.SetUpstreamErrorObserver(tracker.RecordFailure)

	token, err := auth.LoadToken(cfg.TokenFile)
	if err != nil {
		st.Close()
		return nil, nil, nil, nil, err
	}
	mgr := txn.New(st, func(state *store.State) {
		applyAndTrack(srv, tracker, state, logger)
	}, resolver.Covers, upstream.NewPolicy(cfg.Upstreams.Allow), txn.Config{
		PoolStart:    cfg.PortPool.Start,
		PoolEnd:      cfg.PortPool.End,
		HTTPSPort:    cfg.HTTPSPort,
		HTTPPort:     cfg.HTTPPort,
		MgmtBindPort: cfg.MgmtBindPort(),
		MgmtHost:     cfg.ManagementHostname,
		CADir:        cfg.CADir,
	})
	mgmtHandler := api.New(api.Deps{
		Txn:         mgr,
		Auth:        auth.NewAuthenticator(token),
		Limiter:     auth.NewRateLimiter(cfg.Management.AuthRateLimitPerMin, time.Minute),
		CADir:       cfg.CADir,
		Version:     version,
		HTTPSPort:   cfg.HTTPSPort,
		HTTPPort:    cfg.HTTPPort,
		PoolStart:   cfg.PortPool.Start,
		PoolEnd:     cfg.PortPool.End,
		ActivePorts: srv.ActivePorts,
		Logger:      logger,
		Health:      tracker,
	})
	srv.SetManagementHandler(mgmtHandler)
	return srv, st, mgmtHandler, tracker, nil
}

// applyAndTrack reconfigures the proxy from state and refreshes the health
// checker's dialer + target set so both stay consistent after every change.
func applyAndTrack(srv *proxy.Server, tracker *health.Tracker, state *store.State, logger *slog.Logger) {
	for _, d := range srv.Apply(state) {
		logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
	}
	tracker.SetDialer(srv.Dialer())
	tracker.SetTargets(health.IdentitiesFrom(state))
}

// startMgmtBind serves the management handler on a separate plaintext listener
// (loopback TCP or unix socket; validated by config) (§15).
func startMgmtBind(bind string, h http.Handler, limits proxy.Limits, logger *slog.Logger) (*http.Server, error) {
	network, addr := "tcp", bind
	if strings.HasPrefix(bind, "/") || strings.HasPrefix(bind, "@") || strings.HasPrefix(bind, "unix:") {
		network = "unix"
		addr = strings.TrimPrefix(bind, "unix:")
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, fmt.Errorf("management bind %q: %w", bind, err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: limits.ReadHeaderTimeout,
		IdleTimeout:       limits.IdleTimeout,
		MaxHeaderBytes:    limits.MaxHeaderBytes,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("management bind stopped", "error", err)
		}
	}()
	logger.Info("management plane on separate bind", "bind", bind)
	return srv, nil
}

// runSignals blocks until SIGTERM/SIGINT, handling SIGHUP reloads in between.
func runSignals(configPath string, srv *proxy.Server, st *store.Store, tracker *health.Tracker, mgmtBind *http.Server, logger *slog.Logger, levelVar *slog.LevelVar) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	for sig := range sigs {
		switch sig {
		case syscall.SIGHUP:
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
			applyAndTrack(srv, tracker, st.Snapshot(), logger)
		case syscall.SIGTERM, syscall.SIGINT:
			logger.Info("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			var wg sync.WaitGroup
			if mgmtBind != nil {
				wg.Add(1)
				go func() { defer wg.Done(); _ = mgmtBind.Shutdown(ctx) }()
			}
			err := srv.Shutdown(ctx)
			wg.Wait()
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
