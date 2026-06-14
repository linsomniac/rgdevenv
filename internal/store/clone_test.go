package store

import (
	"testing"
	"time"
)

func TestStateCloneIsDeep(t *testing.T) {
	orig := &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name:      "a.example.com",
			Label:     "demo",
			CreatedAt: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
			Mappings: []Mapping{{
				ListenPort: 443, ListenTLS: true, AllocationID: "alloc-1",
				Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}},
			}},
		}},
		PortAllocations: []PortAllocation{{ID: "alloc-1", Port: 9011, Owner: "a.example.com", Auto: true}},
	}
	clone := orig.Clone()

	clone.LoadBalancers[0].Name = "changed"
	clone.LoadBalancers[0].Mappings[0].ListenPort = 8443
	clone.LoadBalancers[0].Mappings[0].Upstream.Host = "evil"
	clone.PortAllocations[0].Port = 1

	if orig.LoadBalancers[0].Name != "a.example.com" {
		t.Fatal("clone shares LoadBalancer header")
	}
	if orig.LoadBalancers[0].Mappings[0].ListenPort != 443 {
		t.Fatal("clone shares Mappings slice")
	}
	if orig.LoadBalancers[0].Mappings[0].Upstream.Host != "localhost" {
		t.Fatal("clone shares Upstream")
	}
	if orig.PortAllocations[0].Port != 9011 {
		t.Fatal("clone shares PortAllocations slice")
	}
}

func TestStateCloneNil(t *testing.T) {
	var s *State
	if s.Clone() != nil {
		t.Fatal("nil clone should be nil")
	}
}
