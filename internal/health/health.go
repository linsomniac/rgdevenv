// Package health runs protocol-aware upstream health checks over the shared safe
// dialer and reports per-identity status to the management API (§17).
package health

import (
	"sort"

	"github.com/realgo/rgdevenv/internal/store"
)

// Status is the flap-resistant health of an upstream identity.
type Status string

const (
	Unknown Status = "unknown"
	Up      Status = "up"
	Down    Status = "down"
)

// Identity is a distinct upstream target — the unit of health (§17). For http
// upstreams the tls fields are normalized away so they don't create phantom
// identities.
type Identity struct {
	Scheme string
	Host   string
	Port   int
	Mode   string // upstream tls mode (verify|ca|skip); "" for http
	CAName string
}

// IdentityOf derives the identity of an upstream.
func IdentityOf(up store.Upstream) Identity {
	id := Identity{Scheme: up.Scheme, Host: up.Host, Port: up.Port}
	if up.Scheme == "https" {
		id.Mode = up.TLS.Mode
		id.CAName = up.TLS.CAName
	}
	return id
}

// IdentitiesFrom returns the deduplicated, deterministically sorted set of
// upstream identities referenced by any mapping in st.
func IdentitiesFrom(st *store.State) []Identity {
	seen := make(map[Identity]bool)
	var out []Identity
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			id := IdentityOf(m.Upstream)
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sortIdentities(out)
	return out
}

// Entry is one tracked identity with its current status (for GET /status and the
// API health detail).
type Entry struct {
	Scheme  string `json:"scheme"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSMode string `json:"tls_mode,omitempty"`
	Health  Status `json:"health"`
}

// Reporter is the read side consumed by the API. *Tracker implements it.
type Reporter interface {
	Status(up store.Upstream) Status
	List() []Entry
}

func sortIdentities(ids []Identity) {
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		switch {
		case a.Host != b.Host:
			return a.Host < b.Host
		case a.Port != b.Port:
			return a.Port < b.Port
		case a.Scheme != b.Scheme:
			return a.Scheme < b.Scheme
		case a.Mode != b.Mode:
			return a.Mode < b.Mode
		default:
			return a.CAName < b.CAName
		}
	})
}
