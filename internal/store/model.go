// Package store holds rgdevenv's dynamic state: the model, atomic+durable JSON
// persistence, a single-instance lock, and the atomically published snapshot.
package store

import "time"

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
