package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/realgo/rgdevenv/internal/config"
)

// CertResolver holds the supplied certificate(s), selects one by SNI (§7), and
// supports validate-before-swap reload.
type CertResolver struct {
	mu    sync.RWMutex
	certs []tls.Certificate
}

// NewCertResolver loads and parses the given cert/key pairs (primary first).
func NewCertResolver(pairs []config.CertPair) (*CertResolver, error) {
	certs, err := loadPairs(pairs)
	if err != nil {
		return nil, err
	}
	return &CertResolver{certs: certs}, nil
}

func loadPairs(pairs []config.CertPair) ([]tls.Certificate, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("proxy: no certificate pairs configured")
	}
	out := make([]tls.Certificate, 0, len(pairs))
	for _, p := range pairs {
		cert, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("proxy: load cert %s: %w", p.CertFile, err)
		}
		if cert.Leaf == nil && len(cert.Certificate) > 0 {
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				return nil, fmt.Errorf("proxy: parse leaf %s: %w", p.CertFile, err)
			}
			cert.Leaf = leaf
		}
		out = append(out, cert)
	}
	return out, nil
}

// GetCertificate is the tls.Config.GetCertificate callback. It prefers a cert
// matching the SNI name and falls back to the primary pair.
func (r *CertResolver) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.certs {
		if err := hello.SupportsCertificate(&r.certs[i]); err == nil {
			return &r.certs[i], nil
		}
	}
	if len(r.certs) > 0 {
		return &r.certs[0], nil
	}
	return nil, fmt.Errorf("proxy: no certificate available")
}

// Covers reports whether some loaded certificate is valid for the canonical host.
func (r *CertResolver) Covers(host string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.certs {
		if r.certs[i].Leaf != nil && r.certs[i].Leaf.VerifyHostname(host) == nil {
			return true
		}
	}
	return false
}

// Reload validates new pairs and swaps them in only on success (§7). On any
// error the previously loaded certs are retained; verification is never silently
// downgraded.
//
// AIDEV-NOTE: validate-before-swap — parse everything first, mutate only after
// all pairs load cleanly.
func (r *CertResolver) Reload(pairs []config.CertPair) error {
	certs, err := loadPairs(pairs)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.certs = certs
	r.mu.Unlock()
	return nil
}

// TLSConfig builds a server tls.Config backed by this resolver.
func (r *CertResolver) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: r.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}
