package health

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// Config tunes the checker (§17).
type Config struct {
	Enabled   bool
	Interval  time.Duration
	Timeout   time.Duration
	Path      string // "" → TCP connect; else HTTP(S) GET of this path
	Threshold int    // consecutive like samples required to flip status
}

// Tracker probes upstream identities and reports flap-resistant status.
// It implements Reporter.
type Tracker struct {
	cfg    Config
	caDir  string
	logger *slog.Logger

	dialer  atomic.Pointer[upstream.Dialer] // the shared safe dialer (refreshed each Apply)
	targets atomic.Pointer[[]Identity]      // current probe set

	mu     sync.Mutex
	states map[Identity]*hstate
}

type hstate struct {
	status   Status
	last     bool // last raw sample healthy?
	haveLast bool
	streak   int // consecutive identical raw samples
}

// New builds a Tracker. A nil logger discards. Non-positive Threshold/Interval/
// Timeout are floored to sane defaults (1, 15s, 5s) so a struct-built Config can't
// produce a 0 ticker interval (panic) or an already-expired probe deadline.
func New(cfg Config, caDir string, logger *slog.Logger) *Tracker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Threshold < 1 {
		cfg.Threshold = 1
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Tracker{cfg: cfg, caDir: caDir, logger: logger, states: make(map[Identity]*hstate)}
}

// SetTargets replaces the probe set: new identities seed at unknown; identities
// no longer present are pruned (so a removed mapping stops reporting).
func (t *Tracker) SetTargets(ids []Identity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	want := make(map[Identity]bool, len(ids))
	for _, id := range ids {
		want[id] = true
		if t.states[id] == nil {
			t.states[id] = &hstate{status: Unknown}
		}
	}
	for id := range t.states {
		if !want[id] {
			delete(t.states, id)
		}
	}
	cp := append([]Identity(nil), ids...)
	t.targets.Store(&cp)
}

// Status reports the current health of up's identity (Reporter).
func (t *Tracker) Status(up store.Upstream) Status {
	id := IdentityOf(up)
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.states[id]; s != nil {
		return s.status
	}
	return Unknown
}

// List returns all tracked identities with status, deterministically ordered
// (Reporter).
func (t *Tracker) List() []Entry {
	t.mu.Lock()
	ids := make([]Identity, 0, len(t.states))
	status := make(map[Identity]Status, len(t.states))
	for id, s := range t.states {
		ids = append(ids, id)
		status[id] = s.status
	}
	t.mu.Unlock()

	sortIdentities(ids)
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, Entry{Scheme: id.Scheme, Host: id.Host, Port: id.Port, TLSMode: id.Mode, Health: status[id]})
	}
	return out
}

// record applies one raw sample under hysteresis. Used by the active probe loop
// AND the live-failure feed.
//
// AIDEV-NOTE: status changes only after `threshold` CONSECUTIVE identical raw
// samples (§17). A flapping upstream stays put (or unknown).
func (t *Tracker) record(id Identity, healthy bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.states[id]
	if s == nil {
		// AIDEV-NOTE: only SetTargets seeds identities. A record() for an unknown
		// identity means it was pruned (its mapping was deleted) between SetTargets and
		// an in-flight live failure (RecordFailure) — ignore it so a deleted upstream
		// can't reappear in /status. The probe loop only records current targets, which
		// are always already seeded.
		return
	}
	if s.haveLast && s.last == healthy {
		s.streak++
	} else {
		s.streak = 1
	}
	s.last, s.haveLast = healthy, true

	desired := Down
	if healthy {
		desired = Up
	}
	if s.status != desired && s.streak >= t.cfg.Threshold {
		t.logger.Info("upstream health changed",
			"scheme", id.Scheme, "host", id.Host, "port", id.Port, "from", s.status, "to", desired)
		s.status = desired
	}
}

// RecordFailure feeds a single unhealthy sample for up's identity (the live
// proxy-failure feed, §17), subject to the same hysteresis as active probes. It is
// IGNORED when the identity is not currently tracked (only SetTargets seeds
// identities), so a live failure racing a mapping deletion can't resurrect the
// deleted upstream in /status.
func (t *Tracker) RecordFailure(up store.Upstream) { t.record(IdentityOf(up), false) }

// Run probes all targets every interval until ctx is cancelled. A disabled
// tracker returns immediately. The first round runs eagerly (no initial delay).
func (t *Tracker) Run(ctx context.Context) {
	if !t.cfg.Enabled {
		return
	}
	ticker := time.NewTicker(t.cfg.Interval) // New() guarantees Interval > 0
	defer ticker.Stop()
	t.checkOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.checkOnce(ctx)
		}
	}
}

// checkOnce probes every current target once and records the result.
func (t *Tracker) checkOnce(ctx context.Context) {
	var targets []Identity
	if p := t.targets.Load(); p != nil {
		targets = *p
	}
	for _, id := range targets {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t.record(id, t.probe(ctx, id))
	}
}
