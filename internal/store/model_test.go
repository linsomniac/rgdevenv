package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStateJSONRoundTrip(t *testing.T) {
	in := &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name:      "rg-1.sean.realgo.com",
			Label:     "demo",
			CreatedAt: time.Date(2026, 6, 13, 9, 30, 0, 0, time.UTC),
			Mappings: []Mapping{
				{
					ListenPort: 443,
					ListenTLS:  true,
					Upstream:   Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}},
				},
				{
					ListenPort: 8080,
					ListenTLS:  false, // must survive round-trip
					Upstream:   Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: UpstreamTLS{Mode: "verify"}},
				},
			},
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// AIDEV-NOTE: listen_tls must NOT be omitempty; false has to persist.
	if !strings.Contains(string(b), `"listen_tls":false`) {
		t.Fatalf("listen_tls=false not serialized: %s", b)
	}
	var out State
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.LoadBalancers[0].Mappings[1].ListenTLS {
		t.Fatal("ListenTLS false not preserved")
	}
	if out.LoadBalancers[0].Mappings[0].Upstream.Port != 9011 {
		t.Fatal("upstream port lost")
	}
}
