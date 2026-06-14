package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/config"
)

func TestManagementAPIEndToEnd(t *testing.T) {
	certFile, keyFile := writeMainTestCert(t) // helper in serve_test.go
	httpsPort := freeTCPPort(t)               // helper in serve_test.go
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
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, _, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv.Apply(st.Snapshot())
	defer srv.Shutdown(context.Background())
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	call := func(method, path, body, tok string) (int, string) {
		var r *http.Request
		if body != "" {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, strings.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, nil)
		}
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Unauthenticated -> 401.
	if code, _ := call("GET", "/api/v1/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	// Create LB.
	if code, body := call("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`, token); code != http.StatusCreated {
		t.Fatalf("create lb: %d %s", code, body)
	}
	// Create an allocate mapping on the (always-on) https port.
	mbody := fmt.Sprintf(`{"listen_port":%d,"listen_tls":true,"allocate":true,"label":"web"}`, httpsPort)
	if code, body := call("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", mbody, token); code != http.StatusCreated {
		t.Fatalf("create mapping: %d %s", code, body)
	}
	// One port in use.
	if code, body := call("GET", "/api/v1/ports", "", token); code != 200 || !strings.Contains(body, `"used":1`) {
		t.Fatalf("ports: %d %s", code, body)
	}
	// Returning the in-use port -> 409.
	if code, _ := call("DELETE", "/api/v1/ports/9000", "", token); code != http.StatusConflict {
		t.Fatalf("return in-use: %d", code)
	}
	// Delete the LB -> 204; the auto port cascades free.
	if code, _ := call("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com", "", token); code != http.StatusNoContent {
		t.Fatalf("delete lb: %d", code)
	}
	if code, body := call("GET", "/api/v1/ports", "", token); code != 200 || !strings.Contains(body, `"used":0`) {
		t.Fatalf("ports after delete: %d %s", code, body)
	}
}
