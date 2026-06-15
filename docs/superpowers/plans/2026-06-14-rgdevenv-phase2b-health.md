# rgdevenv Phase 2b — Upstream Health Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a background, protocol-aware upstream health checker (over the shared safe dialer) whose results surface on `GET /lbs` (per-mapping `health`) and `GET /status` (per-identity `upstreams[]`), with flap-resistant hysteresis and an optional live-failure feed from the reverse proxy.

**Architecture:** A `health.Tracker` owns a map of distinct upstream identities (`scheme/host/port/tls-mode/ca`) → hysteresis state. A background goroutine probes each identity every interval through the **same `upstream.Dialer`** the proxy uses (validated-IP pinning, no redirect-following), recording a healthy/unhealthy sample; status flips only after N consecutive like samples. The proxy's reverse-proxy `ErrorHandler` optionally feeds a `false` sample on a live upstream error. `setupServer` constructs the tracker, refreshes its dialer + target set after every `proxy.Server.Apply`, starts/stops its goroutine with the daemon, and injects it into the API as a `health.Reporter`.

**Tech Stack:** Go 1.22 stdlib (`net/http`, `crypto/tls`, `context`, `log/slog`, `sync/atomic`); no new dependencies.

**Spec sections:** §8 (shared safe dialer), §12 (`GET /lbs` "with health", `GET /status` "upstream health"), §15 (deny-redirect, shared dialer), §17 (protocol-aware health, hysteresis, live failures feed status), §19 (testing).

> **Commit convention:** every commit message in this plan must additionally end with the trailer
> `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
> (omitted from the per-task `-m` examples below for brevity). Work on a branch, not `master`.

---

## Design decisions (review these first)

These resolve under-specified details in §17. They are deliberate, boring choices — flag any you disagree with before execution.

1. **Probe semantics.** `path != ""` → an HTTP(S) `GET scheme://host:port{path}`; **healthy iff a response arrives with status `< 500`** (a 4xx still means "the server is responding" — dev upstreams may require auth). `path == ""` → a bare **TCP connect** (the "falling back to TCP connect" of §17), healthy iff the connection opens. Redirects are **never followed** (`CheckRedirect` returns `http.ErrUseLastResponse`), which trivially satisfies "health checks do not follow redirects to denied targets" (§15).
2. **Defaults** (new `[health]` config, integer seconds to match the existing int-based config style): `enabled = true`, `interval_seconds = 15`, `timeout_seconds = 5`, `path = "/"`, `threshold = 2`.
3. **Hysteresis.** Each identity starts `unknown`. A status is set/flipped only after `threshold` **consecutive identical** raw samples. A flapping upstream (alternating results) stays in its current status (or `unknown`).
4. **Shared dialer (not a private one).** The tracker probes through `proxy.Server.Dialer()` — the exact dialer instance the proxy built on its last `Apply` — so the SSRF/loop policy and self-guard (own-listener deny) are identical for proxy and health (§8, §15). The dialer is refreshed on every `Apply`.
5. **Identity, not host:port.** Health is tracked per distinct `(scheme, host, port, tls-mode, ca_name)` (§17). For `http` upstreams the tls fields are normalized away (so `http` mode differences don't create phantom identities).
6. **Live failures feed status.** The reverse-proxy `ErrorHandler` (already the single choke-point for upstream errors) optionally calls an observer that records a `false` sample — subject to the same hysteresis. This is isolated behind one callback and one setter; it adds no health import to the proxy package.
7. **Sequential probing.** Probes run one-at-a-time per round. A dev host has a handful of upstreams; bounded concurrency is a future optimization, not now (YAGNI).
8. **Graceful disable.** `enabled = false` (or a struct-built `config.Config` that never sets `[health]`) yields a tracker whose `Run` is a no-op and whose `Status`/`List` report `unknown`/empty — so the API degrades to "unknown" exactly as the Phase 2a note anticipated.

---

## File structure

**New files**
```
internal/health/health.go         Status, Identity, IdentityOf, IdentitiesFrom, Entry, Reporter
internal/health/health_test.go
internal/health/checker.go        Tracker, Config, hysteresis (record), Status, List, SetTargets, Run, RecordFailure
internal/health/checker_test.go
internal/health/probe.go          SetDialer, probe (HTTP(S)/TCP via the shared dialer)
internal/health/probe_test.go
internal/upstream/tlsconfig.go    TLSClientConfig (shared by reverse proxy + health probes)
internal/upstream/tlsconfig_test.go
internal/api/views.go             lbView/mappingView (+ health) + noopHealth reporter
internal/api/views_test.go
cmd/rgdevenv/health_e2e_test.go   end-to-end: /status reports a live upstream healthy
```

**Modified files**
```
internal/config/config.go         + HealthConfig, defaults, normalize validation, HealthInterval/HealthTimeout
internal/config/config_test.go    + defaults + validation tests
internal/proxy/reverseproxy.go    BuildReverseProxy gains a trailing onError func(); ErrorHandler calls it
internal/proxy/router.go          RouteDeps.OnUpstreamError; thread per-upstream closure into BuildReverseProxy
internal/proxy/server.go          dialer atomic.Pointer + Dialer(); onUpstreamErr + SetUpstreamErrorObserver; Apply wiring
internal/proxy/reverseproxy_test.go  update 2 call sites; + observer test
internal/proxy/server_test.go     + Dialer()-set-after-Apply test
internal/api/api.go               Deps.Health + Handler.health (+ noop default)
internal/api/lbs.go               list/get/create/patch return lbView (with health)
internal/api/mappings.go          create/put return mappingView (with health)
internal/api/misc.go              status adds upstreams[] from health.List()
internal/api/lbs_test.go          extract newAPITestHandlerWith(t, reporter) helper
cmd/rgdevenv/serve.go             build tracker, applyAndTrack, start/stop Run, observer, inject Health; signature +*health.Tracker
cmd/rgdevenv/serve_test.go        update setupServer call site (5 returns)
cmd/rgdevenv/integration_test.go  update setupServer call site (5 returns)
```

---

## Task 1: Health configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go` (add `"time"` to its imports if absent):

```go
func TestHealthDefaults(t *testing.T) {
	cfg, err := Load("") // no file → defaults + env
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Health.Enabled {
		t.Fatal("health should default to enabled")
	}
	if cfg.HealthInterval() != 15*time.Second {
		t.Fatalf("interval = %v, want 15s", cfg.HealthInterval())
	}
	if cfg.HealthTimeout() != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", cfg.HealthTimeout())
	}
	if cfg.Health.Path != "/" {
		t.Fatalf("path = %q, want /", cfg.Health.Path)
	}
	if cfg.Health.Threshold != 2 {
		t.Fatalf("threshold = %d, want 2", cfg.Health.Threshold)
	}
}

func TestHealthValidation(t *testing.T) {
	for name, mut := range map[string]func(*Config){
		"interval":  func(c *Config) { c.Health.IntervalSeconds = 0 },
		"timeout":   func(c *Config) { c.Health.TimeoutSeconds = -1 },
		"threshold": func(c *Config) { c.Health.Threshold = 0 },
	} {
		c := Default()
		mut(c)
		if err := c.normalize(); err == nil {
			t.Fatalf("%s: expected normalize error", name)
		}
	}
}

func TestHealthPathLeadingSlash(t *testing.T) {
	c := Default()
	c.Health.Path = "healthz"
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Health.Path != "/healthz" {
		t.Fatalf("path = %q, want /healthz", c.Health.Path)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestHealth' -v`
Expected: compile error — `cfg.Health` / `HealthInterval` undefined.

- [ ] **Step 3: Implement the config additions**

In `internal/config/config.go` add `"time"` to the imports. Add the struct type near the other `*Config` sub-structs:

```go
type HealthConfig struct {
	Enabled         bool   `toml:"enabled"`
	IntervalSeconds int    `toml:"interval_seconds"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
	Path            string `toml:"path"` // "" → TCP-connect probe; else HTTP(S) GET of this path
	Threshold       int    `toml:"threshold"`
}
```

Add a field to `Config` (next to `Log LogConfig`):

```go
	Health HealthConfig `toml:"health"`
