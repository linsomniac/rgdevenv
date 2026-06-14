package store

// Clone returns a deep copy of s so a transaction can mutate the copy without
// touching the published (immutable) snapshot (§16). Returns nil for a nil state.
//
// AIDEV-NOTE: every slice must be copied; structs without reference fields copy
// by value. If you add a slice/map/pointer field to the model, copy it here too.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	out := &State{Version: s.Version}
	if s.LoadBalancers != nil {
		out.LoadBalancers = make([]LoadBalancer, len(s.LoadBalancers))
		for i, lb := range s.LoadBalancers {
			nlb := lb // copies scalar/time fields
			if lb.Mappings != nil {
				nlb.Mappings = make([]Mapping, len(lb.Mappings))
				copy(nlb.Mappings, lb.Mappings) // Mapping has no reference fields
			}
			out.LoadBalancers[i] = nlb
		}
	}
	if s.PortAllocations != nil {
		out.PortAllocations = make([]PortAllocation, len(s.PortAllocations))
		copy(out.PortAllocations, s.PortAllocations)
	}
	return out
}
