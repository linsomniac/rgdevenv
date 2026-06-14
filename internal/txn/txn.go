package txn

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/registry"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// Config carries static inputs for validation and allocation.
type Config struct {
	PoolStart, PoolEnd  int
	HTTPSPort, HTTPPort int
	MgmtBindPort        int
	MgmtHost            string
	CADir               string
}

// MappingSpec describes a mapping to create or replace. When Allocate is true the
// upstream is set to http://localhost:<allocated-port> and Upstream is ignored.
type MappingSpec struct {
	ListenPort int
	ListenTLS  bool
	Upstream   store.Upstream
	Allocate   bool
	AllocLabel string
}

// Manager serializes every state mutation and republishes the live proxy (§16).
type Manager struct {
	store  *store.Store
	apply  func(*store.State) // republish proxy (server.Apply); may be nil in tests
	covers func(string) bool  // cert coverage (resolver.Covers); may be nil
	policy *upstream.Policy
	cfg    Config

	now   func() time.Time
	newID func() string
	mu    sync.Mutex
}

// New builds a Manager.
//
// AIDEV-NOTE: apply is invoked while the Manager lock is held; it must not
// re-enter any Manager mutation method (deadlock).
func New(st *store.Store, apply func(*store.State), covers func(string) bool, policy *upstream.Policy, cfg Config) *Manager {
	return &Manager{
		store: st, apply: apply, covers: covers, policy: policy, cfg: cfg,
		now:   func() time.Time { return time.Now().UTC() },
		newID: defaultID,
	}
}

func defaultID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "alloc-" + hex.EncodeToString(b)
}

// Snapshot returns the current published state (read-only handlers).
func (m *Manager) Snapshot() *store.State { return m.store.Snapshot() }

func (m *Manager) params() ValidateParams {
	return ValidateParams{
		Policy: m.policy, Covers: m.covers,
		PoolStart: m.cfg.PoolStart, PoolEnd: m.cfg.PoolEnd,
		HTTPSPort: m.cfg.HTTPSPort, HTTPPort: m.cfg.HTTPPort,
		MgmtBindPort: m.cfg.MgmtBindPort, MgmtHost: m.cfg.MgmtHost, CADir: m.cfg.CADir,
	}
}

// commitLocked validates, persists (commit point), publishes, and applies.
// Caller holds m.mu.
//
// AIDEV-NOTE: commit point is a successful Save (§16). Publish + apply run only
// after persistence; apply is infallible (bind failures degrade, §10).
func (m *Manager) commitLocked(cand *store.State) (*store.State, error) {
	if err := Validate(cand, m.params()); err != nil {
		return nil, err
	}
	if err := m.store.Save(cand); err != nil {
		return nil, fmt.Errorf("txn: persist: %w", err)
	}
	m.store.Publish(cand)
	if m.apply != nil {
		m.apply(cand)
	}
	return cand, nil
}

// CreateLB adds a new load balancer (canonicalized) with no mappings.
func (m *Manager) CreateLB(name, label string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	if findLB(cand, cn) != nil {
		return nil, Conflict("duplicate_lb", fmt.Sprintf("load balancer %q already exists", cn))
	}
	cand.LoadBalancers = append(cand.LoadBalancers, store.LoadBalancer{Name: cn, Label: label, CreatedAt: m.now()})
	return m.commitLocked(cand)
}

// UpdateLBLabel sets the label of an existing load balancer.
func (m *Manager) UpdateLBLabel(name, label string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	lb.Label = label
	return m.commitLocked(cand)
}