```

In `Default()`, add to the returned literal:

```go
		Health: HealthConfig{Enabled: true, IntervalSeconds: 15, TimeoutSeconds: 5, Path: "/", Threshold: 2},
```

In `normalize()`, just before `return c.validateMgmtBind()`, insert:

```go
	// AIDEV-NOTE: health (§17). interval/timeout are integer SECONDS (matches the
	// int-based config style); a non-empty probe path is forced to start with "/".
	if c.Health.IntervalSeconds < 1 {
		return fmt.Errorf("config: health.interval_seconds must be >= 1")
	}
	if c.Health.TimeoutSeconds < 1 {
		return fmt.Errorf("config: health.timeout_seconds must be >= 1")
	}
	if c.Health.Threshold < 1 {
		return fmt.Errorf("config: health.threshold must be >= 1")
	}
	if c.Health.Path != "" && !strings.HasPrefix(c.Health.Path, "/") {
		c.Health.Path = "/" + c.Health.Path
	}
```

Add the accessors at the end of the file:

```go
// HealthInterval is the probe interval as a duration.
func (c *Config) HealthInterval() time.Duration { return time.Duration(c.Health.IntervalSeconds) * time.Second }

// HealthTimeout is the per-probe timeout as a duration.
func (c *Config) HealthTimeout() time.Duration { return time.Duration(c.Health.TimeoutSeconds) * time.Second }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS (all config tests, including the three new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add [health] settings (interval/timeout/path/threshold)"
```

---

## Task 2: Health identity and reporter types

**Files:**
- Create: `internal/health/health.go`
- Test: `internal/health/health_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/health/health_test.go`:

```go
package health

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestIdentityOfNormalizesTLSForHTTP(t *testing.T) {
	a := IdentityOf(store.Upstream{Scheme: "http", Host: "localhost", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}})
	b := IdentityOf(store.Upstream{Scheme: "http", Host: "localhost", Port: 80, TLS: store.UpstreamTLS{Mode: "skip"}})
	if a != b {
		t.Fatalf("http identities must ignore tls fields: %+v vs %+v", a, b)
	}
	c := IdentityOf(store.Upstream{Scheme: "https", Host: "h", Port: 443, TLS: store.UpstreamTLS{Mode: "ca", CAName: "corp"}})
	if c.Mode != "ca" || c.CAName != "corp" {
		t.Fatalf("https identity must keep tls fields: %+v", c)
	}
}

