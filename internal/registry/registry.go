// Package registry implements port-pool reservation lifecycle. Phase 1 uses only
// startup Reconcile; allocate/return mutation arrives with the Phase-2 API.
package registry

import "github.com/realgo/rgdevenv/internal/store"

// Reconcile frees orphaned auto-allocated ports — auto=true allocations not
// referenced by any mapping (§10, §11). It returns the cleaned allocation slice
// and whether anything changed. Manually allocated ports are always retained.
func Reconcile(st *store.State) (allocs []store.PortAllocation, changed bool) {
	referenced := make(map[string]bool)
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID != "" {
				referenced[m.AllocationID] = true
			}
		}
	}
	kept := make([]store.PortAllocation, 0, len(st.PortAllocations))
	for _, a := range st.PortAllocations {
		if a.Auto && !referenced[a.ID] {
			changed = true
			continue
		}
		kept = append(kept, a)
	}
	return kept, changed
}
