package registry

import (
	"errors"

	"github.com/realgo/rgdevenv/internal/store"
)

// ErrPoolExhausted is returned when no free port remains in the pool.
var ErrPoolExhausted = errors.New("registry: port pool exhausted")

// ErrNotAllocated is returned when returning a port that is not allocated.
var ErrNotAllocated = errors.New("registry: port not allocated")

// Allocate reserves the lowest free port in [start,end] and returns a new
// PortAllocation with the given id.
//
// AIDEV-NOTE: the caller (txn) sets Owner/Label/Auto and stamps AllocatedAt — the
// clock is injected at the txn layer so allocation is a pure, testable function.
func Allocate(allocs []store.PortAllocation, start, end int, id string) (store.PortAllocation, error) {
	used := make(map[int]bool, len(allocs))
	for _, a := range allocs {
		used[a.Port] = true
	}
	for p := start; p <= end; p++ {
		if !used[p] {
			return store.PortAllocation{ID: id, Port: p}, nil
		}
	}
	return store.PortAllocation{}, ErrPoolExhausted
}

// Return removes the allocation for the given port. Callers must first confirm
// the port is not referenced by a mapping (IsAllocationReferenced). Returns the
// trimmed slice, or ErrNotAllocated if the port is not present.
func Return(allocs []store.PortAllocation, port int) ([]store.PortAllocation, error) {
	out := make([]store.PortAllocation, 0, len(allocs))
	found := false
	for _, a := range allocs {
		if a.Port == port {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return nil, ErrNotAllocated
	}
	return out, nil
}

// IsAllocationReferenced reports whether any mapping references allocation id.
func IsAllocationReferenced(st *store.State, id string) bool {
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID == id {
				return true
			}
		}
	}
	return false
}

// AllocationByPort returns a pointer to the allocation with the given port, or nil.
func AllocationByPort(allocs []store.PortAllocation, port int) *store.PortAllocation {
	for i := range allocs {
		if allocs[i].Port == port {
			return &allocs[i]
		}
	}
	return nil
}
