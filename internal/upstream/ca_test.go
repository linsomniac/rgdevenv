package upstream

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidCAName(t *testing.T) {
	ok := []string{"build-box", "corp_ca", "ca.internal", "CA-1"}
	bad := []string{"", ".", "..", "../etc/passwd", "a/b", `a\b`, "a..b", "with space"}
	for _, n := range ok {
		if !ValidCAName(n) {
			t.Errorf("ValidCAName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if ValidCAName(n) {
			t.Errorf("ValidCAName(%q) = true, want false", n)
		}
	}
}

// writeTestCAPEM writes a self-signed CA as <dir>/<name>.pem and returns dir.
func writeTestCAPEM(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, name+".pem"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadCA(t *testing.T) {
	dir := writeTestCAPEM(t, "corp")
	pool, err := LoadCA(dir, "corp")
	if err != nil || pool == nil {
		t.Fatalf("LoadCA: pool=%v err=%v", pool, err)
	}
	if _, err := LoadCA(dir, "missing"); err == nil {
		t.Fatal("expected error for missing CA")
	}
	if _, err := LoadCA(dir, "../corp"); err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestListCAs(t *testing.T) {
	dir := writeTestCAPEM(t, "corp") // helper already in ca_test.go
	if err := os.WriteFile(filepath.Join(dir, "build-box.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := ListCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "build-box" || names[1] != "corp" {
		t.Fatalf("ListCAs = %v, want sorted [build-box corp]", names)
	}
	if n, err := ListCAs(filepath.Join(dir, "nope")); err != nil || n != nil {
		t.Fatalf("missing dir: %v %v", n, err)
	}
}
