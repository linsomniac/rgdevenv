package health

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/upstream"
)

// SetDialer installs the shared safe dialer used for probes (§8, §15). Until set,
// probes report unhealthy. Refreshed on each proxy reconfigure so the self-guard
// (own-listener deny) stays current.
func (t *Tracker) SetDialer(d *upstream.Dialer) { t.dialer.Store(d) }

// probe performs one health probe of id and returns whether it is healthy.
//
// AIDEV-NOTE: path != "" → HTTP(S) GET, healthy iff a response with status < 500
// arrives (a 4xx still means "responding"); path == "" → bare TCP connect.
// Redirects are NEVER followed, so a redirect to a denied target can't be chased
// (§15). All connections go through the shared safe dialer (validated-IP pinning).
func (t *Tracker) probe(ctx context.Context, id Identity) bool {
	d := t.dialer.Load()
	if d == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()
	addr := net.JoinHostPort(id.Host, strconv.Itoa(id.Port))

	if t.cfg.Path == "" {
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}

	transport := &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true}
	if id.Scheme == "https" {
		tlsCfg, err := upstream.TLSClientConfig(id.Mode, id.CAName, id.Host, t.caDir)
		if err != nil {
			return false
		}
		transport.TLSClientConfig = tlsCfg
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       t.cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	url := id.Scheme + "://" + addr + t.cfg.Path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode < 500
}
