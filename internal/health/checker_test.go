package health

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	tr.SetTargets([]Identity{id}) // record() only updates tracked identities

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
	tr.SetTargets([]Identity{IdentityOf(u)})
	tr.record(IdentityOf(u), true)
	if tr.Status(u) != Up {
		t.Fatal("threshold 0 must be treated as 1 (one sample flips)")
	}
}

func TestNewFloorsIntervalAndTimeout(t *testing.T) {
	tr := New(Config{}, "", nil) // all zero values
	if tr.cfg.Threshold < 1 {
		t.Fatalf("Threshold not floored: %d", tr.cfg.Threshold)
	}
	if tr.cfg.Interval <= 0 {
		t.Fatalf("Interval not floored: %v", tr.cfg.Interval)
	}
	if tr.cfg.Timeout <= 0 {
		t.Fatalf("Timeout not floored: %v", tr.cfg.Timeout)
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
	tr.SetTargets([]Identity{IdentityOf(u)}) // must be tracked
	tr.RecordFailure(u)
	if tr.Status(u) != Down {
		t.Fatalf("RecordFailure on a tracked identity → down, got %s", tr.Status(u))
	}
}

// A live failure for an identity that was never tracked (or was just pruned by a
// mapping delete) must NOT resurrect it — otherwise a deleted upstream lingers in
// /status (review finding #2).
func TestRecordFailureIgnoresUntracked(t *testing.T) {
	tr := New(Config{Threshold: 1}, "", nil)
	ghost := up("deleted", 1)
	tr.RecordFailure(ghost)
	if tr.Status(ghost) != Unknown {
		t.Fatalf("untracked RecordFailure must be ignored, got %s", tr.Status(ghost))
	}
	if got := tr.List(); len(got) != 0 {
		t.Fatalf("untracked identity must not appear in List: %+v", got)
	}
}

func TestRecordLogsStatusTransition(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr := New(Config{Threshold: 1}, "", lg)
	u := up("h", 1)
	tr.SetTargets([]Identity{IdentityOf(u)})
	tr.record(IdentityOf(u), true) // Unknown → Up
	if !strings.Contains(buf.String(), "upstream health changed") || !strings.Contains(buf.String(), "to=up") {
		t.Fatalf("expected a status-transition log, got %q", buf.String())
	}
}
