package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestListenersOpenAndClose(t *testing.T) {
	port := freePort(t)
	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{})
	mk := func(p int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	}
	if failed := m.Reconcile(map[int]bool{port: false}, mk); len(failed) != 0 {
		t.Fatalf("unexpected bind failures: %v", failed)
	}
	defer m.Shutdown(context.Background())

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	// Reconcile to empty -> the (non-always-on) port closes.
	m.Reconcile(map[int]bool{}, mk)
	if len(m.ActivePorts()) != 0 {
		t.Fatalf("expected no active ports, got %v", m.ActivePorts())
	}
}

func TestListenersBindFailureReported(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{})
	failed := m.Reconcile(map[int]bool{port: false}, func(p int) http.Handler { return http.NotFoundHandler() })
	if _, ok := failed[port]; !ok {
		t.Fatalf("expected bind failure for occupied port %d", port)
	}
}

func TestListenersAlwaysOnNotClosed(t *testing.T) {
	port := freePort(t)
	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{port: false})
	mk := func(p int) http.Handler { return http.NotFoundHandler() }
	m.Reconcile(map[int]bool{port: false}, mk)
	defer m.Shutdown(context.Background())
	// Reconcile to empty must NOT close an always-on port.
	m.Reconcile(map[int]bool{}, mk)
	if len(m.ActivePorts()) != 1 {
		t.Fatalf("always-on port must stay open, active = %v", m.ActivePorts())
	}
}
