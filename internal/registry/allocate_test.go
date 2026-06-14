package registry

import (
	"errors"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestAllocateLowestFree(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9002}}
	got, err := Allocate(allocs, 9000, 9999, "id-x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9001 || got.ID != "id-x" {
		t.Fatalf("expected lowest free 9001/id-x, got %+v", got)
	}
}

func TestAllocateExhausted(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9001}}
	_, err := Allocate(allocs, 9000, 9001, "id-x")
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestReturnFreesPort(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9001}}
	got, err := Return(allocs, 9000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Port != 9001 {
		t.Fatalf("expected only 9001 to remain, got %+v", got)
	}
}

func TestReturnUnknownPort(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}}
	if _, err := Return(allocs, 9999); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("expected ErrNotAllocated, got %v", err)
	}
}

func TestIsPortReferenced(t *testing.T) {
	st := &store.State{
		LoadBalancers: []store.LoadBalancer{{
			Name:     "x",
			Mappings: []store.Mapping{{ListenPort: 443, AllocationID: "a"}},
		}},
		PortAllocations: []store.PortAllocation{{ID: "a", Port: 9000}},
	}
	if !IsAllocationReferenced(st, "a") {
		t.Fatal("alloc a is referenced by a mapping")
	}
	if IsAllocationReferenced(st, "b") {
		t.Fatal("alloc b is not referenced")
	}
}

func TestAllocationByPort(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9001}}
	if got := AllocationByPort(allocs, 9001); got == nil || got.ID != "b" {
		t.Fatalf("hit: got %+v, want id b", got)
	}
	if got := AllocationByPort(allocs, 9999); got != nil {
		t.Fatalf("miss: got %+v, want nil", got)
	}
}
