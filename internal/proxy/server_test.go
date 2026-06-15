package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newTestServer(t *testing.T, certFile, keyFile, mgmtHost string) *Server {
	t.Helper()
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: 443, HTTPPort: 80, MgmtHost: mgmtHost, DialTimeout: time.Second}
	return NewServer(cfg, upstream.NewPolicy(nil), resolver, DefaultLimits(), discardLogger())
}

func TestDispatchUnknownHost404(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	req := httptest.NewRequest("GET", "https://nope.sean.realgo.com/", nil)
	req.Host = "nope.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", w.Code)
	}
}

func TestDispatchManagementHost404InPhase1(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	req := httptest.NewRequest("GET", "https://rgdevenv.sean.realgo.com/", nil)
	req.Host = "rgdevenv.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("management host should 404 in phase 1, got %d", w.Code)
	}
}

func TestDispatchRoutesToUpstream(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "from-upstream")
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: host, Port: port, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	tbl, degraded := BuildRoutingTable(st, RouteDeps{Dialer: dialer, Resolver: s.resolver, Limits: DefaultLimits(), Logger: discardLogger()})
	if len(degraded) != 0 {
		t.Fatalf("degraded: %+v", degraded)
	}
	s.routes.Store(tbl)

	req := httptest.NewRequest("GET", "https://rg-1.sean.realgo.com/", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "from-upstream" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestApplyDegradesOnListenerBindFailure(t *testing.T) {
	// Occupy a port so the mapping's listener cannot bind.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	httpsPort := freePort(t)
	cfg := ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: httpsPort, HTTPPort: 0, MgmtHost: "rgdevenv.sean.realgo.com", DialTimeout: time.Second}
	s := NewServer(cfg, upstream.NewPolicy(nil), resolver, DefaultLimits(), discardLogger())
	defer s.Shutdown(context.Background())

	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-busy.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: busyPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	degraded := s.Apply(st)

	found := false
	for _, d := range degraded {
		if d.ListenPort == busyPort {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bind-failure degraded for port %d, got %+v", busyPort, degraded)
	}
	if _, ok := s.routes.Load().Lookup(busyPort, "rg-busy.sean.realgo.com"); ok {
		t.Fatal("bind-failed mapping must not be routable")
	}
}

func TestDispatchEnforcesBodyLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxRequestBody = 16 // tiny cap
	s := NewServer(ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: 443, HTTPPort: 80, DialTimeout: time.Second},
		upstream.NewPolicy(nil), resolver, limits, discardLogger())
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	tbl, _ := BuildRoutingTable(&store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: host, Port: port, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}, RouteDeps{Dialer: dialer, Resolver: resolver, Limits: limits, Logger: discardLogger()})
	s.routes.Store(tbl)

	req := httptest.NewRequest("POST", "https://rg-1.sean.realgo.com/", strings.NewReader(strings.Repeat("x", 1000)))
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)

	// The oversized body must NOT be successfully proxied (limit enforced).
	if w.Code == http.StatusOK {
		t.Fatalf("oversized body should be rejected, got 200")
	}
}

func TestDispatchServesManagementHandler(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	s.SetManagementHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "mgmt")
	}))

	req := httptest.NewRequest("GET", "https://rgdevenv.sean.realgo.com/api/v1/status", nil)
	req.Host = "rgdevenv.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "mgmt" {
		t.Fatalf("management handler not served: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestServerDialerSetAfterApply(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: freePort(t), HTTPPort: 0, DialTimeout: time.Second},
		upstream.NewPolicy(nil), resolver, DefaultLimits(), discardLogger())
	defer s.Shutdown(context.Background())

	if s.Dialer() != nil {
		t.Fatal("dialer must be nil before Apply")
	}
	s.Apply(&store.State{})
	if s.Dialer() == nil {
		t.Fatal("dialer must be set after Apply")
	}
}