func TestIdentitiesFromDedupAndSort(t *testing.T) {
	st := &store.State{LoadBalancers: []store.LoadBalancer{
		{Name: "b", Mappings: []store.Mapping{{Upstream: store.Upstream{Scheme: "http", Host: "h2", Port: 9001}}}},
		{Name: "a", Mappings: []store.Mapping{
			{Upstream: store.Upstream{Scheme: "http", Host: "h1", Port: 9000}},
			{Upstream: store.Upstream{Scheme: "http", Host: "h1", Port: 9000}}, // duplicate identity
		}},
	}}
	ids := IdentitiesFrom(st)
	if len(ids) != 2 {
		t.Fatalf("want 2 deduped identities, got %d (%+v)", len(ids), ids)
	}
	if ids[0].Host != "h1" || ids[1].Host != "h2" {
		t.Fatalf("identities not sorted: %+v", ids)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/health/ -v`
Expected: FAIL — no Go files / undefined `IdentityOf`.

- [ ] **Step 3: Implement the types**

`internal/health/health.go`:

```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/health/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/health/health.go internal/health/health_test.go
git commit -m "feat(health): upstream identity + reporter types"
```

---

## Task 3: Tracker + hysteresis state machine

**Files:**
- Create: `internal/health/checker.go`
- Test: `internal/health/checker_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/health/checker_test.go`:

```go
package health

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func up(host string, port int) store.Upstream {
	return store.Upstream{Scheme: "http", Host: host, Port: port}
}

func TestHysteresisFlipsOnlyAfterThreshold(t *testing.T) {
	tr := New(Config{Threshold: 2}, "", nil)
	u := up("localhost", 9000)
	id := IdentityOf(u)

	if tr.Status(u) != Unknown {
		t.Fatal("initial status must be unknown")
	}
	tr.record(id, true)
	if tr.Status(u) != Unknown {
		t.Fatal("one healthy sample must not flip with threshold 2")
	}
	tr.record(id, true)
	if tr.Status(u) != Up {
		t.Fatalf("two healthy → up, got %s", tr.Status(u))
	}
	tr.record(id, false) // streak resets, not enough to flip
	if tr.Status(u) != Up {
		t.Fatal("a single bad sample must not flip away from up")
	}
	tr.record(id, false)
	if tr.Status(u) != Down {
		t.Fatalf("two bad → down, got %s", tr.Status(u))
	}
}

func TestSetTargetsSeedsAndPrunes(t *testing.T) {
	tr := New(Config{Threshold: 1}, "", nil)
	a, b := up("a", 1), up("b", 2)
	tr.SetTargets([]Identity{IdentityOf(a), IdentityOf(b)})

	tr.record(IdentityOf(a), true)
	if tr.Status(a) != Up {
		t.Fatal("a should be up")
	}
	tr.SetTargets([]Identity{IdentityOf(b)}) // drop a
	if tr.Status(a) != Unknown {
		t.Fatal("pruned identity must report unknown")
	}
	list := tr.List()
	if len(list) != 1 || list[0].Host != "b" {
		t.Fatalf("List after prune = %+v", list)
	}
}

func TestNewBumpsZeroThreshold(t *testing.T) {
	tr := New(Config{Threshold: 0}, "", nil)
	u := up("h", 1)
	tr.record(IdentityOf(u), true)
	if tr.Status(u) != Up {
		t.Fatal("threshold 0 must be treated as 1 (one sample flips)")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/health/ -run 'TestHysteresis|TestSetTargets|TestNewBumps' -v`
Expected: FAIL — undefined `New`, `Config`, `record`.

- [ ] **Step 3: Implement the Tracker core**

`internal/health/checker.go`:

```go
package health

import (
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

// New builds a Tracker. A nil logger discards. Threshold < 1 is treated as 1.
func New(cfg Config, caDir string, logger *slog.Logger) *Tracker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Threshold < 1 {
		cfg.Threshold = 1
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
		s = &hstate{status: Unknown}
		t.states[id] = s
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
		s.status = desired
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/health/ -v`
Expected: PASS (Task 2 + Task 3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/health/checker.go internal/health/checker_test.go
git commit -m "feat(health): tracker with hysteresis state machine"
```

---

## Task 4: Shared upstream TLS-config helper

**Files:**
- Create: `internal/upstream/tlsconfig.go`
- Modify: `internal/proxy/reverseproxy.go`
- Test: `internal/upstream/tlsconfig_test.go`

- [ ] **Step 1: Write the failing test**

`internal/upstream/tlsconfig_test.go`:

```go
package upstream

import "testing"

func TestTLSClientConfigModes(t *testing.T) {
	if c, err := TLSClientConfig("verify", "", "host", ""); err != nil || c.InsecureSkipVerify {
		t.Fatalf("verify: c=%+v err=%v", c, err)
	}
	if c, err := TLSClientConfig("", "", "host", ""); err != nil || c.InsecureSkipVerify {
		t.Fatalf("empty mode == verify: c=%+v err=%v", c, err)
	}
	if c, err := TLSClientConfig("skip", "", "host", ""); err != nil || !c.InsecureSkipVerify {
		t.Fatalf("skip: c=%+v err=%v", c, err)
	}
	if _, err := TLSClientConfig("bogus", "", "host", ""); err == nil {
		t.Fatal("unknown mode must error")
	}
	if _, err := TLSClientConfig("ca", "missing", "host", t.TempDir()); err == nil {
		t.Fatal("ca mode with missing CA must error")
	}
}

func TestTLSClientConfigServerName(t *testing.T) {
	c, err := TLSClientConfig("verify", "", "up.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerName != "up.example" {
		t.Fatalf("ServerName = %q, want up.example", c.ServerName)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/upstream/ -run TestTLSClientConfig -v`
Expected: FAIL — undefined `TLSClientConfig`.

- [ ] **Step 3: Implement the helper and refactor the proxy to use it**

`internal/upstream/tlsconfig.go`:

```go
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
```

In `internal/proxy/reverseproxy.go`, replace the body of `upstreamTLSConfig` with a delegation and drop the now-unused `crypto/tls` import:

```go
func upstreamTLSConfig(up store.Upstream, caDir string) (*tls.Config, error) {
	return upstream.TLSClientConfig(up.TLS.Mode, up.TLS.CAName, up.Host, caDir)
}
```

Note: after this change `crypto/tls` is referenced in `reverseproxy.go` only by the `*tls.Config` return type, so **keep** the `crypto/tls` import. (Verify with the build in Step 4; if `goimports`/the compiler reports it unused, remove it.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/upstream/ ./internal/proxy/ -v`
Expected: PASS — all upstream and proxy tests still green (the refactor is behavior-preserving).

- [ ] **Step 5: Commit**

```bash
git add internal/upstream/tlsconfig.go internal/upstream/tlsconfig_test.go internal/proxy/reverseproxy.go
git commit -m "refactor(upstream): extract shared TLSClientConfig; proxy delegates"
```

---

## Task 5: Probe (HTTP(S)/TCP over the shared dialer)

**Files:**
- Create: `internal/health/probe.go`
- Test: `internal/health/probe_test.go`

- [ ] **Step 1: Write the failing test**

`internal/health/probe_test.go`:

```go
package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/upstream"
)

func testDialer() *upstream.Dialer {
	return upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, 2*time.Second)
}

func hostPort(t *testing.T, url string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestProbeHTTPUpAndDown(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	tr := New(Config{Path: "/", Timeout: 2 * time.Second}, "", nil)
	tr.SetDialer(testDialer())

	h, p := hostPort(t, ok.URL)
	if !tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("200 response must be healthy")
	}
	h, p = hostPort(t, bad.URL)
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("500 response must be unhealthy")
	}
}

func TestProbeTCPMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h, p := hostPort(t, srv.URL)

	tr := New(Config{Path: "", Timeout: time.Second}, "", nil)
	tr.SetDialer(testDialer())
	if !tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("open port must be healthy in TCP mode")
	}
	srv.Close()
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: h, Port: p}) {
		t.Fatal("closed port must be unhealthy in TCP mode")
	}
}

