package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestCheckOnceDrivesStatus(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	h, p := hostPort(t, ok.URL)
	u := store.Upstream{Scheme: "http", Host: h, Port: p}

	tr := New(Config{Enabled: true, Path: "/", Timeout: 2 * time.Second, Threshold: 2}, "", nil)
	tr.SetDialer(testDialer())
	tr.SetTargets([]Identity{IdentityOf(u)})

	tr.checkOnce(context.Background())
	if tr.Status(u) != Unknown {
		t.Fatalf("one round, threshold 2 → unknown, got %s", tr.Status(u))
	}
	tr.checkOnce(context.Background())
	if tr.Status(u) != Up {
		t.Fatalf("two rounds → up, got %s", tr.Status(u))
	}
}

func TestRunReturnsWhenDisabled(t *testing.T) {
	New(Config{Enabled: false}, "", nil).Run(context.Background()) // must return immediately
}

func TestRunStopsOnContextCancel(t *testing.T) {
	tr := New(Config{Enabled: true, Interval: time.Hour, Path: "", Timeout: time.Second}, "", nil)
	tr.SetDialer(testDialer())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on context cancel")
	}
}

func TestRecordFailureFeedsHysteresis(t *testing.T) {
	tr := New(Config{Threshold: 1}, "", nil)
	u := up("localhost", 9000)
	tr.RecordFailure(u)
	if tr.Status(u) != Down {
		t.Fatalf("RecordFailure → down, got %s", tr.Status(u))
	}
}
