// Package canon canonicalizes hostnames into the single form used as a key for
// routing, validation, auth, persistence, and certificate matching.
package canon

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// Host canonicalizes s: strips an optional :port, strips a trailing dot,
// lowercases, and applies IDNA (lookup profile) normalization. Malformed hosts
// return an error.
//
// AIDEV-NOTE: This is the ONLY canonicalizer. Routing, auth, persistence, and
// cert matching must all key off this exact form to avoid host-confusion
// bypasses (§5, §15).
func Host(s string) (string, error) {
	h := strings.TrimSpace(s)
	if h == "" {
		return "", fmt.Errorf("canon: empty host")
	}
	// Strip an optional port. SplitHostPort errors when there is no port, in
	// which case we keep the original string.
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	h = strings.ToLower(h)
	if h == "" {
		return "", fmt.Errorf("canon: empty host after normalization")
	}
	if h == "." || strings.Contains(h, "..") {
		return "", fmt.Errorf("canon: malformed host %q", s)
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", fmt.Errorf("canon: %q: %w", s, err)
	}
	return ascii, nil
}
