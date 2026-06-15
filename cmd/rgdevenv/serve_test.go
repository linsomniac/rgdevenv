package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s never came up", addr)
}

func writeMainTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "*.sean.realgo.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"*.sean.realgo.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestSetupServerWiring(t *testing.T) {
	const mgmtToken = "0123456789abcdef0123456789abcdef"
	t.Setenv("RGDEVENV_TOKEN", mgmtToken)

	certFile, keyFile := writeMainTestCert(t)
	httpsPort := freeTCPPort(t)
	cfg := &config.Config{
		BindAddr:           "127.0.0.1",
		HTTPSPort:          httpsPort,
		HTTPPort:           0, // disable :80 in this test
		CertFile:           certFile,
		KeyFile:            keyFile,
		ManagementHostname: "rgdevenv.sean.realgo.com",
		StateFile:          filepath.Join(t.TempDir(), "state.json"),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, _, _, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv.Apply(st.Snapshot())
	defer srv.Shutdown(context.Background())

	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "rgdevenv.sean.realgo.com"},
	}}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d/", httpsPort), nil)
	req.Host = "rgdevenv.sean.realgo.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 ||
		resp.TLS.PeerCertificates[0].Subject.CommonName != "*.sean.realgo.com" {
		t.Fatalf("unexpected served certificate: %+v", resp.TLS)
	}
	resp.Body.Close()
	// TLS handshake proves the cert loaded + listener bound; mgmt host 404s in phase 1.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	mgmtGet := func(path, token string) int {
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d%s", httpsPort, path), nil)
		req.Host = "rgdevenv.sean.realgo.com"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := mgmtGet("/healthz", ""); code != http.StatusOK {
		t.Fatalf("mgmt /healthz = %d, want 200", code)
	}
	if code := mgmtGet("/api/v1/status", ""); code != http.StatusUnauthorized {
		t.Fatalf("mgmt /api/v1/status without token = %d, want 401", code)
	}
	if code := mgmtGet("/api/v1/status", mgmtToken); code != http.StatusOK {
		t.Fatalf("mgmt /api/v1/status with token = %d, want 200", code)
	}
}
