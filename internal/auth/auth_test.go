package auth

import (
	"os"
	"path/filepath"
	"testing"
)

const goodToken = "0123456789abcdef0123456789abcdef" // 32 chars >= 256-bit

func TestAuthenticatorCheck(t *testing.T) {
	a := NewAuthenticator(goodToken)
	if !a.Check(goodToken) {
		t.Fatal("correct token rejected")
	}
	if a.Check("wrong") {
		t.Fatal("wrong (shorter) token accepted")
	}
	if a.Check(goodToken + "x") {
		t.Fatal("wrong (longer) token accepted")
	}
	if a.Check("") {
		t.Fatal("empty token accepted")
	}
}

func TestLoadTokenFromEnv(t *testing.T) {
	t.Setenv("RGDEVENV_TOKEN", "  "+goodToken+"  ") // trimmed
	tok, err := LoadToken("")
	if err != nil {
		t.Fatal(err)
	}
	if tok != goodToken {
		t.Fatalf("token = %q", tok)
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(goodToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadToken(p)
	if err != nil {
		t.Fatal(err)
	}
	if tok != goodToken {
		t.Fatalf("token = %q", tok)
	}
}

func TestLoadTokenTooShort(t *testing.T) {
	t.Setenv("RGDEVENV_TOKEN", "short")
	if _, err := LoadToken(""); err == nil {
		t.Fatal("expected error for short token")
	}
}

func TestLoadTokenMissing(t *testing.T) {
	t.Setenv("RGDEVENV_TOKEN", "") // ensure no ambient token
	if _, err := LoadToken(""); err == nil {
		t.Fatal("expected error when no token configured")
	}
}

func TestParseBearer(t *testing.T) {
	if tok, ok := ParseBearer("Bearer abc123"); !ok || tok != "abc123" {
		t.Fatalf("ParseBearer good = %q,%v", tok, ok)
	}
	if _, ok := ParseBearer("Basic abc"); ok {
		t.Fatal("ParseBearer should reject non-Bearer")
	}
	if _, ok := ParseBearer(""); ok {
		t.Fatal("ParseBearer should reject empty")
	}
}
