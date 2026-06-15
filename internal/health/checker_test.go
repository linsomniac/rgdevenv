package health

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func up(host string, port int) store.Upstream {
	return store.Upstream{Scheme: "http", Host: host, Port: port}
}

func TestHysteresisFlipsOnlyAfterThreshold(t *testing.T) {
	tr := New(Config{Threshold: 2}, "", nil)
	u := up("localhost", 9000)
	id := IdentityOf(u)

	if tr.Status(u) != Unknown {
		t.Fatal("initial status must be unknown")
	}
	tr.record(id, true)
	if tr.Status(u) != Unknown {
		t.Fatal("one healthy sample must not flip with threshold 2")
	}
	tr.record(id, true)
	if tr.Status(u) != Up {
		t.Fatalf("two healthy → up, got %s", tr.Status(u))
	}
	tr.record(id, false) // streak resets, not enough to flip
	if tr.Status(u) != Up {
		t.Fatal("a single bad sample must not flip away from up")
	}
	tr.record(id, false)
	if tr.Status(u) != Down {
		t.Fatalf("two bad → down, got %s", tr.Status(u))
	}
}

func TestSetTargetsSeedsAndPrunes(t *testing.T) {
	tr := New(Config{Threshold: 1}, "", nil)
	a, b := up("a", 1), up("b", 2)
	tr.SetTargets([]Identity{IdentityOf(a), IdentityOf(b)})

	tr.record(IdentityOf(a), true)
	if tr.Status(a) != Up {
		t.Fatal("a should be up")
	}
	tr.SetTargets([]Identity{IdentityOf(b)}) // drop a
	if tr.Status(a) != Unknown {
		t.Fatal("pruned identity must report unknown")
	}
	list := tr.List()
	if len(list) != 1 || list[0].Host != "b" {
		t.Fatalf("List after prune = %+v", list)
	}
}

func TestNewBumpsZeroThreshold(t *testing.T) {
	tr := New(Config{Threshold: 0}, "", nil)
	u := up("h", 1)
	tr.record(IdentityOf(u), true)
	if tr.Status(u) != Up {
		t.Fatal("threshold 0 must be treated as 1 (one sample flips)")
	}
}