// DeleteLB removes a load balancer and cascades its auto-allocated ports (§11).
func (m *Manager) DeleteLB(name string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	idx := lbIndex(cand, cn)
	if idx < 0 {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	cand.LoadBalancers = append(cand.LoadBalancers[:idx], cand.LoadBalancers[idx+1:]...)
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// PutMapping creates (replace=false) or replaces (replace=true) a mapping. When
// spec.Allocate is set, a localhost port is allocated and wired (§11).
func (m *Manager) PutMapping(name string, spec MappingSpec, replace bool) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	idx := mappingIndex(lb, spec.ListenPort)
	if !replace && idx >= 0 {
		return nil, Conflict("mapping_exists", fmt.Sprintf("mapping :%d already exists on %s", spec.ListenPort, cn))
	}

	nm := store.Mapping{ListenPort: spec.ListenPort, ListenTLS: spec.ListenTLS, Upstream: spec.Upstream}
	if spec.Allocate {
		a, err := registry.Allocate(cand.PortAllocations, m.cfg.PoolStart, m.cfg.PoolEnd, m.newID())
		if err != nil {
			if errors.Is(err, registry.ErrPoolExhausted) {
				return nil, Conflict("pool_exhausted", "port pool exhausted")
			}
			return nil, fmt.Errorf("txn: allocate: %w", err)
		}
		a.Owner = cn
		a.Label = spec.AllocLabel
		a.Auto = true
		a.AllocatedAt = m.now()
		cand.PortAllocations = append(cand.PortAllocations, a)
		nm.Upstream = store.Upstream{Scheme: "http", Host: "localhost", Port: a.Port, TLS: store.UpstreamTLS{Mode: "verify"}}
		nm.AllocationID = a.ID
		nm.AutoAllocated = true
	}

	if idx >= 0 {
		lb.Mappings[idx] = nm
	} else {
		lb.Mappings = append(lb.Mappings, nm)
	}
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// DeleteMapping removes a mapping and cascades its auto-allocated port (§11).
func (m *Manager) DeleteMapping(name string, listenPort int) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	mi := mappingIndex(lb, listenPort)
	if mi < 0 {
		return nil, NotFound("mapping_not_found", fmt.Sprintf("mapping :%d not found on %s", listenPort, cn))
	}
	lb.Mappings = append(lb.Mappings[:mi], lb.Mappings[mi+1:]...)
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// AllocatePort reserves a manual (non-auto) port and returns the allocation.
func (m *Manager) AllocatePort(owner, label string) (*store.State, store.PortAllocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	a, err := registry.Allocate(cand.PortAllocations, m.cfg.PoolStart, m.cfg.PoolEnd, m.newID())
	if err != nil {
		if errors.Is(err, registry.ErrPoolExhausted) {
			return nil, store.PortAllocation{}, Conflict("pool_exhausted", "port pool exhausted")
		}
		return nil, store.PortAllocation{}, fmt.Errorf("txn: allocate: %w", err)
	}
	a.Owner = owner
	a.Label = label
	a.Auto = false
	a.AllocatedAt = m.now()
	cand.PortAllocations = append(cand.PortAllocations, a)
	st, err := m.commitLocked(cand)
	if err != nil {
		return nil, store.PortAllocation{}, err
	}
	return st, a, nil
}

// ReturnPort frees a port; in-use ports are rejected (§11).
func (m *Manager) ReturnPort(port int) (*store.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	a := registry.AllocationByPort(cand.PortAllocations, port)
	if a == nil {
		return nil, NotFound("port_not_allocated", fmt.Sprintf("port %d is not allocated", port))
	}
	if registry.IsAllocationReferenced(cand, a.ID) {
		return nil, Conflict("port_in_use", fmt.Sprintf("port %d is referenced by a mapping", port))
	}
	cand.PortAllocations, _ = registry.Return(cand.PortAllocations, port)
	return m.commitLocked(cand)
}

func findLB(st *store.State, name string) *store.LoadBalancer {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return &st.LoadBalancers[i]
		}
	}
	return nil
}

func lbIndex(st *store.State, name string) int {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return i
		}
	}
	return -1
}

func mappingIndex(lb *store.LoadBalancer, port int) int {
	for i := range lb.Mappings {
		if lb.Mappings[i].ListenPort == port {
			return i
		}
	}
	return -1
}
