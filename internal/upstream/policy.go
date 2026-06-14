// Package upstream enforces the SSRF/loop policy and loads named upstream CAs.
package upstream

import (
	"fmt"
	"net"
	"strings"
)

// denyNets are always denied regardless of the allowlist (§15): link-local IPv4
// (which includes cloud-metadata 169.254.169.254) and link-local IPv6.
var denyNets = mustCIDRs("169.254.0.0/16", "fe80::/10")

// Policy decides whether an upstream host/IP may be dialed.
type Policy struct {
	allowHosts map[string]bool
	allowNets  []*net.IPNet
}

// NewPolicy builds a Policy from allow entries (hostnames and/or CIDRs). An entry
// that parses as a CIDR is a network; otherwise it is a hostname.
func NewPolicy(allow []string) *Policy {
	p := &Policy{allowHosts: make(map[string]bool)}
	for _, e := range allow {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			p.allowNets = append(p.allowNets, n)
			continue
		}
		p.allowHosts[strings.ToLower(e)] = true
	}
	return p
}

// SelfGuard identifies rgdevenv's own listener endpoints for loop protection.
type SelfGuard struct {
	LocalIPs    []net.IP
	ListenPorts map[int]bool
}

func (g SelfGuard) isSelf(ip net.IP, port int) bool {
	if !g.ListenPorts[port] {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, lip := range g.LocalIPs {
		if lip.Equal(ip) {
			return true
		}
	}
	return false
}

// AllowedHost reports whether a host NAME (or IP literal) is permitted, without
// resolving DNS. localhost/loopback are always allowed; a name must be in the
// allowlist; an IP literal must fall in an allow CIDR.
//
// AIDEV-NOTE: name-level gate for create/validate time. The dial-time gate
// (Dialer) additionally validates EVERY resolved IP and pins it (§8, §15).
func (p *Policy) AllowedHost(host string) error {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		for _, n := range p.allowNets {
			if n.Contains(ip) {
				return nil
			}
		}
		return fmt.Errorf("upstream: IP %s not in allowlist", host)
	}
	if p.allowHosts[h] {
		return nil
	}
	return fmt.Errorf("upstream: host %q not in allowlist", host)
}

// CheckDialIP returns a non-empty reason if dialing ip:port must be denied
// regardless of the allowlist (§15).
func (p *Policy) CheckDialIP(ip net.IP, port int, self SelfGuard) string {
	if ip == nil {
		return "nil IP denied"
	}
	for _, n := range denyNets {
		if n.Contains(ip) {
			return "link-local/cloud-metadata address denied"
		}
	}
	if self.isSelf(ip, port) {
		return "self/listener address denied (loop protection)"
	}
	return ""
}

func mustCIDRs(cidrs ...string) []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("upstream: bad built-in CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}
