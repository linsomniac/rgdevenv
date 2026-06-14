package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// ServerConfig is the static configuration the Server needs.
type ServerConfig struct {
	BindAddr    string
	HTTPSPort   int
	HTTPPort    int
	CADir       string
	MgmtHost    string // canonical management hostname (reserved; 404 in Phase 1)
	DialTimeout time.Duration
}

// Server owns the routing table, listeners, and per-port dispatch.
type Server struct {
	cfg      ServerConfig
	policy   *upstream.Policy
	resolver *CertResolver
	limits   Limits
	logger   *slog.Logger
	localIPs []net.IP

	routes    atomic.Pointer[RoutingTable]
	listeners *Listeners
	mgmt      atomic.Pointer[http.Handler] // optional management-plane handler (Phase 2)
}

// NewServer constructs a Server; the HTTPS/on-demand TLS listeners use
// resolver.TLSConfig().
func NewServer(cfg ServerConfig, policy *upstream.Policy, resolver *CertResolver, limits Limits, logger *slog.Logger) *Server {
	alwaysOn := map[int]bool{cfg.HTTPSPort: true}
	if cfg.HTTPPort > 0 {
		alwaysOn[cfg.HTTPPort] = true
	}
	s := &Server{
		cfg:       cfg,
		policy:    policy,
		resolver:  resolver,
		limits:    limits,
		logger:    logger,
		localIPs:  localInterfaceIPs(),
		listeners: NewListeners(cfg.BindAddr, resolver.TLSConfig(), limits, logger, alwaysOn),
	}
	s.routes.Store(&RoutingTable{routes: map[int]map[string]*route{}, hosts: map[string]bool{}})
	return s
}

// SetManagementHandler installs the handler served at the management hostname
// (and on the optional management bind). Set it before serving; it is read
// race-free per request. A nil/unset handler makes the management host 404.
func (s *Server) SetManagementHandler(h http.Handler) {
	s.mgmt.Store(&h)
}

// Apply rebuilds routing + listeners from st and publishes atomically. Returns
// degraded mappings. Used at startup and (Phase 2) after each committed
// transaction.
//
// AIDEV-NOTE: the dialer self-guard uses ALL declared listen ports (a superset),
// independent of degraded status, so loop protection is stable across rebuilds.
func (s *Server) Apply(st *store.State) []Degraded {
	selfPorts := allListenPorts(st, s.cfg.HTTPSPort, s.cfg.HTTPPort)
	dialer := upstream.New(s.policy, upstream.SelfGuard{LocalIPs: s.localIPs, ListenPorts: selfPorts}, s.cfg.DialTimeout)

	table, degraded := BuildRoutingTable(st, RouteDeps{
		Dialer:   dialer,
		Resolver: s.resolver,
		CADir:    s.cfg.CADir,
		Limits:   s.limits,
		Logger:   s.logger,
	})

	desired := table.DesiredListeners(s.cfg.HTTPSPort, s.cfg.HTTPPort)
	failed := s.listeners.Reconcile(desired, s.makeHandler)
	if len(failed) > 0 {
		fp := make(map[int]bool, len(failed))
		for p := range failed {
			fp[p] = true
		}
		table = table.WithoutPorts(fp)
		degraded = append(degraded, listenerFailures(st, failed)...)
	}
	// AIDEV-NOTE: store AFTER Reconcile — new routes are invisible until their
	// listeners are bound. The only window is a brief 404 on already-open
	// always-on ports during a reload, which is acceptable.
	s.routes.Store(table)
	return degraded
}

func (s *Server) makeHandler(port int) http.Handler {
	if port == s.cfg.HTTPPort {
		// s.routes.Load is a method value, re-evaluated on every request (always sees the live table).
		return newRedirectHandler(s.routes.Load, s.resolver, s.cfg.HTTPSPort)
	}
	return s.dispatch(port)
}

func (s *Server) dispatch(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := canon.Host(r.Host)
		if err != nil {
			writeNotFound(w)
			return
		}
		// Management hostname → the injected management handler (auth + REST API
		// + UI). If none is installed, 404 like any unknown host (§6).
		if s.cfg.MgmtHost != "" && host == s.cfg.MgmtHost {
			if hp := s.mgmt.Load(); hp != nil {
				(*hp).ServeHTTP(w, r)
				return
			}
			writeNotFound(w)
			return
		}
		rte, ok := s.routes.Load().Lookup(port, host)
		if !ok {
			writeNotFound(w)
			return
		}
		if s.limits.MaxRequestBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.limits.MaxRequestBody)
		}
		rte.handler.ServeHTTP(w, r)
	})
}

// Shutdown gracefully stops all listeners.
func (s *Server) Shutdown(ctx context.Context) error { return s.listeners.Shutdown(ctx) }

// Resolver exposes the cert resolver (for SIGHUP reload wiring).
func (s *Server) Resolver() *CertResolver { return s.resolver }

// ActivePorts returns the currently bound listener ports (for /status).
func (s *Server) ActivePorts() []int { return s.listeners.ActivePorts() }

func allListenPorts(st *store.State, httpsPort, httpPort int) map[int]bool {
	ports := map[int]bool{httpsPort: true}
	if httpPort > 0 {
		ports[httpPort] = true
	}
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			ports[m.ListenPort] = true
		}
	}
	return ports
}

func listenerFailures(st *store.State, failed map[int]error) []Degraded {
	var out []Degraded
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if err, ok := failed[m.ListenPort]; ok {
				out = append(out, Degraded{LB: lb.Name, ListenPort: m.ListenPort, Reason: "listener bind failed: " + err.Error()})
			}
		}
	}
	return out
}

func localInterfaceIPs() []net.IP {
	var ips []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips
}
