package auth

import (
	"sync"
	"time"
)

// RateLimiter tracks failed auth attempts per source IP in a sliding window and
// blocks once the count reaches the limit (§15).
type RateLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time // injectable clock for tests

	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewRateLimiter builds a limiter; non-positive args fall back to 10/minute.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{limit: limit, window: window, now: time.Now, hits: make(map[string][]time.Time)}
}

// Allowed reports whether the IP is currently under the failure limit.
func (r *RateLimiter) Allowed(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prune(ip)) < r.limit
}

// RecordFailure records a failed auth attempt for the IP.
func (r *RateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits[ip] = append(r.prune(ip), r.now())
}

// prune drops timestamps older than the window and returns the survivors.
// Caller must hold r.mu.
//
// AIDEV-NOTE: entries for an IP that stops failing age out on next access but the
// map key lingers. Fine for a single-developer host (few client IPs); add a GC
// sweep only if this is ever exposed to many sources.
func (r *RateLimiter) prune(ip string) []time.Time {
	cutoff := r.now().Add(-r.window)
	kept := r.hits[ip][:0]
	for _, t := range r.hits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.hits[ip] = kept
	return kept
}
