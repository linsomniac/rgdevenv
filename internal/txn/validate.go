package txn

import (
	"fmt"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// ValidateParams carries the config-dependent inputs validation needs.
type ValidateParams struct {
	Policy       *upstream.Policy
	Covers       func(host string) bool // certificate coverage (resolver.Covers)
	PoolStart    int
	PoolEnd      int
	HTTPSPort    int
	HTTPPort     int // redirect-only port; 0 = disabled
	MgmtBindPort int // 0 = none
	MgmtHost     string
	CADir        string
}

// Validate checks a candidate state: structural invariants (store.Validate) plus
// config-dependent rules (§16). Returns a typed *Error.
//
// AIDEV-NOTE: a listen_port may equal HTTPSPort (the always-on TLS listener that
// serves data-plane mappings) but NOT the pool, the :80 redirect port, or the
// mgmt-bind port; a mapping on HTTPSPort must be TLS. (§6 — the spec's
// "must not equal https_port" wording is superseded by this architecture.)
func Validate(st *store.State, p ValidateParams) error {
	if err := store.Validate(st); err != nil {
		return Validation("invalid_state", err.Error())
	}
	for _, lb := range st.LoadBalancers {
		name, err := canon.Host(lb.Name)
		if err != nil {
			return Validation("invalid_hostname", fmt.Sprintf("load balancer name %q: %v", lb.Name, err))
		}
		if name != lb.Name {
			return Validation("non_canonical_name", fmt.Sprintf("load balancer name %q is not canonical (want %q)", lb.Name, name))
		}
		if p.MgmtHost != "" && name == p.MgmtHost {
			return Conflict("reserved_name", "load balancer name equals the management hostname")
		}
		for _, m := range lb.Mappings {
			if err := validateMapping(name, m, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMapping(lbName string, m store.Mapping, p ValidateParams) error {
	if m.ListenPort < 1 || m.ListenPort > 65535 {
		return Validation("invalid_listen_port", fmt.Sprintf("listen_port %d out of range", m.ListenPort))
	}
	if m.ListenPort >= p.PoolStart && m.ListenPort <= p.PoolEnd {
		return Conflict("listen_port_in_pool", fmt.Sprintf("listen_port %d is inside the port pool [%d,%d]", m.ListenPort, p.PoolStart, p.PoolEnd))
	}
	if p.HTTPPort > 0 && m.ListenPort == p.HTTPPort {
		return Conflict("reserved_port", fmt.Sprintf("listen_port %d is the HTTP redirect port", m.ListenPort))
	}
	if p.MgmtBindPort > 0 && m.ListenPort == p.MgmtBindPort {
		return Conflict("reserved_port", fmt.Sprintf("listen_port %d is the management bind port", m.ListenPort))
	}
	if m.ListenPort == p.HTTPSPort && !m.ListenTLS {
		return Conflict("listen_tls_required", fmt.Sprintf("listen_port %d (https) requires TLS", m.ListenPort))
	}

	if m.Upstream.Scheme != "http" && m.Upstream.Scheme != "https" {
		return Validation("invalid_scheme", fmt.Sprintf("upstream scheme %q must be http or https", m.Upstream.Scheme))
	}
	if m.Upstream.Port < 1 || m.Upstream.Port > 65535 {
		return Validation("invalid_upstream_port", fmt.Sprintf("upstream port %d out of range", m.Upstream.Port))
	}
	if err := p.Policy.AllowedHost(m.Upstream.Host); err != nil {
		return Validation("upstream_not_allowed", err.Error())
	}
	switch m.Upstream.TLS.Mode {
	case "", "verify", "skip":
		// no CA needed
	case "ca":
		if !upstream.ValidCAName(m.Upstream.TLS.CAName) {
			return Validation("invalid_ca_name", fmt.Sprintf("ca_name %q is not a path-safe identifier", m.Upstream.TLS.CAName))
		}
		if _, err := upstream.LoadCA(p.CADir, m.Upstream.TLS.CAName); err != nil {
			return Validation("ca_not_found", fmt.Sprintf("ca %q is not loadable: %v", m.Upstream.TLS.CAName, err))
		}
	default:
		return Validation("invalid_tls_mode", fmt.Sprintf("upstream tls mode %q must be verify, ca, or skip", m.Upstream.TLS.Mode))
	}
	if m.ListenTLS && p.Covers != nil && !p.Covers(lbName) {
		return Validation("host_not_covered", fmt.Sprintf("%s is not covered by a configured certificate", lbName))
	}
	return nil
}
