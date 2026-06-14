package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

// writeWildcardCert writes a self-signed cert+key (default SAN *.sean.realgo.com)
// and returns their file paths. Reused across proxy tests.
func writeWildcardCert(t *testing.T, dnsNames ...string) (certFile, keyFile string) {
	t.Helper()
	if len(dnsNames) == 0 {
		dnsNames = []string{"*.sean.realgo.com"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
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

func TestCertResolverCovers(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Covers("rg-1.sean.realgo.com") {
		t.Error("wildcard should cover subdomain")
	}
	if r.Covers("example.com") {
		t.Error("must not cover unrelated host")
	}
}

func TestCertResolverGetCertificate(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := r.GetCertificate(&tls.ClientHelloInfo{ServerName: "rg-1.sean.realgo.com"})
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate: cert=%v err=%v", cert, err)
	}
}

func TestCertResolverReloadValidateBeforeSwap(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload([]config.CertPair{{CertFile: "/nonexistent", KeyFile: "/nonexistent"}}); err == nil {
		t.Fatal("expected reload error")
	}
	if !r.Covers("rg-1.sean.realgo.com") {
		t.Error("old cert must be retained after a failed reload")
	}
}

func TestCertResolverSNISelectsMatchingCert(t *testing.T) {
	certA, keyA := writeWildcardCert(t, "*.a.example")
	certB, keyB := writeWildcardCert(t, "*.b.example")
	r, err := NewCertResolver([]config.CertPair{
		{CertFile: certA, KeyFile: keyA},
		{CertFile: certB, KeyFile: keyB},
	})
	if err != nil {
		t.Fatal(err)
	}
	hello := &tls.ClientHelloInfo{
		ServerName:        "host.b.example",
		SupportedVersions: []uint16{tls.VersionTLS13},
		CipherSuites:      []uint16{tls.TLS_AES_128_GCM_SHA256},
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
		SupportedCurves:   []tls.CurveID{tls.CurveP256},
	}
	cert, err := r.GetCertificate(hello)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf.VerifyHostname("host.b.example") != nil {
		t.Fatal("SNI selected the wrong certificate")
	}
}

func TestNewCertResolverEmptyPairs(t *testing.T) {
	if _, err := NewCertResolver(nil); err == nil {
		t.Fatal("expected error for empty cert pairs")
	}
}

func TestCertResolverReloadSuccess(t *testing.T) {
	certA, keyA := writeWildcardCert(t, "*.a.example")
	r, err := NewCertResolver([]config.CertPair{{CertFile: certA, KeyFile: keyA}})
	if err != nil {
		t.Fatal(err)
	}
	certB, keyB := writeWildcardCert(t, "*.b.example")
	if err := r.Reload([]config.CertPair{{CertFile: certB, KeyFile: keyB}}); err != nil {
		t.Fatal(err)
	}
	if r.Covers("x.a.example") {
		t.Error("old cert should be gone after successful reload")
	}
	if !r.Covers("x.b.example") {
		t.Error("new cert should be active after successful reload")
	}
}
