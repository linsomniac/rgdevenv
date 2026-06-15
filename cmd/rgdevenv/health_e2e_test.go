package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

func TestHealthEndToEnd(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer backend.Close()
	bhost, bportStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	bport, _ := strconv.Atoi(bportStr)

	certFile, keyFile := writeMainTestCert(t)
	httpsPort := freeTCPPort(t)
	const token = "0123456789abcdef0123456789abcdef"
	t.Setenv("RGDEVENV_TOKEN", token)

	cfg := &config.Config{
		BindAddr:           "127.0.0.1",
		HTTPSPort:          httpsPort,
		HTTPPort:           0,
		CertFile:           certFile,
		KeyFile:            keyFile,
		ManagementHostname: "rgdevenv.sean.realgo.com",
		CADir:              t.TempDir(),
		StateFile:          filepath.Join(t.TempDir(), "state.json"),
		PortPool:           config.PortPoolConfig{Start: 9000, End: 9999},
		Management:         config.ManagementConfig{AuthRateLimitPerMin: 1000},
		Health:             config.HealthConfig{Enabled: true, IntervalSeconds: 1, TimeoutSeconds: 2, Path: "/", Threshold: 1},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, _, tracker, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	applyAndTrack(srv, tracker, st.Snapshot(), logger)
	defer srv.Shutdown(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Run(ctx)
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	call := func(method, path, body string) (int, string) {
		var r *http.Request
		if body != "" {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, strings.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := call("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`); code != http.StatusCreated {
		t.Fatalf("create lb: %d %s", code, body)
	}
	mbody := fmt.Sprintf(`{"listen_port":%d,"listen_tls":true,"upstream":{"scheme":"http","host":%q,"port":%d}}`, httpsPort, bhost, bport)
	if code, body := call("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", mbody); code != http.StatusCreated {
		t.Fatalf("create mapping: %d %s", code, body)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if code, body := call("GET", "/api/v1/status", ""); code == 200 && strings.Contains(body, `"health":"up"`) {
			return // success: the live upstream is reported healthy
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("upstream never reported healthy in /status")
}
