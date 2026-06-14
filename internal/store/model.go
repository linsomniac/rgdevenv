// Package store holds rgdevenv's dynamic state: the model, atomic+durable JSON
// persistence, a single-instance lock, and the atomically published snapshot.
package store

import (
	"fmt"
	"time"
)

// CurrentVersion is the on-disk schema version. A file with a newer version is
// refused (§10); older versions would be migrated forward.
const CurrentVersion = 1

type State struct {
	Version         int              `json:"version"`
	LoadBalancers   []LoadBalancer   `json:"load_balancers"`
	PortAllocations []PortAllocation `json:"port_allocations"`
}

type LoadBalancer struct {
	Name      string    `json:"name"` // canonical FQDN; unique key
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Mappings  []Mapping `json:"mappings"`
}

type Mapping struct {
	ListenPort    int      `json:"listen_port"`
	ListenTLS     bool     `json:"listen_tls"` // NOT omitempty: false must persist
	Upstream      Upstream `json:"upstream"`
	AllocationID  string   `json:"allocation_id,omitempty"`
	AutoAllocated bool     `json:"auto_allocated,omitempty"`
}

type Upstream struct {
	Scheme string      `json:"scheme"` // http | https
	Host   string      `json:"host"`
	Port   int         `json:"port"`
	TLS    UpstreamTLS `json:"tls"`
}

type UpstreamTLS struct {
	Mode   string `json:"mode"` // verify | ca | skip
	CAName string `json:"ca_name,omitempty"`
}

type PortAllocation struct {
	ID          string    `json:"id"`
	Port        int       `json:"port"`
	Owner       string    `json:"owner,omitempty"`
	Label       string    `json:"label,omitempty"`
	Auto        bool      `json:"auto,omitempty"`
	AllocatedAt time.Time `json:"allocated_at"`
}

// Validate checks structural invariants required before a State is served or
// published (§10).
//
// AIDEV-NOTE: "one TLS mode per listen_port" is GLOBAL across all LBs (§6), not
// per-LB. Config-dependent checks (pool disjointness, upstream policy, cert
// coverage) are enforced by config.normalize, the proxy router, and the Phase-2
// transaction — not here.
func Validate(st *State) error {
	seenLB := make(map[string]bool)
	portTLS := make(map[int]bool)
	portSeen := make(map[int]bool)

	for _, lb := range st.LoadBalancers {
		if lb.Name == "" {
			return fmt.Errorf("store: load balancer with empty name")
		}
		if seenLB[lb.Name] {
			return fmt.Errorf("store: duplicate load balancer %q", lb.Name)
		}
		seenLB[lb.Name] = true

		seenPort := make(map[int]bool)
		for _, m := range lb.Mappings {
			if seenPort[m.ListenPort] {
				return fmt.Errorf("store: lb %q has duplicate listen_port %d", lb.Name, m.ListenPort)
			}
			seenPort[m.ListenPort] = true

			if portSeen[m.ListenPort] && portTLS[m.ListenPort] != m.ListenTLS {
				return fmt.Errorf("store: listen_port %d has conflicting TLS modes", m.ListenPort)
			}
			portSeen[m.ListenPort] = true
			portTLS[m.ListenPort] = m.ListenTLS
		}
	}

	allocByID := make(map[string]bool)
	allocByPort := make(map[int]bool)
	for _, a := range st.PortAllocations {
		if a.ID == "" {
			return fmt.Errorf("store: allocation with empty id")
		}
		if allocByID[a.ID] {
			return fmt.Errorf("store: duplicate allocation id %q", a.ID)
		}
		allocByID[a.ID] = true
		if allocByPort[a.Port] {
			return fmt.Errorf("store: duplicate allocation for port %d", a.Port)
		}
		allocByPort[a.Port] = true
	}
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID != "" && !allocByID[m.AllocationID] {
				return fmt.Errorf("store: lb %q mapping :%d references missing allocation %q",
					lb.Name, m.ListenPort, m.AllocationID)
			}
		}
	}
	return nil
}
