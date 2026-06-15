package client

import "time"

// AIDEV-NOTE: these DTOs mirror the Phase 2b API wire shape (internal/api/views.go,
// ports.go, misc.go; internal/store/model.go; internal/health/health.go). The json
// tags ARE the contract — keep them in sync if the API's response shapes change.
type LoadBalancer struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Mappings  []Mapping `json:"mappings"`
}

type Mapping struct {
	ListenPort    int      `json:"listen_port"`
	ListenTLS     bool     `json:"listen_tls"`
	Upstream      Upstream `json:"upstream"`
	AllocationID  string   `json:"allocation_id,omitempty"`
	AutoAllocated bool     `json:"auto_allocated,omitempty"`
	Health        string   `json:"health,omitempty"`
}

type Upstream struct {
	Scheme string      `json:"scheme"`
	Host   string      `json:"host"`
	Port   int         `json:"port"`
	TLS    UpstreamTLS `json:"tls"`
}

type UpstreamTLS struct {
	Mode   string `json:"mode"`
	CAName string `json:"ca_name,omitempty"`
}

type PortPool struct {
	Start       int          `json:"start"`
	End         int          `json:"end"`
	Used        int          `json:"used"`
	Free        int          `json:"free"`
	Allocations []Allocation `json:"allocations"`
}

type Allocation struct {
	ID          string    `json:"id"`
	Port        int       `json:"port"`
	Owner       string    `json:"owner,omitempty"`
	Label       string    `json:"label,omitempty"`
	Auto        bool      `json:"auto,omitempty"`
	AllocatedAt time.Time `json:"allocated_at"`
}

type AllocateResult struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

type Status struct {
	Version         string           `json:"version"`
	HTTPSPort       int              `json:"https_port"`
	HTTPPort        int              `json:"http_port"`
	ActiveListeners []int            `json:"active_listeners"`
	LoadBalancers   int              `json:"load_balancers"`
	Mappings        int              `json:"mappings"`
	Allocations     int              `json:"allocations"`
	Upstreams       []UpstreamHealth `json:"upstreams"`
}

type UpstreamHealth struct {
	Scheme  string `json:"scheme"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSMode string `json:"tls_mode,omitempty"`
	Health  string `json:"health"`
}
