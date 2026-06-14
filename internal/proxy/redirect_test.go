package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func rg1State() *store.State {
	return &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
}

func TestRedirectKnownHost(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(rg1State(), deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 443)

	req := httptest.NewRequest("GET", "http://rg-1.sean.realgo.com/foo?x=1", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://rg-1.sean.realgo.com/foo?x=1" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestRedirectUnknownHostNoOpenRedirect(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(&store.State{}, deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 443)

	req := httptest.NewRequest("GET", "http://evil.example.com/", nil)
	req.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (no open redirect)", w.Code)
	}
	if w.Header().Get("Location") != "" {
		t.Fatal("must not emit a Location for an unknown host")
	}
}

func TestRedirectNonDefaultHTTPSPort(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(rg1State(), deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 8443)

	req := httptest.NewRequest("GET", "http://rg-1.sean.realgo.com/", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if loc := w.Header().Get("Location"); loc != "https://rg-1.sean.realgo.com:8443/" {
		t.Fatalf("Location = %q, want port 8443", loc)
	}
}
