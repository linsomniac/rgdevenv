package health

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestIdentityOfNormalizesTLSForHTTP(t *testing.T) {
	a := IdentityOf(store.Upstream{Scheme: "http", Host: "localhost", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}})
	b := IdentityOf(store.Upstream{Scheme: "http", Host: "localhost", Port: 80, TLS: store.UpstreamTLS{Mode: "skip"}})
	if a != b {
		t.Fatalf("http identities must ignore tls fields: %+v vs %+v", a, b)
	}
	c := IdentityOf(store.Upstream{Scheme: "https", Host: "h", Port: 443, TLS: store.UpstreamTLS{Mode: "ca", CAName: "corp"}})
	if c.Mode != "ca" || c.CAName != "corp" {
		t.Fatalf("https identity must keep tls fields: %+v", c)
	}
}

func TestIdentitiesFromDedupAndSort(t *testing.T) {
	st := &store.State{LoadBalancers: []store.LoadBalancer{
		{Name: "b", Mappings: []store.Mapping{{Upstream: store.Upstream{Scheme: "http", Host: "h2", Port: 9001}}}},
		{Name: "a", Mappings: []store.Mapping{
			{Upstream: store.Upstream{Scheme: "http", Host: "h1", Port: 9000}},
			{Upstream: store.Upstream{Scheme: "http", Host: "h1", Port: 9000}}, // duplicate identity
		}},
	}}
	ids := IdentitiesFrom(st)
	if len(ids) != 2 {
		t.Fatalf("want 2 deduped identities, got %d (%+v)", len(ids), ids)
	}
	if ids[0].Host != "h1" || ids[1].Host != "h2" {
		t.Fatalf("identities not sorted: %+v", ids)
	}
}
