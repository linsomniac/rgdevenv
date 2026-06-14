package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validState() *State {
	return &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name: "a.example.com",
			Mappings: []Mapping{
				{ListenPort: 443, ListenTLS: true, AllocationID: "alloc-1",
					Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}}},
			},
		}},
		PortAllocations: []PortAllocation{{ID: "alloc-1", Port: 9011}},
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(validState()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
}

func TestValidateDuplicateLB(t *testing.T) {
	st := validState()
	st.LoadBalancers = append(st.LoadBalancers, LoadBalancer{Name: "a.example.com"})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate LB error")
	}
}

func TestValidateTLSModeConflictAcrossLBs(t *testing.T) {
	st := validState()
	// Another LB reuses port 443 but as plaintext -> conflict.
	st.LoadBalancers = append(st.LoadBalancers, LoadBalancer{
		Name: "b.example.com",
		Mappings: []Mapping{{ListenPort: 443, ListenTLS: false,
			Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: UpstreamTLS{Mode: "verify"}}}},
	})
	if err := Validate(st); err == nil {
		t.Fatal("expected TLS-mode conflict on port 443")
	}
}

func TestValidateDanglingAllocation(t *testing.T) {
	st := validState()
	st.LoadBalancers[0].Mappings[0].AllocationID = "ghost"
	if err := Validate(st); err == nil {
		t.Fatal("expected dangling allocation error")
	}
}

func TestValidateDuplicateAllocationPort(t *testing.T) {
	st := validState()
	st.PortAllocations = append(st.PortAllocations, PortAllocation{ID: "alloc-2", Port: 9011})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate allocation port error")
	}
}

func TestValidateEmptyLBName(t *testing.T) {
	st := validState()
	st.LoadBalancers = append(st.LoadBalancers, LoadBalancer{Name: ""})
	if err := Validate(st); err == nil {
		t.Fatal("expected empty LB name error")
	}
}

func TestValidateDuplicateAllocationID(t *testing.T) {
	st := validState()
	st.PortAllocations = append(st.PortAllocations, PortAllocation{ID: "alloc-1", Port: 9999})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate allocation id error")
	}
}

func TestValidateDuplicateListenPortWithinLB(t *testing.T) {
	st := validState()
	st.LoadBalancers[0].Mappings = append(st.LoadBalancers[0].Mappings, Mapping{
		ListenPort: 443, ListenTLS: true,
		Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9099, TLS: UpstreamTLS{Mode: "verify"}},
	})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate listen_port within LB error")
	}
}

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
