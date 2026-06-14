package txn

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newTestManager(t *testing.T) (*Manager, *store.Store, *int) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	applyCount := 0
	cfg := Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"}
	m := New(st, func(*store.State) { applyCount++ }, func(string) bool { return true }, upstream.NewPolicy(nil), cfg)
	idSeq := 0
	m.newID = func() string { idSeq++; return fmt.Sprintf("alloc-%d", idSeq) }
	return m, st, &applyCount
}

func TestCreateLBAndDuplicate(t *testing.T) {
	m, st, applied := newTestManager(t)
	if _, err := m.CreateLB("RG-1.sean.realgo.com", "demo"); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if len(got.LoadBalancers) != 1 || got.LoadBalancers[0].Name != "rg-1.sean.realgo.com" {
		t.Fatalf("LB not created canonical: %+v", got.LoadBalancers)
	}
	if *applied == 0 {
		t.Fatal("apply callback not invoked")
	}
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestPutMappingCreateAndReplace(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	spec := MappingSpec{ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict on duplicate create, got %v", err)
	}
	spec.Upstream.Port = 9012
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, true); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if got.LoadBalancers[0].Mappings[0].Upstream.Port != 9012 {
		t.Fatalf("replace did not take: %+v", got.LoadBalancers[0].Mappings[0])
	}
}

func TestPutMappingAllocateConvenience(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	spec := MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true, AllocLabel: "web"}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	mp := got.LoadBalancers[0].Mappings[0]
	if !mp.AutoAllocated || mp.AllocationID == "" || mp.Upstream.Host != "localhost" || mp.Upstream.Port != 9000 {
		t.Fatalf("allocate convenience wrong: %+v", mp)
	}
	if len(got.PortAllocations) != 1 || got.PortAllocations[0].Port != 9000 || !got.PortAllocations[0].Auto {
		t.Fatalf("allocation not recorded: %+v", got.PortAllocations)
	}
}

func TestDeleteLBCascadesAutoPort(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteLB("rg-1.sean.realgo.com"); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if len(got.LoadBalancers) != 0 {
		t.Fatal("LB not deleted")
	}
	if len(got.PortAllocations) != 0 {
		t.Fatalf("auto allocation not cascaded: %+v", got.PortAllocations)
	}
}

func TestAllocateAndReturnPort(t *testing.T) {
	m, st, _ := newTestManager(t)
	_, a, err := m.AllocatePort("owner", "label")
	if err != nil {
		t.Fatal(err)
	}
	if a.Port != 9000 || a.Auto {
		t.Fatalf("manual allocation wrong: %+v", a)
	}
	if _, err := m.ReturnPort(9000); err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshot().PortAllocations) != 0 {
		t.Fatal("port not returned")
	}
	if _, err := m.ReturnPort(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReturnPortInUse(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReturnPort(9000); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict (port in use), got %v", err)
	}
}

func TestValidationFailureLeavesStateUnchanged(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	before := st.Snapshot()
	bad := MappingSpec{ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "evil.example.com", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}}}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", bad, false); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	after := st.Snapshot()
	if len(after.LoadBalancers[0].Mappings) != 0 {
		t.Fatal("failed mutation must not persist")
	}
	if before != after {
		t.Fatal("published snapshot pointer changed on a failed mutation")
	}
}

func TestPutMappingReplaceAllocateCascade(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, false); err != nil {
		t.Fatal(err)
	}
	firstID := st.Snapshot().LoadBalancers[0].Mappings[0].AllocationID
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, true); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if len(got.PortAllocations) != 1 {
		t.Fatalf("expected exactly 1 allocation after replace (old freed), got %+v", got.PortAllocations)
	}
	newID := got.LoadBalancers[0].Mappings[0].AllocationID
	if newID == firstID {
		t.Fatal("replace should have allocated a new allocation id")
	}
	if got.PortAllocations[0].ID != newID {
		t.Fatalf("remaining allocation should be the new mapping's: %+v", got.PortAllocations[0])
	}
}
