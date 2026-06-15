package main

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/api"
	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// inProcessAPI builds a real management Handler over a temp store and serves it.
func inProcessAPI(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := txn.New(st, func(*store.State) {}, func(string) bool { return true }, upstream.NewPolicy(nil),
		txn.Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"})
	h := api.New(api.Deps{
		Txn: mgr, Auth: auth.NewAuthenticator("0123456789abcdef0123456789abcdef"),
		Limiter: auth.NewRateLimiter(1000, time.Minute),
		CADir:   t.TempDir(), Version: "test", HTTPSPort: 443, HTTPPort: 80, PoolStart: 9000, PoolEnd: 9999,
		ActivePorts: func() []int { return []int{443} },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// run executes the root command with args and returns combined stdout.
func run(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	root := newTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	full := append([]string{
		"--api", srv.URL,
		"--token", "0123456789abcdef0123456789abcdef",
	}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

func TestCLIEndToEnd(t *testing.T) {
	srv := inProcessAPI(t)

	// Create an LB.
	if out, err := run(t, srv, "lb", "add", "rg-1.sean.realgo.com", "--label", "demo"); err != nil {
		t.Fatalf("lb add: %v (%s)", err, out)
	}
	// List shows it.
	out, err := run(t, srv, "lb", "ls")
	if err != nil || !strings.Contains(out, "rg-1.sean.realgo.com") || !strings.Contains(out, "demo") {
		t.Fatalf("lb ls: %v %q", err, out)
	}
	// Add an allocate mapping on :443.
	if out, err := run(t, srv, "map", "add", "rg-1.sean.realgo.com", "--allocate", "--label", "web"); err != nil {
		t.Fatalf("map add --allocate: %v (%s)", err, out)
	}
	// port ls shows one used port, JSON form.
	out, err = run(t, srv, "--json", "port", "ls")
	if err != nil || !strings.Contains(out, `"used": 1`) {
		t.Fatalf("port ls --json: %v %q", err, out)
	}
	// map ls shows the localhost upstream.
	out, err = run(t, srv, "map", "ls", "rg-1.sean.realgo.com")
	if err != nil || !strings.Contains(out, "localhost") {
		t.Fatalf("map ls: %v %q", err, out)
	}
	// status works.
	if out, err := run(t, srv, "status"); err != nil || !strings.Contains(out, "version test") {
		t.Fatalf("status: %v %q", err, out)
	}
	// A conflict surfaces as an error (duplicate LB).
	if _, err := run(t, srv, "lb", "add", "rg-1.sean.realgo.com"); err == nil {
		t.Fatal("duplicate lb add should error")
	}
	// Delete the LB.
	if _, err := run(t, srv, "lb", "rm", "rg-1.sean.realgo.com"); err != nil {
		t.Fatalf("lb rm: %v", err)
	}
	// After delete, lb ls no longer shows it.
	if out, err := run(t, srv, "lb", "ls"); err != nil || strings.Contains(out, "rg-1.sean.realgo.com") {
		t.Fatalf("lb ls after rm should not list the deleted lb: %v %q", err, out)
	}
}
