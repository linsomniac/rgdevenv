package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/upstream"
)

func testDialer() *upstream.Dialer {
	return upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, 2*time.Second)
}

func hostPort(t *testing.T, url string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestProbeHTTPUpAndDown(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	tr := New(Config{Path: "/", Timeout: 2 * time.Second}, "", nil)
	tr.SetDialer(testDialer())

	h, p := hostPort(t, ok.URL)
	if !tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("200 response must be healthy")
	}
	h, p = hostPort(t, bad.URL)
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("500 response must be unhealthy")
	}
}

func TestProbeTCPMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h, p := hostPort(t, srv.URL)

	tr := New(Config{Path: "", Timeout: time.Second}, "", nil)
	tr.SetDialer(testDialer())
	if !tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("open port must be healthy in TCP mode")
	}
	srv.Close()
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("closed port must be unhealthy in TCP mode")
	}
}

func TestProbeNoDialerIsUnhealthy(t *testing.T) {
	tr := New(Config{Path: "/", Timeout: time.Second}, "", nil)
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: "localhost", Port: 1}) {
		t.Fatal("no dialer set → unhealthy")
	}
}
