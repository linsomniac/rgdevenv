// Package auth provides bearer-token authentication and per-source-IP rate
// limiting for the rgdevenv management plane (§15).
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"
)

// minTokenLen is a proxy for "≥256-bit": 32 ASCII chars (§15). The token is
// supplied by the operator; we cannot measure entropy, only enforce a floor.
const minTokenLen = 32

// Authenticator holds a fixed-length digest of the shared token and compares
// presented tokens in constant time.
//
// AIDEV-NOTE: compare SHA-256 digests (fixed 32 bytes), not raw strings —
// subtle.ConstantTimeCompare on unequal lengths returns early and would leak
// the token length. Hashing first makes the comparison length-independent.
type Authenticator struct {
	digest [32]byte
}

// NewAuthenticator builds an Authenticator for the given token.
func NewAuthenticator(token string) *Authenticator {
	return &Authenticator{digest: sha256.Sum256([]byte(token))}
}

// Check reports whether presented equals the configured token (constant time).
func (a *Authenticator) Check(presented string) bool {
	d := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(a.digest[:], d[:]) == 1
}

// LoadToken loads the token from RGDEVENV_TOKEN (preferred) or the token file,
// trims surrounding whitespace, and enforces the minimum length.
func LoadToken(tokenFile string) (string, error) {
	if v := os.Getenv("RGDEVENV_TOKEN"); strings.TrimSpace(v) != "" {
		return validateToken(strings.TrimSpace(v))
	}
	if tokenFile == "" {
		return "", errors.New("auth: no token configured (set RGDEVENV_TOKEN or token_file)")
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("auth: read token file: %w", err)
	}
	return validateToken(strings.TrimSpace(string(b)))
}

func validateToken(tok string) (string, error) {
	if len(tok) < minTokenLen {
		return "", fmt.Errorf("auth: token too short (%d chars; need >= %d / 256-bit)", len(tok), minTokenLen)
	}
	return tok, nil
}

// ParseBearer extracts the token from an "Authorization: Bearer <token>" header
// value. It returns ok=false for any other scheme or an empty value.
func ParseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
