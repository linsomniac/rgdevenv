package upstream

import (
	"crypto/tls"
	"fmt"
)

// TLSClientConfig builds a *tls.Config for dialing an HTTPS upstream in the given
// mode (§7): "verify"/"" (system roots), "skip" (InsecureSkipVerify, dev-only),
// or "ca" (trusts ONLY the named private CA from caDir; system roots excluded).
// serverName sets ServerName for verification (the caller passes the upstream
// host). Shared by the reverse proxy and the health checker so both honor the
// exact same trust rules.
func TLSClientConfig(mode, caName, serverName, caDir string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	switch mode {
	case "verify", "":
		// verify against system roots
	case "skip":
		cfg.InsecureSkipVerify = true // dev-only (§7)
	case "ca":
		pool, err := LoadCA(caDir, caName)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool // trusts ONLY the named private CA
	default:
		return nil, fmt.Errorf("upstream: unknown tls mode %q", mode)
	}
	return cfg, nil
}
