package proxy

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type listenerEntry struct {
	port int
	tls  bool
	srv  *http.Server
}

// Listeners manages the set of per-port HTTP servers. Always-on ports (https,
// http) are protected from closure by Reconcile.
type Listeners struct {
	bindAddr  string
	tlsConfig *tls.Config
	limits    Limits
	logger    *slog.Logger
	alwaysOn  map[int]bool

	mu     sync.Mutex
	active map[int]*listenerEntry
}

// NewListeners builds the manager. alwaysOn ports are never closed by Reconcile.
func NewListeners(bindAddr string, tlsConfig *tls.Config, limits Limits, logger *slog.Logger, alwaysOn map[int]bool) *Listeners {
	return &Listeners{
		bindAddr:  bindAddr,
		tlsConfig: tlsConfig,
		limits:    limits,
		logger:    logger,
		alwaysOn:  alwaysOn,
		active:    make(map[int]*listenerEntry),
	}
}

// Reconcile opens listeners for ports in desired that are not active and closes
// active non-always-on ports not in desired. desired maps port -> isTLS. Ports
// that fail to bind are returned so the caller can degrade their mappings (§10).
//
// AIDEV-NOTE: bind failures are non-fatal (§10) — the daemon starts anyway.
func (m *Listeners) Reconcile(desired map[int]bool, makeHandler func(port int) http.Handler) map[int]error {
	m.mu.Lock()

	failed := make(map[int]error)
	for port, isTLS := range desired {
		if _, ok := m.active[port]; ok {
			continue
		}
		entry, err := m.open(port, isTLS, makeHandler(port))
		if err != nil {
			m.logger.Error("listener bind failed", "port", port, "error", err)
			failed[port] = err
			continue
		}
		m.active[port] = entry
	}
	var stale []*listenerEntry
	for port, entry := range m.active {
		if _, ok := m.alwaysOn[port]; ok {
			continue
		}
		if _, ok := desired[port]; ok {
			continue
		}
		stale = append(stale, entry)
		delete(m.active, port)
	}
	m.mu.Unlock()

	// AIDEV-NOTE: drain removed listeners OUTSIDE the lock so a slow drain can't
	// stall concurrent Reconcile/ActivePorts/Shutdown callers.
	var wg sync.WaitGroup
	for _, e := range stale {
		wg.Add(1)
		go func(e *listenerEntry) {
			defer wg.Done()
			m.shutdown(e)
		}(e)
	}
	wg.Wait()
	return failed
}

func (m *Listeners) open(port int, isTLS bool, h http.Handler) (*listenerEntry, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(m.bindAddr, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	if isTLS {
		ln = tls.NewListener(ln, m.tlsConfig)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: m.limits.ReadHeaderTimeout,
		IdleTimeout:       m.limits.IdleTimeout,
		MaxHeaderBytes:    m.limits.MaxHeaderBytes,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			m.logger.Error("listener stopped", "port", port, "error", err)
		}
	}()
	return &listenerEntry{port: port, tls: isTLS, srv: srv}, nil
}

func (m *Listeners) shutdown(e *listenerEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.srv.Shutdown(ctx); err != nil {
		m.logger.Warn("listener shutdown error", "port", e.port, "error", err)
	}
}

// Shutdown gracefully stops all listeners.
func (m *Listeners) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for port, e := range m.active {
		if err := e.srv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.active, port)
	}
	return firstErr
}

// ActivePorts returns the currently bound ports.
func (m *Listeners) ActivePorts() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	ports := make([]int, 0, len(m.active))
	for p := range m.active {
		ports = append(ports, p)
	}
	return ports
}
