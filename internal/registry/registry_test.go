package registry

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestReconcileFreesOrphanAuto(t *testing.T) {
	st := &store.State{
		PortAllocations: []store.PortAllocation{
			{ID: "a", Port: 9000, Auto: true},  // orphan auto -> freed
			{ID: "b", Port: 9001, Auto: false}, // manual orphan -> kept
		},
	}
	got, changed := Reconcile(st)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only manual alloc kept, got %+v", got)
	}
}

func TestReconcileKeepsReferencedAuto(t *testing.T) {
	st := &store.State{
		LoadBalancers: []store.LoadBalancer{{
			Name:     "x",
			Mappings: []store.Mapping{{ListenPort: 443, AllocationID: "a"}},
		}},
		PortAllocations: []store.PortAllocation{{ID: "a", Port: 9000, Auto: true}},
	}
	got, changed := Reconcile(st)
	if changed {
		t.Fatal("expected changed=false")
	}
	if len(got) != 1 {
		t.Fatalf("referenced auto alloc dropped: %+v", got)
	}
}

func TestReconcileNoChangeWhenNothingOrphaned(t *testing.T) {
	st := &store.State{
		LoadBalancers: []store.LoadBalancer{{
			Name:     "x",
			Mappings: []store.Mapping{{ListenPort: 443, AllocationID: "a"}},
		}},
		PortAllocations: []store.PortAllocation{
			{ID: "a", Port: 9000, Auto: true},  // referenced auto -> kept
			{ID: "b", Port: 9001, Auto: false}, // manual -> kept
		},
	}
	got, changed := Reconcile(st)
	if changed {
		t.Fatal("expected changed=false")
	}
	if len(got) != 2 {
		t.Fatalf("expected both allocations kept, got %+v", got)
	}
}