func TestProbeNoDialerIsUnhealthy(t *testing.T) {
	tr := New(Config{Path: "/", Timeout: time.Second}, "", nil)
	if tr.probe(context.Background(), Identity{Scheme: "http", Host: "localhost", Port: 1}) {
		t.Fatal("no dialer set → unhealthy")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/health/ -run TestProbe -v`
Expected: FAIL — undefined `SetDialer` / `probe`.

- [ ] **Step 3: Implement the probe**

`internal/health/probe.go`:

```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/health/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/health/probe.go internal/health/probe_test.go
git commit -m "feat(health): protocol-aware probe over the shared safe dialer"
```

---

## Task 6: Background probe loop

**Files:**
- Modify: `internal/health/checker.go`
- Test: `internal/health/checker_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/health/checker_test.go` (add imports `"context"`, `"net/http"`, `"net/http/httptest"`, `"time"`):

```go
func TestCheckOnceDrivesStatus(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	h, p := hostPort(t, ok.URL)
	u := store.Upstream{Scheme: "http", Host: h, Port: p}

	tr := New(Config{Enabled: true, Path: "/", Timeout: 2 * time.Second, Threshold: 2}, "", nil)
	tr.SetDialer(testDialer())
	tr.SetTargets([]Identity{IdentityOf(u)})

	tr.checkOnce(context.Background())
	if tr.Status(u) != Unknown {
		t.Fatalf("one round, threshold 2 → unknown, got %s", tr.Status(u))
	}
	tr.checkOnce(context.Background())
	if tr.Status(u) != Up {
		t.Fatalf("two rounds → up, got %s", tr.Status(u))
	}
}

func TestRunReturnsWhenDisabled(t *testing.T) {
	New(Config{Enabled: false}, "", nil).Run(context.Background()) // must return immediately
}

func TestRunStopsOnContextCancel(t *testing.T) {
	tr := New(Config{Enabled: true, Interval: time.Hour, Path: "", Timeout: time.Second}, "", nil)
	tr.SetDialer(testDialer())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on context cancel")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/health/ -run 'TestCheckOnce|TestRun' -v`
Expected: FAIL — undefined `checkOnce` / `Run`.

- [ ] **Step 3: Implement the loop**

Append to `internal/health/checker.go`:

```go
// Run probes all targets every interval until ctx is cancelled. A disabled
// tracker returns immediately. The first round runs eagerly (no initial delay).
func (t *Tracker) Run(ctx context.Context) {
	if !t.cfg.Enabled {
		return
	}
	interval := t.cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/health/ -race -v`
Expected: PASS (no data races).

- [ ] **Step 5: Commit**

```bash
git add internal/health/checker.go internal/health/checker_test.go
git commit -m "feat(health): background probe loop (Run/checkOnce)"
```

---

## Task 7: Expose the proxy's safe dialer

**Files:**
- Modify: `internal/proxy/server.go`
- Test: `internal/proxy/server_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/proxy/server_test.go`:

```go
func TestServerDialerSetAfterApply(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: freePort(t), HTTPPort: 0, DialTimeout: time.Second},
		upstream.NewPolicy(nil), resolver, DefaultLimits(), discardLogger())
	defer s.Shutdown(context.Background())

	if s.Dialer() != nil {
		t.Fatal("dialer must be nil before Apply")
	}
	s.Apply(&store.State{})
	if s.Dialer() == nil {
		t.Fatal("dialer must be set after Apply")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/proxy/ -run TestServerDialerSetAfterApply -v`
Expected: FAIL — `s.Dialer` undefined.

- [ ] **Step 3: Implement the accessor**

In `internal/proxy/server.go`, add a field to `Server` (next to `mgmt atomic.Pointer[http.Handler]`):

```go
	dialer atomic.Pointer[upstream.Dialer] // the safe dialer from the latest Apply (shared with the health checker)
```

In `Apply`, right after the dialer is constructed (`dialer := upstream.New(...)`), publish it:

```go
	s.dialer.Store(dialer)
```

Add the accessor near `ActivePorts`:

```go
// Dialer returns the safe dialer from the most recent Apply (nil before the first
// Apply). The health checker probes through this exact instance so proxy and
// health share one SSRF/loop policy + self-guard (§8, §15).
func (s *Server) Dialer() *upstream.Dialer { return s.dialer.Load() }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/proxy/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/server.go internal/proxy/server_test.go
git commit -m "feat(proxy): expose Server.Dialer() for the shared health checker"
```

---

## Task 8: Health field on API responses

**Files:**
- Create: `internal/api/views.go`
- Modify: `internal/api/api.go`, `internal/api/lbs.go`, `internal/api/mappings.go`, `internal/api/misc.go`
- Modify (test helper): `internal/api/lbs_test.go`
- Test: `internal/api/views_test.go`

- [ ] **Step 1: Write the failing test**

First, refactor the shared test helper in `internal/api/lbs_test.go` so tests can inject a health reporter. Add `"github.com/realgo/rgdevenv/internal/health"` to its imports, then replace `newAPITestHandler`:

```go
func newAPITestHandler(t *testing.T) *Handler { return newAPITestHandlerWith(t, nil) }

func newAPITestHandlerWith(t *testing.T, reporter health.Reporter) *Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := txn.New(st, func(*store.State) {}, func(string) bool { return true }, upstream.NewPolicy(nil),
		txn.Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"})
	return New(Deps{
		Txn: m, Auth: auth.NewAuthenticator(testToken), Limiter: auth.NewRateLimiter(1000, time.Minute),
		CADir: t.TempDir(), Version: "test", HTTPSPort: 443, HTTPPort: 80, PoolStart: 9000, PoolEnd: 9999,
		ActivePorts: func() []int { return []int{443} }, Logger: discardLogger(), Health: reporter,
	})
}
```

Then create `internal/api/views_test.go`:

```go
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
)

type fakeHealth struct{ s health.Status }

func (f fakeHealth) Status(store.Upstream) health.Status { return f.s }
func (f fakeHealth) List() []health.Entry {
	return []health.Entry{{Scheme: "http", Host: "localhost", Port: 9011, Health: f.s}}
}

func addMapping(t *testing.T, h *Handler, lb string) {
	t.Helper()
	body := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/"+lb+"/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("add mapping: %d %s", w.Code, w.Body)
	}
}

func TestLBHealthDefaultsUnknown(t *testing.T) {
	h := newAPITestHandler(t) // no reporter → noop → unknown
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	addMapping(t, h, "rg-1.sean.realgo.com")

	w := do(h, authReq("GET", "/api/v1/lbs", ""))
	if !strings.Contains(w.Body.String(), `"health":"unknown"`) {
		t.Fatalf("expected per-mapping health unknown, got %s", w.Body)
	}
}

func TestLBAndStatusHealthFromReporter(t *testing.T) {
	h := newAPITestHandlerWith(t, fakeHealth{health.Up})
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	addMapping(t, h, "rg-1.sean.realgo.com")

	w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", ""))
	if !strings.Contains(w.Body.String(), `"health":"up"`) {
		t.Fatalf("expected mapping health up, got %s", w.Body)
	}
	w = do(h, authReq("GET", "/api/v1/status", ""))
	if !strings.Contains(w.Body.String(), `"upstreams"`) || !strings.Contains(w.Body.String(), `"health":"up"`) {
		t.Fatalf("expected status upstreams with health up, got %s", w.Body)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'TestLBHealth|TestLBAndStatus' -v`
Expected: FAIL — `Deps.Health` undefined; no `"health"` field in responses.

- [ ] **Step 3: Implement the views and wire the reporter**

Create `internal/api/views.go`:

```go
package api

import (
	"time"

	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
)

// mappingView is a store.Mapping plus its resolved health (§12 "with health").
type mappingView struct {
	ListenPort    int            `json:"listen_port"`
	ListenTLS     bool           `json:"listen_tls"`
	Upstream      store.Upstream `json:"upstream"`
	AllocationID  string         `json:"allocation_id,omitempty"`
	AutoAllocated bool           `json:"auto_allocated,omitempty"`
	Health        health.Status  `json:"health"`
}

// lbView is a store.LoadBalancer whose mappings carry health.
type lbView struct {
	Name      string        `json:"name"`
	Label     string        `json:"label,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	Mappings  []mappingView `json:"mappings"`
}

func (h *Handler) toMappingView(m store.Mapping) mappingView {
	return mappingView{
		ListenPort: m.ListenPort, ListenTLS: m.ListenTLS, Upstream: m.Upstream,
		AllocationID: m.AllocationID, AutoAllocated: m.AutoAllocated,
		Health: h.health.Status(m.Upstream),
	}
}

func (h *Handler) toLBView(lb store.LoadBalancer) lbView {
	ms := make([]mappingView, 0, len(lb.Mappings))
	for _, m := range lb.Mappings {
		ms = append(ms, h.toMappingView(m))
	}
	return lbView{Name: lb.Name, Label: lb.Label, CreatedAt: lb.CreatedAt, Mappings: ms}
}

// noopHealth is the default reporter when none is injected (tests, health off).
type noopHealth struct{}

func (noopHealth) Status(store.Upstream) health.Status { return health.Unknown }
func (noopHealth) List() []health.Entry                { return nil }
```

In `internal/api/api.go`: add `"github.com/realgo/rgdevenv/internal/health"` to imports; add `Health health.Reporter` to `Deps`; add `health health.Reporter` to `Handler`; in `New`, default + copy it:

```go
	if d.Health == nil {
		d.Health = noopHealth{}
	}
```
and in the `&Handler{...}` literal add `health: d.Health,`.

In `internal/api/lbs.go`, return views. Replace the four handler bodies' write sites:

- `listLBs`:
```go
func (h *Handler) listLBs(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	out := make([]lbView, 0, len(snap.LoadBalancers))
	for _, lb := range snap.LoadBalancers {
		out = append(out, h.toLBView(lb))
	}
	writeJSON(w, http.StatusOK, out)
}
```
- `createLB` final line: `writeJSON(w, http.StatusCreated, h.toLBView(*lbByName(st, cn)))`
- `getLB` (after the nil check): `writeJSON(w, http.StatusOK, h.toLBView(*lb))`
- `patchLB` final line: `writeJSON(w, http.StatusOK, h.toLBView(*lbByName(st, cn)))`

(`lbByName` is unchanged and still returns `*store.LoadBalancer`; the success paths guarantee non-nil.)

In `internal/api/mappings.go`, return mapping views:
- `createMapping` final line: `writeJSON(w, http.StatusCreated, h.toMappingView(*mappingInLB(st, cn, port)))`
- `putMapping` final line: `writeJSON(w, http.StatusOK, h.toMappingView(*mappingInLB(st, cn, port)))`

In `internal/api/misc.go`, add the upstreams detail (§17). Add `"github.com/realgo/rgdevenv/internal/health"` to imports, add a field to `statusResp`:

```go
	Upstreams []health.Entry `json:"upstreams"`
```
and in `status`, build it before the write:

```go
	ups := h.health.List()
	if ups == nil {
		ups = []health.Entry{}
	}
```
then add `Upstreams: ups,` to the `statusResp{...}` literal.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -v`
Expected: PASS. (Existing tests unmarshal into `store.LoadBalancer`/`store.Mapping` with plain `json.Unmarshal`, which silently ignores the added `health` key, so they remain green.)

- [ ] **Step 5: Commit**

```bash
git add internal/api/views.go internal/api/api.go internal/api/lbs.go internal/api/mappings.go internal/api/misc.go internal/api/lbs_test.go internal/api/views_test.go
git commit -m "feat(api): per-mapping health on /lbs and upstream health on /status"
```

---

## Task 9: Live upstream-failure feed

**Files:**
- Modify: `internal/proxy/reverseproxy.go`, `internal/proxy/router.go`, `internal/proxy/server.go`
- Modify: `internal/proxy/reverseproxy_test.go` (2 existing call sites + 1 new test)
- Modify: `internal/health/checker.go` (add `RecordFailure`)
- Test: `internal/health/checker_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/proxy/reverseproxy_test.go`, **update the two existing `BuildReverseProxy` calls** to pass a trailing `nil`:

```go
	rp, err := BuildReverseProxy(up, true /*listenTLS*/, dialer, "", DefaultLimits(), discardLogger(), nil)
```
```go
	rp, err := BuildReverseProxy(up, true, dialer, "", DefaultLimits(), discardLogger(), nil)
```

Then add a new test:

```go
func TestReverseProxyInvokesErrorObserver(t *testing.T) {
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	up := store.Upstream{Scheme: "http", Host: "blocked.example.com", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}}
	called := make(chan struct{}, 1)
	rp, err := BuildReverseProxy(up, true, dialer, "", DefaultLimits(), discardLogger(), func() { called <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(rp)
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("error observer was not invoked on upstream failure")
	}
}
```

In `internal/health/checker_test.go`, add:

```go
func TestRecordFailureFeedsHysteresis(t *testing.T) {
	tr := New(Config{Threshold: 1}, "", nil)
	u := up("localhost", 9000)
	tr.RecordFailure(u)
	if tr.Status(u) != Down {
		t.Fatalf("RecordFailure → down, got %s", tr.Status(u))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/proxy/ ./internal/health/ -run 'TestReverseProxyInvokesErrorObserver|TestRecordFailure' -v`
Expected: FAIL — `BuildReverseProxy` arity mismatch / `RecordFailure` undefined.

- [ ] **Step 3: Implement the feed**

In `internal/proxy/reverseproxy.go`, change the signature and `ErrorHandler`:

```go
func BuildReverseProxy(up store.Upstream, listenTLS bool, dialer *upstream.Dialer, caDir string, limits Limits, logger *slog.Logger, onError func()) (*httputil.ReverseProxy, error) {
```
and in the `ErrorHandler` closure, after the existing `logger.Warn(...)` and before `writeBadGateway(w)`:

```go
			if onError != nil {
				onError()
			}
```

In `internal/proxy/router.go`, add to `RouteDeps`:

```go
	OnUpstreamError func(store.Upstream) // optional: live-failure feed for health (§17)
```
and in `BuildRoutingTable`, replace the `BuildReverseProxy` call with a per-upstream-bound closure:

```go
			var onErr func()
			if deps.OnUpstreamError != nil {
				up := m.Upstream
				onErr = func() { deps.OnUpstreamError(up) }
			}
			h, err := BuildReverseProxy(m.Upstream, m.ListenTLS, deps.Dialer, deps.CADir, deps.Limits, deps.Logger, onErr)
```

In `internal/proxy/server.go`, add a field to `Server`:

```go
	onUpstreamErr func(store.Upstream) // set once before serving (SetUpstreamErrorObserver)
```
add the setter near `SetManagementHandler`:

```go
// SetUpstreamErrorObserver installs a callback invoked when a live proxy request
// to an upstream fails; the health checker uses it to feed status (§17). Set it
// before serving.
func (s *Server) SetUpstreamErrorObserver(f func(store.Upstream)) { s.onUpstreamErr = f }
```
and in `Apply`, add `OnUpstreamError: s.onUpstreamErr,` to the `RouteDeps{...}` literal.

In `internal/health/checker.go`, add:

```go
// RecordFailure feeds a single unhealthy sample for up's identity (the live
// proxy-failure feed, §17). Subject to the same hysteresis as active probes.
func (t *Tracker) RecordFailure(up store.Upstream) { t.record(IdentityOf(up), false) }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/proxy/ ./internal/health/ -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/reverseproxy.go internal/proxy/router.go internal/proxy/server.go internal/proxy/reverseproxy_test.go internal/health/checker.go internal/health/checker_test.go
git commit -m "feat(proxy,health): feed live upstream failures into health status"
```

---

## Task 10: Wire the health checker into the daemon

**Files:**
- Modify: `cmd/rgdevenv/serve.go`
- Modify: `cmd/rgdevenv/serve_test.go`, `cmd/rgdevenv/integration_test.go` (call sites)
- Test: `cmd/rgdevenv/health_e2e_test.go`

- [ ] **Step 1: Write the failing test**

First, update the two existing `setupServer` call sites for the new 5-value signature.

In `cmd/rgdevenv/serve_test.go` line ~102:
```go
	srv, st, _, _, err := setupServer(cfg, logger)
```
In `cmd/rgdevenv/integration_test.go` line ~38:
```go
	srv, st, _, _, err := setupServer(cfg, logger)
```

Then create `cmd/rgdevenv/health_e2e_test.go`:

```go
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

func TestHealthEndToEnd(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer backend.Close()
	bhost, bportStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	bport, _ := strconv.Atoi(bportStr)

	certFile, keyFile := writeMainTestCert(t)
	httpsPort := freeTCPPort(t)
	const token = "0123456789abcdef0123456789abcdef"
	t.Setenv("RGDEVENV_TOKEN", token)

	cfg := &config.Config{
		BindAddr:           "127.0.0.1",
		HTTPSPort:          httpsPort,
		HTTPPort:           0,
		CertFile:           certFile,
		KeyFile:            keyFile,
		ManagementHostname: "rgdevenv.sean.realgo.com",
		CADir:              t.TempDir(),
		StateFile:          filepath.Join(t.TempDir(), "state.json"),
		PortPool:           config.PortPoolConfig{Start: 9000, End: 9999},
		Management:         config.ManagementConfig{AuthRateLimitPerMin: 1000},
		Health:             config.HealthConfig{Enabled: true, IntervalSeconds: 1, TimeoutSeconds: 2, Path: "/", Threshold: 1},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, _, tracker, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	applyAndTrack(srv, tracker, st.Snapshot(), logger)
	defer srv.Shutdown(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Run(ctx)
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	call := func(method, path, body string) (int, string) {
		var r *http.Request
		if body != "" {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, strings.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := call("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`); code != http.StatusCreated {
		t.Fatalf("create lb: %d %s", code, body)
	}
	mbody := fmt.Sprintf(`{"listen_port":%d,"listen_tls":true,"upstream":{"scheme":"http","host":%q,"port":%d,"tls":{"mode":"verify"}}}`, httpsPort, bhost, bport)
	if code, body := call("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", mbody); code != http.StatusCreated {
		t.Fatalf("create mapping: %d %s", code, body)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if code, body := call("GET", "/api/v1/status", ""); code == 200 && strings.Contains(body, `"health":"up"`) {
			return // success: the live upstream is reported healthy
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("upstream never reported healthy in /status")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/rgdevenv/ -run TestHealthEndToEnd -v`
Expected: FAIL — `setupServer` returns 4 values (arity mismatch) / `applyAndTrack` undefined.

- [ ] **Step 3: Implement the wiring**

In `cmd/rgdevenv/serve.go`, add `"github.com/realgo/rgdevenv/internal/health"` to the imports.

Change `setupServer`'s signature and body. New signature:

```go
func setupServer(cfg *config.Config, logger *slog.Logger) (*proxy.Server, *store.Store, http.Handler, *health.Tracker, error) {
```
Update every early `return nil, nil, nil, err` in `setupServer` to `return nil, nil, nil, nil, err`.

Construct the tracker after `srv` is built and before the txn manager (so the apply closure can capture it):

```go
	tracker := health.New(health.Config{
		Enabled:   cfg.Health.Enabled,
		Interval:  cfg.HealthInterval(),
		Timeout:   cfg.HealthTimeout(),
		Path:      cfg.Health.Path,
		Threshold: cfg.Health.Threshold,
	}, cfg.CADir, logger)
	srv.SetUpstreamErrorObserver(tracker.RecordFailure)
```

Replace the txn `apply` closure so it also refreshes the tracker:

```go
	mgr := txn.New(st, func(state *store.State) {
		applyAndTrack(srv, tracker, state, logger)
	}, resolver.Covers, upstream.NewPolicy(cfg.Upstreams.Allow), txn.Config{
		PoolStart:    cfg.PortPool.Start,
		PoolEnd:      cfg.PortPool.End,
		HTTPSPort:    cfg.HTTPSPort,
		HTTPPort:     cfg.HTTPPort,
		MgmtBindPort: cfg.MgmtBindPort(),
		MgmtHost:     cfg.ManagementHostname,
		CADir:        cfg.CADir,
	})
```

Add `Health: tracker,` to the `api.New(api.Deps{...})` literal. Change the final `return`:

```go
	return srv, st, mgmtHandler, tracker, nil
```

Add the shared helper (e.g. just below `setupServer`):

```go
// applyAndTrack reconfigures the proxy from state and refreshes the health
// checker's dialer + target set so both stay consistent after every change.
func applyAndTrack(srv *proxy.Server, tracker *health.Tracker, state *store.State, logger *slog.Logger) {
	for _, d := range srv.Apply(state) {
		logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
	}
	tracker.SetDialer(srv.Dialer())
	tracker.SetTargets(health.IdentitiesFrom(state))
}
```

In `runServe`, capture the tracker, replace the startup `Apply` loop with `applyAndTrack`, and run the checker for the daemon's lifetime:

```go
	srv, st, mgmtHandler, tracker, err := setupServer(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	applyAndTrack(srv, tracker, st.Snapshot(), logger)
	healthCtx, cancelHealth := context.WithCancel(context.Background())
	defer cancelHealth()
	go tracker.Run(healthCtx)
```
(Delete the old `for _, d := range srv.Apply(st.Snapshot()) { ... }` block it replaces.)

Pass the tracker to `runSignals` (last line of `runServe`):

```go
	return runSignals(configPath, srv, st, tracker, mgmtBind, logger, levelVar)
```

Update `runSignals`'s signature and its SIGHUP `Apply` loop:

```go
func runSignals(configPath string, srv *proxy.Server, st *store.Store, tracker *health.Tracker, mgmtBind *http.Server, logger *slog.Logger, levelVar *slog.LevelVar) error {
```
and replace the post-reload `for _, d := range srv.Apply(st.Snapshot()) { ... }` block with:

```go
			applyAndTrack(srv, tracker, st.Snapshot(), logger)
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./cmd/rgdevenv/ -race -v`
Expected: PASS (`TestSetupServerWiring`, `TestManagementAPIEndToEnd`, and `TestHealthEndToEnd`).

- [ ] **Step 5: Commit**

```bash
git add cmd/rgdevenv/serve.go cmd/rgdevenv/serve_test.go cmd/rgdevenv/integration_test.go cmd/rgdevenv/health_e2e_test.go
git commit -m "feat(serve): run the health checker and refresh it on every Apply"
```

---

## Final verification

- [ ] **Whole-module gate**

Run:
```bash
go build ./... && go test ./... -race 2>&1 | grep -E '^(ok|FAIL)' && go vet ./... && (golangci-lint run >/dev/null 2>&1 && echo "lint 0 issues") && test -z "$(gofmt -l .)" && echo "ALL GREEN"
```
Expected: every package `ok`, `lint 0 issues`, `ALL GREEN`.

- [ ] **Update the config sample (docs).** Add a `[health]` block to the §9 example in `docs/superpowers/specs/2026-06-13-rgdevenv-design.md`? **No** — do not edit the approved spec. Instead, if a sample `config.toml` exists in the repo, add the block there; otherwise skip. (The defaults live in `config.Default()`.)

---

## Spec coverage self-check (filled in during planning)

- §17 protocol-aware checks (HTTP(S) + TCP fallback) → Tasks 5/6. ✅
- §17 hysteresis (N consecutive) → Task 3. ✅
- §17 live proxy failures feed status → Task 9. ✅
- §17 results in /status → Task 8 (`upstreams[]`); §12 /lbs "with health" → Task 8 (per-mapping `health`). ✅
- §8/§15 shared safe dialer, validated-IP pinning, no redirect to denied targets → Tasks 5 (probe via `*upstream.Dialer`, `ErrUseLastResponse`) + 7 (`Server.Dialer()`). ✅
- §17 configurable interval/path + threshold → Task 1. ✅
- §19 tests: health unit (identity/hysteresis/probe), API health fields, end-to-end → Tasks 2/3/5/6/8/10. ✅

---

## What Phase 2c / 2d add (not in this plan)

- **Phase 2c — CLI:** `internal/client` REST client + `lb`/`map`/`port`/`ca`/`status` cobra subcommands (`--upstream` URL parsing, `--allocate`, human tables + `--json`), client config (`RGDEVENV_API`/`RGDEVENV_TOKEN`/`~/.config/rgdevenv/cli.toml`). Independent of health; consumes the Phase 2a/2b API. Gets its own plan.
- **Phase 2d — Web UI:** `internal/ui` embedded `html/template` + vanilla JS static login shell and dashboard mounted at `/`; its health dot consumes this phase's `health` field. Gets its own plan.
