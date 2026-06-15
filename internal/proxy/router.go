package proxy

import (
	"log/slog"
	"net/http"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// route is a resolved mapping ready to serve.
type route struct {
	lbName  string
	mapping store.Mapping
	handler http.Handler
}

// Degraded records a mapping that is configured but not served, with a reason
// (cert-not-covered, unbuildable upstream, listener bind failure) (§7, §10).
type Degraded struct {
	LB         string
	ListenPort int
	Reason     string
}

// RoutingTable maps (listen_port, canonical host) -> route. It is immutable once
// built and swapped in atomically (§16).
type RoutingTable struct {
	routes map[int]map[string]*route
	hosts  map[string]bool // hosts with >=1 live route (for the :80 redirect)
}

// RouteDeps are the dependencies for building a routing table.
type RouteDeps struct {
	Dialer          *upstream.Dialer
	Resolver        *CertResolver
	CADir           string
	Limits          Limits
	Logger          *slog.Logger
	OnUpstreamError func(store.Upstream) // optional: live-failure feed for health (§17)
}

// BuildRoutingTable builds routes for all servable mappings and returns the
// degraded ones. A mapping is degraded if its host is invalid, a TLS mapping's
// host is not certificate-covered (§7), or its reverse proxy cannot be built
// (bad scheme/mode/CA).
//
// AIDEV-NOTE: upstream POLICY (allowlist/deny) is enforced at dial time by the
// safe dialer, not here — build does no DNS, so degraded reasons are
// deterministic and offline.
func BuildRoutingTable(st *store.State, deps RouteDeps) (*RoutingTable, []Degraded) {
	t := &RoutingTable{routes: make(map[int]map[string]*route), hosts: make(map[string]bool)}
	var degraded []Degraded

	for _, lb := range st.LoadBalancers {
		name, err := canon.Host(lb.Name)
		if err != nil {
			for _, m := range lb.Mappings {
				degraded = append(degraded, Degraded{lb.Name, m.ListenPort, "invalid hostname: " + err.Error()})
			}
			continue
		}
		for _, m := range lb.Mappings {
			if m.ListenTLS && !deps.Resolver.Covers(name) {
				degraded = append(degraded, Degraded{name, m.ListenPort, "host not covered by certificate"})
				continue
			}
			var onErr func()
			if deps.OnUpstreamError != nil {
				up := m.Upstream
				onErr = func() { deps.OnUpstreamError(up) }
			}
			h, err := BuildReverseProxy(m.Upstream, m.ListenTLS, deps.Dialer, deps.CADir, deps.Limits, deps.Logger, onErr)
			if err != nil {
				degraded = append(degraded, Degraded{name, m.ListenPort, "upstream not servable: " + err.Error()})
				continue
			}
			if t.routes[m.ListenPort] == nil {
				t.routes[m.ListenPort] = make(map[string]*route)
			}
			t.routes[m.ListenPort][name] = &route{lbName: name, mapping: m, handler: h}
			t.hosts[name] = true
		}
	}
	return t, degraded
}

// Lookup finds the route for (port, canonical host).
func (t *RoutingTable) Lookup(port int, host string) (*route, bool) {
	byHost, ok := t.routes[port]
	if !ok {
		return nil, false
	}
	r, ok := byHost[host]
	return r, ok
}

// HasHost reports whether any live route serves this host (used by the :80 redirect).
func (t *RoutingTable) HasHost(host string) bool { return t.hosts[host] }

// DesiredListeners returns the ports the table needs and whether each is TLS,
// including always-on https (TLS) and http (plaintext redirect, when >0).
func (t *RoutingTable) DesiredListeners(httpsPort, httpPort int) map[int]bool {
	desired := make(map[int]bool)
	for port, byHost := range t.routes {
		for _, r := range byHost {
			desired[port] = r.mapping.ListenTLS
			break
		}
	}
	desired[httpsPort] = true
	if httpPort > 0 {
		desired[httpPort] = false
	}
	return desired
}

// WithoutPorts returns a copy of the table with the given ports removed (used
// when a listener fails to bind).
func (t *RoutingTable) WithoutPorts(ports map[int]bool) *RoutingTable {
	nt := &RoutingTable{routes: make(map[int]map[string]*route), hosts: make(map[string]bool)}
	for port, byHost := range t.routes {
		if ports[port] {
			continue
		}
		nt.routes[port] = byHost
		for host := range byHost {
			nt.hosts[host] = true
		}
	}
	return nt
}
