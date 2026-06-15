package api

import (
	"time"

	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
)

// AIDEV-NOTE: mappingView and lbView manually mirror store.Mapping /
// store.LoadBalancer plus the computed "health" field. If you add a field to
// either store type you MUST add it here too, or it will silently disappear from
// API responses — there is no compile-time enforcement of this invariant.

// mappingView is a store.Mapping plus its resolved health (§12 "with health").
type mappingView struct {
	ListenPort    int            `json:"listen_port"`
	ListenTLS     bool           `json:"listen_tls"`
	Upstream      store.Upstream `json:"upstream"`
	AllocationID  string         `json:"allocation_id,omitempty"`
	AutoAllocated bool           `json:"auto_allocated,omitempty"`
	Health        health.Status  `json:"health"`
}

// lbView is a store.LoadBalancer whose mappings carry health.
type lbView struct {
	Name      string        `json:"name"`
	Label     string        `json:"label,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	Mappings  []mappingView `json:"mappings"`
}

func (h *Handler) toMappingView(m store.Mapping) mappingView {
	return mappingView{
		ListenPort: m.ListenPort, ListenTLS: m.ListenTLS, Upstream: m.Upstream,
		AllocationID: m.AllocationID, AutoAllocated: m.AutoAllocated,
		Health: h.health.Status(m.Upstream),
	}
}

func (h *Handler) toLBView(lb store.LoadBalancer) lbView {
	ms := make([]mappingView, 0, len(lb.Mappings))
	for _, m := range lb.Mappings {
		ms = append(ms, h.toMappingView(m))
	}
	return lbView{Name: lb.Name, Label: lb.Label, CreatedAt: lb.CreatedAt, Mappings: ms}
}

// noopHealth is the default reporter when none is injected (tests, health off).
type noopHealth struct{}

func (noopHealth) Status(store.Upstream) health.Status { return health.Unknown }
func (noopHealth) List() []health.Entry                { return nil }
