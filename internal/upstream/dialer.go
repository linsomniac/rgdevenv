package upstream

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// PolicyError indicates an upstream dial was refused by policy. The reverse proxy
// logs it but returns a generic 502 to clients (§8).
type PolicyError struct {
	Host   string
	IP     net.IP
	Reason string
}

func (e *PolicyError) Error() string {
	if e.IP != nil {
		return fmt.Sprintf("upstream policy: %s (%s): %s", e.Host, e.IP, e.Reason)
	}
	return fmt.Sprintf("upstream policy: %s: %s", e.Host, e.Reason)
}

// Dialer is the single shared SAFE dialer used by both the reverse proxy and the
// (Phase-2) health checker. It resolves the upstream name, validates EVERY
// returned address against the allowlist + deny rules, then dials a PINNED
// validated IP with no OS re-resolution — closing the DNS-rebinding window.
type Dialer struct {
	policy *Policy
	self   SelfGuard

	// Resolve and Dial are injectable for tests; defaults use the system
	// resolver and a net.Dialer.
	Resolve func(ctx context.Context, host string) ([]net.IP, error)
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
}

// New builds a safe dialer with the given policy, self-guard, and dial timeout.
func New(policy *Policy, self SelfGuard, timeout time.Duration) *Dialer {
	base := &net.Dialer{Timeout: timeout}
	d := &Dialer{policy: policy, self: self}
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	d.Dial = base.DialContext
	return d
}

// DialContext matches http.Transport.DialContext.
//
// AIDEV-NOTE: SECURITY-CRITICAL — the ONLY place upstream connections are made.
// Order is deliberate: allow-check (name) -> resolve -> deny-check (every IP) ->
// dial a PINNED IP. Never pass `host` to the OS dialer (that re-resolves and
// reopens the rebinding window). Deny-if-ANY: one bad address fails the whole dial.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("upstream: bad address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("upstream: bad port %q", portStr)
	}

	if err := d.policy.AllowedHost(host); err != nil {
		return nil, &PolicyError{Host: host, Reason: err.Error()}
	}

	ips, err := d.Resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("upstream: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("upstream: no addresses for %q", host)
	}
	for _, ip := range ips {
		if reason := d.policy.CheckDialIP(ip, port, d.self); reason != "" {
			return nil, &PolicyError{Host: host, IP: ip, Reason: reason}
		}
	}
	// All IPs are already validated; a failure here is a connectivity error, returned as-is (not a PolicyError).
	var lastErr error
	for _, ip := range ips {
		conn, err := d.Dial(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
