package proxy

import (
	"bufio"
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
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func waitListening(t *testing.T, addr string) {
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

// rgClient returns an HTTP client that always connects to 127.0.0.1:httpsPort but
// keeps the request URL's Host/SNI, so dispatch routes by Host. Our self-signed
// front cert is skipped (we are testing the proxy, not cert trust).
func rgClient(httpsPort int) *http.Client {
	d := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func startProxyOn(t *testing.T, st *store.State, allow []string, caDir string, httpsPort, httpPort int) func() {
	t.Helper()
	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{
		BindAddr: "127.0.0.1", HTTPSPort: httpsPort, HTTPPort: httpPort,
		CADir: caDir, MgmtHost: "rgdevenv.sean.realgo.com", DialTimeout: 3 * time.Second,
	}
	srv := NewServer(cfg, upstream.NewPolicy(allow), resolver, DefaultLimits(), discardLogger())
	srv.Apply(st)
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if httpPort > 0 {
		waitListening(t, fmt.Sprintf("127.0.0.1:%d", httpPort))
	}
	return func() { srv.Shutdown(context.Background()) }
}

// newTLSBackend starts an HTTPS backend (cert for 127.0.0.1/localhost signed by a
// fresh CA) and returns its host, port, the CA PEM, and a stop func.
func newTLSBackend(t *testing.T, body string) (host string, port int, caPEM []byte, stop func()) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "integration ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(101), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})}
	go srv.Serve(tlsLn)
	h, ps, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(ps)
	return h, p, caPEM, func() { srv.Close() }
}

func TestIntegrationHTTPRouting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	}))
	defer backend.Close()
	bh, bp := backendHostPort(t, backend.URL)

	httpsPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	client := rgClient(httpsPort)

	resp, err := client.Get("https://rg-1.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("known host: code=%d body=%q", resp.StatusCode, body)
	}

	resp2, err := client.Get("https://nope.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("unknown host: code=%d, want 404", resp2.StatusCode)
	}
}

func TestIntegrationUpstreamTLSModes(t *testing.T) {
	bh, bp, caPEM, stop := newTLSBackend(t, "secure-ok")
	defer stop()

	caDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(caDir, "corp.pem"), caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	httpsPort := freePort(t)
	mk := func(name, mode, caName string) store.LoadBalancer {
		return store.LoadBalancer{Name: name, Mappings: []store.Mapping{{
			ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "https", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: mode, CAName: caName}},
		}}}
	}
	st := &store.State{LoadBalancers: []store.LoadBalancer{
		mk("rg-ca.sean.realgo.com", "ca", "corp"),
		mk("rg-skip.sean.realgo.com", "skip", ""),
		mk("rg-verify.sean.realgo.com", "verify", ""),
	}}
	defer startProxyOn(t, st, nil, caDir, httpsPort, 0)()
	client := rgClient(httpsPort)

	for _, host := range []string{"rg-ca.sean.realgo.com", "rg-skip.sean.realgo.com"} {
		resp, err := client.Get("https://" + host + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "secure-ok" {
			t.Fatalf("%s: code=%d body=%q (want 200/secure-ok)", host, resp.StatusCode, body)
		}
	}

	// verify mode: self-signed CA not in system roots -> generic 502, no leak.
	resp, err := client.Get("https://rg-verify.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("verify mode against self-signed: code=%d, want 502", resp.StatusCode)
	}
	if strings.Contains(string(body), bh) || strings.Contains(strings.ToLower(string(body)), "certificate") {
		t.Fatalf("502 body leaked detail: %q", body)
	}
}

func TestIntegrationWebSocketUpgrade(t *testing.T) {
	// Backend that completes a minimal upgrade and echoes one line.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
		line, _ := buf.ReadString('\n')
		buf.WriteString("echo:" + line)
		buf.Flush()
	})}
	go backend.Serve(ln)
	defer backend.Close()
	bh, ps, _ := net.SplitHostPort(ln.Addr().String())
	bp, _ := strconv.Atoi(ps)

	httpsPort := freePort(t) // always-on TLS port (unused here)
	wsPort := freePort(t)    // plaintext on-demand listener
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-ws.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: wsPort, ListenTLS: false,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", wsPort))

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", wsPort))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: rg-ws.sean.realgo.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("expected 101, got %q (err=%v)", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	fmt.Fprintf(conn, "ping\n")
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(echo) != "echo:ping" {
		t.Fatalf("websocket echo = %q, want %q", strings.TrimSpace(echo), "echo:ping")
	}
}

func TestIntegrationSelfLoopRefused(t *testing.T) {
	httpsPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-loop.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			// Upstream points at OUR OWN https listener -> loop -> denied.
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: httpsPort, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	client := rgClient(httpsPort)

	resp, err := client.Get("https://rg-loop.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("self-loop must be refused with 502, got %d", resp.StatusCode)
	}
}

func TestIntegration80Redirect(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	bh, bp := backendHostPort(t, backend.URL)

	httpsPort := freePort(t)
	httpPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, httpPort)()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Known host -> 308 to canonical https URL on the real https port.
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/path?q=1", httpPort), nil)
	req.Host = "rg-1.sean.realgo.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", resp.StatusCode)
	}
	want := fmt.Sprintf("https://rg-1.sean.realgo.com:%d/path?q=1", httpsPort)
	if got := resp.Header.Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}

	// Unknown host -> 404, no Location.
	req2, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", httpPort), nil)
	req2.Host = "evil.example.com"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 404 || resp2.Header.Get("Location") != "" {
		t.Fatalf("unknown host on :80 = %d loc=%q, want 404/no-loc", resp2.StatusCode, resp2.Header.Get("Location"))
	}
}
