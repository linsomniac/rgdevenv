package proxy

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func backendHostPort(t *testing.T, url string) (string, int) {
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

func TestReverseProxyOverwritesForwardedHeaders(t *testing.T) {
	var got http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, 5*time.Second)
	up := store.Upstream{Scheme: "http", Host: host, Port: port, TLS: store.UpstreamTLS{Mode: "verify"}}
	rp, err := BuildReverseProxy(up, true /*listenTLS*/, dialer, "", DefaultLimits(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	front := httptest.NewServer(rp)
	defer front.Close()
	req, _ := http.NewRequest("GET", front.URL, nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6") // spoof attempt
	req.Header.Set("X-Real-IP", "6.6.6.6")
	req.Header.Set("Forwarded", "for=6.6.6.6") // spoof attempt
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if xff := got.Get("X-Forwarded-For"); !strings.HasPrefix(xff, "127.0.0.1") || strings.Contains(xff, "6.6.6.6") {
		t.Fatalf("X-Forwarded-For not properly overwritten by rgdevenv: %q", xff)
	}
	if got.Get("Forwarded") != "" {
		t.Fatalf("client-supplied Forwarded header not stripped: %q", got.Get("Forwarded"))
	}
	if got.Get("X-Real-IP") == "6.6.6.6" {
		t.Fatal("X-Real-IP spoof not stripped")
	}
	if got.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("X-Forwarded-Proto = %q, want https", got.Get("X-Forwarded-Proto"))
	}
}

func TestReverseProxy502OnPolicyDenial(t *testing.T) {
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	up := store.Upstream{Scheme: "http", Host: "blocked.example.com", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}}
	rp, err := BuildReverseProxy(up, true, dialer, "", DefaultLimits(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(rp)
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "blocked.example.com") {
		t.Fatal("502 body leaked upstream host")
	}
}
