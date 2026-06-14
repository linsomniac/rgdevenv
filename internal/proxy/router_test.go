package proxy

import (
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func testDeps(t *testing.T, certFile, keyFile, caDir string) RouteDeps {
	t.Helper()
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	return RouteDeps{
		Dialer:   upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second),
		Resolver: r,
		CADir:    caDir,
		Limits:   DefaultLimits(),
		Logger:   discardLogger(),
	}
}

func TestBuildRoutingTableLiveRoute(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 0 {
		t.Fatalf("unexpected degraded: %+v", degraded)
	}
	if _, ok := tbl.Lookup(443, "rg-1.sean.realgo.com"); !ok {
		t.Fatal("expected route for rg-1 on 443")
	}
	if !tbl.HasHost("rg-1.sean.realgo.com") {
		t.Fatal("HasHost should be true")
	}
}

func TestBuildRoutingTableDegradedOnCertNotCovered(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t, "*.other.com")
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com", // not covered by *.other.com
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded, got %+v", degraded)
	}
	if _, ok := tbl.Lookup(443, "rg-1.sean.realgo.com"); ok {
		t.Fatal("degraded mapping must not be routable")
	}
}

func TestBuildRoutingTableDegradedOnMissingCA(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, t.TempDir()) // empty ca dir
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "https", Host: "build-box", Port: 8443, TLS: store.UpstreamTLS{Mode: "ca", CAName: "missing"}}}},
	}}}
	_, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded for missing CA, got %+v", degraded)
	}
}

func TestDesiredListeners(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 8443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, _ := BuildRoutingTable(st, deps)
	desired := tbl.DesiredListeners(443, 80)
	if !desired[443] {
		t.Fatal("expected https 443 TLS listener")
	}
	if v, ok := desired[80]; !ok || v {
		t.Fatal("expected http 80 plaintext redirect listener")
	}
	if v, ok := desired[8443]; !ok || !v {
		t.Fatalf("expected 8443 TLS listener: %+v", desired)
	}
}

func TestBuildRoutingTableDegradedOnInvalidHostname(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "bad host", // space -> canon.Host errors
		Mappings: []store.Mapping{
			{ListenPort: 443, ListenTLS: true, Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}},
			{ListenPort: 8443, ListenTLS: true, Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: store.UpstreamTLS{Mode: "verify"}}},
		},
	}}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 2 {
		t.Fatalf("expected both mappings degraded for invalid hostname, got %+v", degraded)
	}
	if _, ok := tbl.Lookup(443, "bad host"); ok {
		t.Fatal("no route should exist for an invalid hostname")
	}
}

func TestRoutingTableWithoutPorts(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{
		{Name: "a.sean.realgo.com", Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true, Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}}},
		{Name: "b.sean.realgo.com", Mappings: []store.Mapping{{ListenPort: 8443, ListenTLS: true, Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: store.UpstreamTLS{Mode: "verify"}}}}},
	}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 0 {
		t.Fatalf("unexpected degraded: %+v", degraded)
	}
	nt := tbl.WithoutPorts(map[int]bool{8443: true})
	if _, ok := nt.Lookup(8443, "b.sean.realgo.com"); ok {
		t.Fatal("removed port should not be routable")
	}
	if nt.HasHost("b.sean.realgo.com") {
		t.Fatal("host b (only on port 8443) should not remain in hosts")
	}
	if _, ok := nt.Lookup(443, "a.sean.realgo.com"); !ok {
		t.Fatal("surviving port must remain routable")
	}
	if _, ok := tbl.Lookup(8443, "b.sean.realgo.com"); !ok {
		t.Fatal("WithoutPorts must not mutate the original table")
	}
}
