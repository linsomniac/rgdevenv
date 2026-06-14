# rgdevenv — Phase 2a: Management API Core — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the token-protected REST management API that mutates rgdevenv state through a single serialized staged transaction and republishes the live proxy — full CRUD for load balancers, mappings, and port allocations, served at the management hostname (and an optional loopback/unix management bind).

**Architecture:** A `txn.Manager` serializes every mutation: snapshot → deep-clone → build → validate (structural + config-dependent) → atomic persist (commit point) → publish snapshot → `proxy.Server.Apply` (reconcile listeners + republish routes; bind failures degrade, never abort) — all under one lock. An `api` package exposes REST handlers behind a bearer-auth + rate-limit middleware; the only unauthenticated surfaces are `/healthz` and (in Phase 2b) the static login shell. The management handler is injected into the existing `proxy.Server` at its `MgmtHost` dispatch seam and, optionally, on a separate loopback/unix plaintext bind.

**Tech Stack:** Go 1.22+ stdlib (`net/http`, `crypto/subtle`, `crypto/rand`, `log/slog`), building on the merged Phase 1 packages. No new external dependencies.

---

## Plan scope (Phase 2a of the management plane)

Builds on the **merged Phase 1** foundations on `master` (canon, config, store, registry/Reconcile, upstream/{policy,ca,dialer}, proxy/{tls,reverseproxy,router,redirect,listeners,server}, cmd serve). Implements spec
(`docs/superpowers/specs/2026-06-13-rgdevenv-design.md`) sections: **§11** (registry allocate/return), **§12** (REST API), **§15/§8** (bearer auth + rate limit), **§16** (staged transaction), and the management-routing parts of **§6**.

**This plan delivers:** a running `rgdevenv serve` whose management hostname (and optional `management.bind`) serves `/healthz` (unauthenticated) and a bearer-protected `/api/v1/*` REST API providing full CRUD over LBs/mappings/ports, with every mutation validated and atomically applied to the live proxy.

**Deferred to Phase 2b** (do NOT build here): `internal/health` (protocol-aware checks; until then `/status` and list endpoints report health as `"unknown"`), `internal/ui` (static login shell + dashboard; the management `/` path returns a minimal placeholder for now), and `internal/client` + the `lb`/`map`/`port`/`ca`/`status` CLI subcommands. Phase 2b gets its own plan.

Spec section references (e.g. *§12*) point into the design spec.

---

## What Phase 1 already provides (do not reimplement)

- `store`: `State`, `LoadBalancer`, `Mapping`, `Upstream`, `UpstreamTLS`, `PortAllocation`, `CurrentVersion`; `Store` with `Open/Snapshot/Publish/Save/Close`; `Validate(*State) error` (structural invariants).
- `registry`: `Reconcile(*State) ([]PortAllocation, bool)`.
- `canon`: `Host(string) (string, error)`.
- `config`: `Config` (incl. `ManagementHostname`, `TokenFile`, `Management.Bind`, `Management.AuthRateLimitPerMin`, `PortPool.{Start,End}`, `Upstreams.Allow`, `CADir`, `HTTPSPort`, `HTTPPort`), `Load`, `AllCertPairs`.
- `upstream`: `Policy` (`NewPolicy`, `AllowedHost`, `CheckDialIP`), `SelfGuard`, `Dialer`, `LoadCA`, `ValidCAName`.
- `proxy`: `Server` (`NewServer`, `Apply(*store.State) []Degraded`, `Shutdown`, `Resolver() *CertResolver`), `CertResolver.Covers(string) bool`, `Degraded`, `DefaultLimits`.
- `cmd/rgdevenv`: `serve.go` with `runServe`, `setupServer(cfg,*slog.Logger) (*proxy.Server, *store.Store, error)`, `runSignals`.

## Post-execution revisions

Executed via subagent-driven development with per-task spec + code-quality review.
Deviations from the task text above (the committed code is the source of truth):

- **`decodeJSON` tolerates an empty body** (`io.EOF` → empty object) so optional-body
  POSTs like `/ports/allocate` succeed with no body; required-field checks still run.
- **`txn.Validate` also range-checks** `listen_port` and `upstream.port` (1–65535),
  beyond the plan's rules.
- **`api.writeErr` logs any 5xx** (covers a future unmapped `txn.Error` kind, not
  only non-txn errors).
- **`api.listCAs` returns a generic error** ("CA directory is unavailable") and logs
  the detail, rather than echoing the OS error (which contained the CA-dir path).
- **serve shutdown drains the management bind concurrently** with the proxy (so a
  wedged bind can't starve the 30s drain); the management-bind `http.Server` also
  sets `IdleTimeout`/`MaxHeaderBytes`.
- Many tasks gained **extra review-driven tests** (PATCH/DELETE-missing → 404,
  good-token-from-rate-limited-IP → 429, replace+allocate cascade, port-range,
  invalid-tls-mode, mgmt-bind-port conflict, empty-body allocate, etc.).

**Note for Phase 2b:** `GET /lbs` and `GET /status` currently OMIT per-mapping health
fields (the plan said they'd report `"unknown"`); Phase 2b adds real health, so the
2b author should introduce the `health` field on those responses then.

---

## File structure (Phase 2a)

```
internal/store/
  clone.go             State.Clone deep copy (for the transaction)
  clone_test.go
internal/registry/
  allocate.go          Allocate (lowest-free) + Return (+ in-use check)
  allocate_test.go
internal/auth/
  token.go             LoadToken (file/env, >=256-bit) + Authenticator (constant-time)
  ratelimit.go         per-source-IP failed-attempt limiter
  auth_test.go
internal/txn/
  txn.go               Manager: serialized snapshot→clone→build→validate→save→publish→apply
  validate.go          config-dependent validation (pool, policy, CA, coverage, reserved, PUT)
  errors.go            typed errors (ErrValidation/ErrConflict/ErrNotFound) → HTTP status
  txn_test.go
  validate_test.go
internal/api/
  api.go               Handler (mux) + JSON helpers + error mapping
  middleware.go        bearer auth + rate-limit + audit log
  lbs.go               /lbs CRUD
  mappings.go          /lbs/{name}/mappings CRUD (+ allocate convenience)
  ports.go             /ports list/allocate/return
  misc.go              /cas, /status, /healthz
  *_test.go
internal/proxy/
  server.go            MODIFY: add SetManagementHandler + serve it at the MgmtHost seam
  server_test.go       MODIFY: add a management-handler dispatch test
cmd/rgdevenv/
  serve.go             MODIFY: build auth/txn/api, wire mgmt handler + optional management.bind
  serve_test.go        MODIFY: end-to-end management-API wiring test
```

**Module path:** `github.com/realgo/rgdevenv`.

## Conventions

- TDD: failing test → watch fail → minimal code → watch pass → commit. One unit per task.
- `gofmt`/`goimports`; `go vet ./...`, `go test ./... -race`, and `golangci-lint run` (govet+staticcheck) must pass before each commit.
- `AIDEV-NOTE:` for subtle/security code (constant-time compare, commit ordering, validation rules). Never remove existing `AIDEV-` comments.
- Commit per task; end every commit message with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Boring over clever. YAGNI: do not build Phase-2b packages (health/ui/client/CLI).
- **Layering:** `txn` may import `store`, `upstream`, `proxy`. `api` may import `txn`, `store`, `proxy`, `auth`, `config`. `proxy` must NOT import `txn`/`api`/`auth` (no cycles).

---

## Task 1: Deep-clone state (`internal/store/clone.go`)

The transaction mutates a **copy** of the published snapshot, never the live one (§16). `State.Clone` is a full deep copy.

**Files:**
- Create: `internal/store/clone.go`
- Test: `internal/store/clone_test.go`

- [ ] **Step 1: Write the failing test** — `internal/store/clone_test.go`

```go
package store

import (
	"testing"
	"time"
)

func TestStateCloneIsDeep(t *testing.T) {
	orig := &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name:      "a.example.com",
			Label:     "demo",
			CreatedAt: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
			Mappings: []Mapping{{
				ListenPort: 443, ListenTLS: true, AllocationID: "alloc-1",
				Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}},
			}},
		}},
		PortAllocations: []PortAllocation{{ID: "alloc-1", Port: 9011, Owner: "a.example.com", Auto: true}},
	}
	clone := orig.Clone()

	// Mutating the clone must not touch the original.
	clone.LoadBalancers[0].Name = "changed"
	clone.LoadBalancers[0].Mappings[0].ListenPort = 8443
	clone.LoadBalancers[0].Mappings[0].Upstream.Host = "evil"
	clone.PortAllocations[0].Port = 1

	if orig.LoadBalancers[0].Name != "a.example.com" {
		t.Fatal("clone shares LoadBalancer header")
	}
	if orig.LoadBalancers[0].Mappings[0].ListenPort != 443 {
		t.Fatal("clone shares Mappings slice")
	}
	if orig.LoadBalancers[0].Mappings[0].Upstream.Host != "localhost" {
		t.Fatal("clone shares Upstream")
	}
	if orig.PortAllocations[0].Port != 9011 {
		t.Fatal("clone shares PortAllocations slice")
	}
}

func TestStateCloneNil(t *testing.T) {
	var s *State
	if s.Clone() != nil {
		t.Fatal("nil clone should be nil")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestStateClone -v`
Expected: FAIL — `clone.Clone undefined`.

- [ ] **Step 3: Write the implementation** — `internal/store/clone.go`

```go
package store

// Clone returns a deep copy of s so a transaction can mutate the copy without
// touching the published (immutable) snapshot (§16). Returns nil for a nil state.
//
// AIDEV-NOTE: every slice must be copied; structs without reference fields copy
// by value. If you add a slice/map/pointer field to the model, copy it here too.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	out := &State{Version: s.Version}
	if s.LoadBalancers != nil {
		out.LoadBalancers = make([]LoadBalancer, len(s.LoadBalancers))
		for i, lb := range s.LoadBalancers {
			nlb := lb // copies scalar/time fields
			if lb.Mappings != nil {
				nlb.Mappings = make([]Mapping, len(lb.Mappings))
				copy(nlb.Mappings, lb.Mappings) // Mapping has no reference fields
			}
			out.LoadBalancers[i] = nlb
		}
	}
	if s.PortAllocations != nil {
		out.PortAllocations = make([]PortAllocation, len(s.PortAllocations))
		copy(out.PortAllocations, s.PortAllocations)
	}
	return out
}
```

> Note: `Mapping`/`Upstream`/`UpstreamTLS`/`PortAllocation` contain only value-type fields (ints, strings, bools, `time.Time`), so `copy` of the slices is a true deep copy. The AIDEV-NOTE guards against future reference-typed fields.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestStateClone -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/clone.go internal/store/clone_test.go
git commit -m "feat(store): deep Clone for transactional copies

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Port allocate/return (`internal/registry/allocate.go`)

Allocate the lowest free port in `[start,end]` (exhaustion → error); return a port (in-use → error) (§11). Pure functions over the allocation slice + the state.

**Files:**
- Create: `internal/registry/allocate.go`
- Test: `internal/registry/allocate_test.go`

- [ ] **Step 1: Write the failing test** — `internal/registry/allocate_test.go`

```go
package registry

import (
	"errors"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestAllocateLowestFree(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9002}}
	got, err := Allocate(allocs, 9000, 9999, "id-x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9001 || got.ID != "id-x" {
		t.Fatalf("expected lowest free 9001/id-x, got %+v", got)
	}
}

func TestAllocateExhausted(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9001}}
	_, err := Allocate(allocs, 9000, 9001, "id-x")
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestReturnFreesPort(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}, {ID: "b", Port: 9001}}
	got, err := Return(allocs, 9000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Port != 9001 {
		t.Fatalf("expected only 9001 to remain, got %+v", got)
	}
}

func TestReturnUnknownPort(t *testing.T) {
	allocs := []store.PortAllocation{{ID: "a", Port: 9000}}
	if _, err := Return(allocs, 9999); !errors.Is(err, ErrNotAllocated) {
		t.Fatalf("expected ErrNotAllocated, got %v", err)
	}
}

func TestIsPortReferenced(t *testing.T) {
	st := &store.State{
		LoadBalancers: []store.LoadBalancer{{
			Name:     "x",
			Mappings: []store.Mapping{{ListenPort: 443, AllocationID: "a"}},
		}},
		PortAllocations: []store.PortAllocation{{ID: "a", Port: 9000}},
	}
	if !IsAllocationReferenced(st, "a") {
		t.Fatal("alloc a is referenced by a mapping")
	}
	if IsAllocationReferenced(st, "b") {
		t.Fatal("alloc b is not referenced")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -run 'TestAllocate|TestReturn|TestIsPort' -v`
Expected: FAIL — `undefined: Allocate`.

- [ ] **Step 3: Write the implementation** — `internal/registry/allocate.go`

```go
package registry

import (
	"errors"

	"github.com/realgo/rgdevenv/internal/store"
)

// ErrPoolExhausted is returned when no free port remains in the pool.
var ErrPoolExhausted = errors.New("registry: port pool exhausted")

// ErrNotAllocated is returned when returning a port that is not allocated.
var ErrNotAllocated = errors.New("registry: port not allocated")

// Allocate reserves the lowest free port in [start,end] and returns a new
// PortAllocation with the given id.
//
// AIDEV-NOTE: the caller (txn) sets Owner/Label/Auto and stamps AllocatedAt — the
// clock is injected at the txn layer so allocation is a pure, testable function.
func Allocate(allocs []store.PortAllocation, start, end int, id string) (store.PortAllocation, error) {
	used := make(map[int]bool, len(allocs))
	for _, a := range allocs {
		used[a.Port] = true
	}
	for p := start; p <= end; p++ {
		if !used[p] {
			return store.PortAllocation{ID: id, Port: p}, nil
		}
	}
	return store.PortAllocation{}, ErrPoolExhausted
}

// Return removes the allocation for the given port. Callers must first confirm
// the port is not referenced by a mapping (IsAllocationReferenced). Returns the
// trimmed slice, or ErrNotAllocated if the port is not present.
func Return(allocs []store.PortAllocation, port int) ([]store.PortAllocation, error) {
	out := make([]store.PortAllocation, 0, len(allocs))
	found := false
	for _, a := range allocs {
		if a.Port == port {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return nil, ErrNotAllocated
	}
	return out, nil
}

// IsAllocationReferenced reports whether any mapping references allocation id.
func IsAllocationReferenced(st *store.State, id string) bool {
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID == id {
				return true
			}
		}
	}
	return false
}

// AllocationByPort returns a pointer to the allocation with the given port, or nil.
func AllocationByPort(allocs []store.PortAllocation, port int) *store.PortAllocation {
	for i := range allocs {
		if allocs[i].Port == port {
			return &allocs[i]
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/registry/ -v`
Expected: PASS (Allocate/Return/IsAllocationReferenced + the existing Reconcile tests).

- [ ] **Step 5: Commit**

```bash
git add internal/registry/allocate.go internal/registry/allocate_test.go
git commit -m "feat(registry): port allocate (lowest-free) and return

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Bearer token + constant-time auth (`internal/auth/token.go`)

Load the shared token (env or file, ≥256-bit), compare in constant time via a
fixed-length digest, and parse the `Authorization: Bearer` header (*§15*).

**Files:**
- Create: `internal/auth/token.go`
- Test: `internal/auth/auth_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/auth_test.go`

```go
package auth

import (
	"os"
	"path/filepath"
	"testing"
)

const goodToken = "0123456789abcdef0123456789abcdef" // 32 chars >= 256-bit

func TestAuthenticatorCheck(t *testing.T) {
	a := NewAuthenticator(goodToken)
	if !a.Check(goodToken) {
		t.Fatal("correct token rejected")
	}
	if a.Check("wrong") {
		t.Fatal("wrong (shorter) token accepted")
	}
	if a.Check(goodToken + "x") {
		t.Fatal("wrong (longer) token accepted")
	}
	if a.Check("") {
		t.Fatal("empty token accepted")
	}
}

func TestLoadTokenFromEnv(t *testing.T) {
	t.Setenv("RGDEVENV_TOKEN", "  "+goodToken+"  ") // trimmed
	tok, err := LoadToken("")
	if err != nil {
		t.Fatal(err)
	}
	if tok != goodToken {
		t.Fatalf("token = %q", tok)
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(goodToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadToken(p)
	if err != nil {
		t.Fatal(err)
	}
	if tok != goodToken {
		t.Fatalf("token = %q", tok)
	}
}

func TestLoadTokenTooShort(t *testing.T) {
	t.Setenv("RGDEVENV_TOKEN", "short")
	if _, err := LoadToken(""); err == nil {
		t.Fatal("expected error for short token")
	}
}

func TestLoadTokenMissing(t *testing.T) {
	if _, err := LoadToken(""); err == nil {
		t.Fatal("expected error when no token configured")
	}
}

func TestParseBearer(t *testing.T) {
	if tok, ok := ParseBearer("Bearer abc123"); !ok || tok != "abc123" {
		t.Fatalf("ParseBearer good = %q,%v", tok, ok)
	}
	if _, ok := ParseBearer("Basic abc"); ok {
		t.Fatal("ParseBearer should reject non-Bearer")
	}
	if _, ok := ParseBearer(""); ok {
		t.Fatal("ParseBearer should reject empty")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/auth/ -run 'TestAuthenticator|TestLoadToken|TestParseBearer' -v`
Expected: FAIL — `undefined: NewAuthenticator`.

- [ ] **Step 3: Write the implementation** — `internal/auth/token.go`

```go
// Package auth provides bearer-token authentication and per-source-IP rate
// limiting for the rgdevenv management plane (§15).
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"
)

// minTokenLen is a proxy for "≥256-bit": 32 ASCII chars (§15). The token is
// supplied by the operator; we cannot measure entropy, only enforce a floor.
const minTokenLen = 32

// Authenticator holds a fixed-length digest of the shared token and compares
// presented tokens in constant time.
//
// AIDEV-NOTE: compare SHA-256 digests (fixed 32 bytes), not raw strings —
// subtle.ConstantTimeCompare on unequal lengths returns early and would leak
// the token length. Hashing first makes the comparison length-independent.
type Authenticator struct {
	digest [32]byte
}

// NewAuthenticator builds an Authenticator for the given token.
func NewAuthenticator(token string) *Authenticator {
	return &Authenticator{digest: sha256.Sum256([]byte(token))}
}

// Check reports whether presented equals the configured token (constant time).
func (a *Authenticator) Check(presented string) bool {
	d := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(a.digest[:], d[:]) == 1
}

// LoadToken loads the token from RGDEVENV_TOKEN (preferred) or the token file,
// trims surrounding whitespace, and enforces the minimum length.
func LoadToken(tokenFile string) (string, error) {
	if v := os.Getenv("RGDEVENV_TOKEN"); strings.TrimSpace(v) != "" {
		return validateToken(strings.TrimSpace(v))
	}
	if tokenFile == "" {
		return "", errors.New("auth: no token configured (set RGDEVENV_TOKEN or token_file)")
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("auth: read token file: %w", err)
	}
	return validateToken(strings.TrimSpace(string(b)))
}

func validateToken(tok string) (string, error) {
	if len(tok) < minTokenLen {
		return "", fmt.Errorf("auth: token too short (%d chars; need >= %d / 256-bit)", len(tok), minTokenLen)
	}
	return tok, nil
}

// ParseBearer extracts the token from an "Authorization: Bearer <token>" header
// value. It returns ok=false for any other scheme or an empty value.
func ParseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/auth/ -run 'TestAuthenticator|TestLoadToken|TestParseBearer' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/token.go internal/auth/auth_test.go
git commit -m "feat(auth): bearer token load + constant-time check

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Per-source-IP rate limiter (`internal/auth/ratelimit.go`)

Count **failed** auth attempts per source IP in a sliding window; block (→ `429`)
once over the limit (*§15*).

**Files:**
- Create: `internal/auth/ratelimit.go`
- Modify: `internal/auth/auth_test.go` (append)

- [ ] **Step 1: Append the failing tests** — to `internal/auth/auth_test.go`

```go
import "time" // add to the existing import block

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	now := time.Unix(1_000_000, 0)
	rl.now = func() time.Time { return now }

	if !rl.Allowed("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	rl.RecordFailure("1.2.3.4")
	rl.RecordFailure("1.2.3.4")
	if rl.Allowed("1.2.3.4") {
		t.Fatal("should be blocked after 2 failures")
	}
	// A different IP is unaffected.
	if !rl.Allowed("5.6.7.8") {
		t.Fatal("other IP should be allowed")
	}
	// After the window passes, the IP is allowed again.
	now = now.Add(2 * time.Minute)
	if !rl.Allowed("1.2.3.4") {
		t.Fatal("should be allowed after the window elapses")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/auth/ -run TestRateLimiter -v`
Expected: FAIL — `undefined: NewRateLimiter`.

- [ ] **Step 3: Write the implementation** — `internal/auth/ratelimit.go`

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/auth/ -v -race`
Expected: PASS (token + rate-limit tests).

- [ ] **Step 5: Commit**

```bash
git add internal/auth/ratelimit.go internal/auth/auth_test.go
git commit -m "feat(auth): per-source-IP failed-attempt rate limiter

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Management handler hook in the proxy (`internal/proxy/server.go`)

Wire an injectable management `http.Handler` into the existing `MgmtHost` dispatch
seam (currently a 404 stub) (*§6*). Uses an `atomic.Pointer` so it can be set
before serving and read race-free per request.

**Files:**
- Modify: `internal/proxy/server.go`
- Modify: `internal/proxy/server_test.go` (append)

- [ ] **Step 1: Append the failing test** — to `internal/proxy/server_test.go`

```go
func TestDispatchServesManagementHandler(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	s.SetManagementHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "mgmt")
	}))

	req := httptest.NewRequest("GET", "https://rgdevenv.sean.realgo.com/api/v1/status", nil)
	req.Host = "rgdevenv.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "mgmt" {
		t.Fatalf("management handler not served: code=%d body=%q", w.Code, w.Body.String())
	}
}
```

(The existing `TestDispatchManagementHost404InPhase1` stays valid: with no handler
set, the management host still 404s.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestDispatchServesManagementHandler -v`
Expected: FAIL — `s.SetManagementHandler undefined`.

- [ ] **Step 3: Modify `internal/proxy/server.go`**

**3a.** Add a field to the `Server` struct (after `listeners *Listeners`):
```go
	mgmt atomic.Pointer[http.Handler] // optional management-plane handler (Phase 2)
```

**3b.** Add this method (e.g. right after `NewServer`):
```go
// SetManagementHandler installs the handler served at the management hostname
// (and on the optional management bind). Set it before serving; it is read
// race-free per request. A nil/unset handler makes the management host 404.
func (s *Server) SetManagementHandler(h http.Handler) {
	s.mgmt.Store(&h)
}
```

**3c.** In `dispatch`, replace the existing management-host seam block:
```go
		// AIDEV-TODO(phase2): when host == s.cfg.MgmtHost, serve the management
		// plane here (auth + REST API + web UI). For now it 404s like any host.
		if s.cfg.MgmtHost != "" && host == s.cfg.MgmtHost {
			writeNotFound(w)
			return
		}
```
with:
```go
		// Management hostname → the injected management handler (auth + REST API
		// + UI). If none is installed, 404 like any unknown host (§6).
		if s.cfg.MgmtHost != "" && host == s.cfg.MgmtHost {
			if hp := s.mgmt.Load(); hp != nil {
				(*hp).ServeHTTP(w, r)
				return
			}
			writeNotFound(w)
			return
		}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/proxy/ -v -race`
Expected: PASS (all proxy tests, including the new one and the existing 404 case).

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/server.go internal/proxy/server_test.go
git commit -m "feat(proxy): injectable management handler at the MgmtHost seam

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Transaction errors + validation (`internal/txn/errors.go`, `validate.go`)

Typed errors that map to HTTP status codes, and the config-dependent validation
that augments `store.Validate` (*§16* validate step): pool/reserved-port
disjointness, the HTTPS-port-is-TLS rule, upstream policy, scheme, upstream-TLS
mode + path-safe CA existence, certificate coverage, canonical + reserved name.

**Files:**
- Create: `internal/txn/errors.go`
- Create: `internal/txn/validate.go`
- Test: `internal/txn/validate_test.go`

- [ ] **Step 1: Write the failing tests** — `internal/txn/validate_test.go`

```go
package txn

import (
	"errors"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func baseParams() ValidateParams {
	return ValidateParams{
		Policy:       upstream.NewPolicy([]string{"build-box"}),
		Covers:       func(h string) bool { return strings.HasSuffix(h, ".sean.realgo.com") },
		PoolStart:    9000,
		PoolEnd:      9999,
		HTTPSPort:    443,
		HTTPPort:     80,
		MgmtBindPort: 0,
		MgmtHost:     "rgdevenv.sean.realgo.com",
		CADir:        "/nonexistent",
	}
}

func validLB() store.LoadBalancer {
	return store.LoadBalancer{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{
			ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}},
		}},
	}
}

func stateWith(lb store.LoadBalancer) *store.State {
	return &store.State{Version: store.CurrentVersion, LoadBalancers: []store.LoadBalancer{lb}}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(stateWith(validLB()), baseParams()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
}

func TestValidateListenPortInPool(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 9500
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestValidateReservedHTTPPort(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 80
	lb.Mappings[0].ListenTLS = false
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for redirect port, got %v", err)
	}
}

func TestValidateHTTPSPortRequiresTLS(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenTLS = false // port 443 but plaintext
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for non-TLS on https port, got %v", err)
	}
}

func TestValidateUpstreamNotAllowed(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Host = "evil.example.com"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestValidateBadScheme(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Scheme = "ftp"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestValidateCANotFound(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Scheme = "https"
	lb.Mappings[0].Upstream.TLS = store.UpstreamTLS{Mode: "ca", CAName: "missing"}
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for missing CA, got %v", err)
	}
}

func TestValidateHostNotCovered(t *testing.T) {
	lb := validLB()
	lb.Name = "rg-1.other.com" // Covers() returns false
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for uncovered host, got %v", err)
	}
}

func TestValidateReservedName(t *testing.T) {
	lb := validLB()
	lb.Name = "rgdevenv.sean.realgo.com" // == mgmt host
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for reserved name, got %v", err)
	}
}

func TestValidateNonCanonicalName(t *testing.T) {
	lb := validLB()
	lb.Name = "RG-1.Sean.Realgo.COM" // not canonical
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for non-canonical name, got %v", err)
	}
}

func TestValidateErrorCarriesCode(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 9500
	var te *Error
	if !errors.As(Validate(stateWith(lb), baseParams()), &te) || te.Code == "" {
		t.Fatal("expected a *txn.Error with a machine code")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/txn/ -v`
Expected: FAIL — `undefined: Validate`.

- [ ] **Step 3: Write the errors** — `internal/txn/errors.go`

```go
// Package txn applies management mutations through a single serialized staged
// transaction: snapshot → clone → build → validate → persist → publish → apply
// (§16).
package txn

import "errors"

// Sentinel kinds; the API maps them to HTTP status codes via errors.Is.
var (
	ErrValidation = errors.New("txn: validation error") // -> 400
	ErrConflict   = errors.New("txn: conflict")         // -> 409
	ErrNotFound   = errors.New("txn: not found")        // -> 404
)

// Error is a typed transaction error carrying a machine code and human message.
type Error struct {
	kind error // one of the sentinels above
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.kind }

// Validation/Conflict/NotFound build typed errors.
func Validation(code, msg string) *Error { return &Error{kind: ErrValidation, Code: code, Msg: msg} }
func Conflict(code, msg string) *Error   { return &Error{kind: ErrConflict, Code: code, Msg: msg} }
func NotFound(code, msg string) *Error   { return &Error{kind: ErrNotFound, Code: code, Msg: msg} }
```

- [ ] **Step 4: Write the validation** — `internal/txn/validate.go`

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/txn/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/txn/errors.go internal/txn/validate.go internal/txn/validate_test.go
git commit -m "feat(txn): typed errors + config-dependent validation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Transaction Manager (`internal/txn/txn.go`)

The serialized mutation engine: each method clones the snapshot, mutates, then
`commitLocked` validates → persists → publishes → applies (*§16*). Auto-port
cascade (*§11*) reuses `registry.Reconcile`. Stays `proxy`-free via callbacks.

**Files:**
- Create: `internal/txn/txn.go`
- Test: `internal/txn/txn_test.go`

- [ ] **Step 1: Write the failing test** — `internal/txn/txn_test.go`

```go
package txn

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newTestManager(t *testing.T) (*Manager, *store.Store, *int) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	applyCount := 0
	cfg := Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"}
	m := New(st, func(*store.State) { applyCount++ }, func(string) bool { return true }, upstream.NewPolicy(nil), cfg)
	m.newID = func() string { return "alloc-test" } // deterministic
	return m, st, &applyCount
}

func TestCreateLBAndDuplicate(t *testing.T) {
	m, st, applied := newTestManager(t)
	if _, err := m.CreateLB("RG-1.sean.realgo.com", "demo"); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if len(got.LoadBalancers) != 1 || got.LoadBalancers[0].Name != "rg-1.sean.realgo.com" {
		t.Fatalf("LB not created canonical: %+v", got.LoadBalancers)
	}
	if *applied == 0 {
		t.Fatal("apply callback not invoked")
	}
	// Duplicate (canonical match) -> conflict.
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestPutMappingCreateAndReplace(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	spec := MappingSpec{ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); err != nil {
		t.Fatal(err)
	}
	// Duplicate create -> conflict; replace (PUT) -> ok.
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict on duplicate create, got %v", err)
	}
	spec.Upstream.Port = 9012
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, true); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if got.LoadBalancers[0].Mappings[0].Upstream.Port != 9012 {
		t.Fatalf("replace did not take: %+v", got.LoadBalancers[0].Mappings[0])
	}
}

func TestPutMappingAllocateConvenience(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	spec := MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true, AllocLabel: "web"}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", spec, false); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	mp := got.LoadBalancers[0].Mappings[0]
	if !mp.AutoAllocated || mp.AllocationID == "" || mp.Upstream.Host != "localhost" || mp.Upstream.Port != 9000 {
		t.Fatalf("allocate convenience wrong: %+v", mp)
	}
	if len(got.PortAllocations) != 1 || got.PortAllocations[0].Port != 9000 || !got.PortAllocations[0].Auto {
		t.Fatalf("allocation not recorded: %+v", got.PortAllocations)
	}
}

func TestDeleteLBCascadesAutoPort(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteLB("rg-1.sean.realgo.com"); err != nil {
		t.Fatal(err)
	}
	got := st.Snapshot()
	if len(got.LoadBalancers) != 0 {
		t.Fatal("LB not deleted")
	}
	if len(got.PortAllocations) != 0 {
		t.Fatalf("auto allocation not cascaded: %+v", got.PortAllocations)
	}
}

func TestAllocateAndReturnPort(t *testing.T) {
	m, st, _ := newTestManager(t)
	_, a, err := m.AllocatePort("owner", "label")
	if err != nil {
		t.Fatal(err)
	}
	if a.Port != 9000 || a.Auto {
		t.Fatalf("manual allocation wrong: %+v", a)
	}
	if _, err := m.ReturnPort(9000); err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshot().PortAllocations) != 0 {
		t.Fatal("port not returned")
	}
	// Returning an unknown port -> not found.
	if _, err := m.ReturnPort(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReturnPortInUse(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", MappingSpec{ListenPort: 443, ListenTLS: true, Allocate: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReturnPort(9000); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict (port in use), got %v", err)
	}
}

func TestValidationFailureLeavesStateUnchanged(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.CreateLB("rg-1.sean.realgo.com", ""); err != nil {
		t.Fatal(err)
	}
	before := st.Snapshot()
	// Upstream host not allowlisted -> validation error, no mutation.
	bad := MappingSpec{ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "evil.example.com", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}}}
	if _, err := m.PutMapping("rg-1.sean.realgo.com", bad, false); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	after := st.Snapshot()
	if len(after.LoadBalancers[0].Mappings) != 0 {
		t.Fatal("failed mutation must not persist")
	}
	if before != after {
		t.Fatal("published snapshot pointer changed on a failed mutation")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/txn/ -run 'TestCreate|TestPut|TestDelete|TestAllocate|TestReturn|TestValidationFailure' -v`
Expected: FAIL — `undefined: New` / `undefined: Manager`.

- [ ] **Step 3: Write the implementation** — `internal/txn/txn.go`

```go
package txn

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/registry"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// Config carries static inputs for validation and allocation.
type Config struct {
	PoolStart, PoolEnd  int
	HTTPSPort, HTTPPort int
	MgmtBindPort        int
	MgmtHost            string
	CADir               string
}

// MappingSpec describes a mapping to create or replace. When Allocate is true the
// upstream is set to http://localhost:<allocated-port> and Upstream is ignored.
type MappingSpec struct {
	ListenPort int
	ListenTLS  bool
	Upstream   store.Upstream
	Allocate   bool
	AllocLabel string
}

// Manager serializes every state mutation and republishes the live proxy (§16).
type Manager struct {
	store  *store.Store
	apply  func(*store.State) // republish proxy (server.Apply); may be nil in tests
	covers func(string) bool  // cert coverage (resolver.Covers); may be nil
	policy *upstream.Policy
	cfg    Config

	now   func() time.Time
	newID func() string
	mu    sync.Mutex
}

// New builds a Manager.
func New(st *store.Store, apply func(*store.State), covers func(string) bool, policy *upstream.Policy, cfg Config) *Manager {
	return &Manager{
		store: st, apply: apply, covers: covers, policy: policy, cfg: cfg,
		now:   func() time.Time { return time.Now().UTC() },
		newID: defaultID,
	}
}

func defaultID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "alloc-" + hex.EncodeToString(b)
}

// Snapshot returns the current published state (read-only handlers).
func (m *Manager) Snapshot() *store.State { return m.store.Snapshot() }

func (m *Manager) params() ValidateParams {
	return ValidateParams{
		Policy: m.policy, Covers: m.covers,
		PoolStart: m.cfg.PoolStart, PoolEnd: m.cfg.PoolEnd,
		HTTPSPort: m.cfg.HTTPSPort, HTTPPort: m.cfg.HTTPPort,
		MgmtBindPort: m.cfg.MgmtBindPort, MgmtHost: m.cfg.MgmtHost, CADir: m.cfg.CADir,
	}
}

// commitLocked validates, persists (commit point), publishes, and applies.
// Caller holds m.mu.
//
// AIDEV-NOTE: commit point is a successful Save (§16). Publish + apply run only
// after persistence; apply is infallible (bind failures degrade, §10).
func (m *Manager) commitLocked(cand *store.State) (*store.State, error) {
	if err := Validate(cand, m.params()); err != nil {
		return nil, err
	}
	if err := m.store.Save(cand); err != nil {
		return nil, fmt.Errorf("txn: persist: %w", err)
	}
	m.store.Publish(cand)
	if m.apply != nil {
		m.apply(cand)
	}
	return cand, nil
}

// CreateLB adds a new load balancer (canonicalized) with no mappings.
func (m *Manager) CreateLB(name, label string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	if findLB(cand, cn) != nil {
		return nil, Conflict("duplicate_lb", fmt.Sprintf("load balancer %q already exists", cn))
	}
	cand.LoadBalancers = append(cand.LoadBalancers, store.LoadBalancer{Name: cn, Label: label, CreatedAt: m.now()})
	return m.commitLocked(cand)
}

// UpdateLBLabel sets the label of an existing load balancer.
func (m *Manager) UpdateLBLabel(name, label string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	lb.Label = label
	return m.commitLocked(cand)
}

// DeleteLB removes a load balancer and cascades its auto-allocated ports (§11).
func (m *Manager) DeleteLB(name string) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	idx := lbIndex(cand, cn)
	if idx < 0 {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	cand.LoadBalancers = append(cand.LoadBalancers[:idx], cand.LoadBalancers[idx+1:]...)
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// PutMapping creates (replace=false) or replaces (replace=true) a mapping. When
// spec.Allocate is set, a localhost port is allocated and wired (§11).
func (m *Manager) PutMapping(name string, spec MappingSpec, replace bool) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	idx := mappingIndex(lb, spec.ListenPort)
	if !replace && idx >= 0 {
		return nil, Conflict("mapping_exists", fmt.Sprintf("mapping :%d already exists on %s", spec.ListenPort, cn))
	}

	nm := store.Mapping{ListenPort: spec.ListenPort, ListenTLS: spec.ListenTLS, Upstream: spec.Upstream}
	if spec.Allocate {
		a, err := registry.Allocate(cand.PortAllocations, m.cfg.PoolStart, m.cfg.PoolEnd, m.newID())
		if err != nil {
			if errors.Is(err, registry.ErrPoolExhausted) {
				return nil, Conflict("pool_exhausted", "port pool exhausted")
			}
			return nil, fmt.Errorf("txn: allocate: %w", err)
		}
		a.Owner = cn
		a.Label = spec.AllocLabel
		a.Auto = true
		a.AllocatedAt = m.now()
		cand.PortAllocations = append(cand.PortAllocations, a)
		nm.Upstream = store.Upstream{Scheme: "http", Host: "localhost", Port: a.Port, TLS: store.UpstreamTLS{Mode: "verify"}}
		nm.AllocationID = a.ID
		nm.AutoAllocated = true
	}

	if idx >= 0 {
		lb.Mappings[idx] = nm
	} else {
		lb.Mappings = append(lb.Mappings, nm)
	}
	// Free any auto allocation orphaned by a replace (§11).
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// DeleteMapping removes a mapping and cascades its auto-allocated port (§11).
func (m *Manager) DeleteMapping(name string, listenPort int) (*store.State, error) {
	cn, err := canon.Host(name)
	if err != nil {
		return nil, Validation("invalid_hostname", err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	lb := findLB(cand, cn)
	if lb == nil {
		return nil, NotFound("lb_not_found", fmt.Sprintf("load balancer %q not found", cn))
	}
	mi := mappingIndex(lb, listenPort)
	if mi < 0 {
		return nil, NotFound("mapping_not_found", fmt.Sprintf("mapping :%d not found on %s", listenPort, cn))
	}
	lb.Mappings = append(lb.Mappings[:mi], lb.Mappings[mi+1:]...)
	cand.PortAllocations, _ = registry.Reconcile(cand)
	return m.commitLocked(cand)
}

// AllocatePort reserves a manual (non-auto) port and returns the allocation.
func (m *Manager) AllocatePort(owner, label string) (*store.State, store.PortAllocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	a, err := registry.Allocate(cand.PortAllocations, m.cfg.PoolStart, m.cfg.PoolEnd, m.newID())
	if err != nil {
		if errors.Is(err, registry.ErrPoolExhausted) {
			return nil, store.PortAllocation{}, Conflict("pool_exhausted", "port pool exhausted")
		}
		return nil, store.PortAllocation{}, fmt.Errorf("txn: allocate: %w", err)
	}
	a.Owner = owner
	a.Label = label
	a.Auto = false
	a.AllocatedAt = m.now()
	cand.PortAllocations = append(cand.PortAllocations, a)
	st, err := m.commitLocked(cand)
	if err != nil {
		return nil, store.PortAllocation{}, err
	}
	return st, a, nil
}

// ReturnPort frees a port; in-use ports are rejected (§11).
func (m *Manager) ReturnPort(port int) (*store.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cand := m.store.Snapshot().Clone()
	a := registry.AllocationByPort(cand.PortAllocations, port)
	if a == nil {
		return nil, NotFound("port_not_allocated", fmt.Sprintf("port %d is not allocated", port))
	}
	if registry.IsAllocationReferenced(cand, a.ID) {
		return nil, Conflict("port_in_use", fmt.Sprintf("port %d is referenced by a mapping", port))
	}
	cand.PortAllocations, _ = registry.Return(cand.PortAllocations, port)
	return m.commitLocked(cand)
}

func findLB(st *store.State, name string) *store.LoadBalancer {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return &st.LoadBalancers[i]
		}
	}
	return nil
}

func lbIndex(st *store.State, name string) int {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return i
		}
	}
	return -1
}

func mappingIndex(lb *store.LoadBalancer, port int) int {
	for i := range lb.Mappings {
		if lb.Mappings[i].ListenPort == port {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/txn/ -v -race`
Expected: PASS (validate + txn tests).

- [ ] **Step 5: Commit**

```bash
git add internal/txn/txn.go internal/txn/txn_test.go
git commit -m "feat(txn): serialized mutation manager with auto-port cascade

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: API core — mux, helpers, auth middleware (`internal/api`)

The management `http.Handler`: a Go 1.22 method-pattern mux with `/healthz`
unauthenticated and `/api/v1/*` behind bearer-auth + rate-limit middleware;
JSON/error helpers; txn-error → HTTP-status mapping (*§12, §15*). Later tasks
register the actual routes.

**Files:**
- Create: `internal/api/api.go`
- Create: `internal/api/middleware.go`
- Test: `internal/api/middleware_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/middleware_test.go`

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/txn"
)

const testToken = "0123456789abcdef0123456789abcdef"

func testHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{
		auth:    auth.NewAuthenticator(testToken),
		limiter: auth.NewRateLimiter(3, time.Minute),
		version: "test",
		logger:  discardLogger(),
	}
}

func TestAuthMiddlewareRejectsAndRateLimits(t *testing.T) {
	h := testHandler(t)
	protected := h.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No token -> 401.
	r := httptest.NewRequest("GET", "/api/v1/x", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: code = %d, want 401", w.Code)
	}

	// Repeated failures from one IP -> 429 (limiter limit = 3).
	bad := func() int {
		rr := httptest.NewRequest("GET", "/api/v1/x", nil)
		rr.RemoteAddr = "8.8.8.8:1"
		ww := httptest.NewRecorder()
		protected.ServeHTTP(ww, rr)
		return ww.Code
	}
	for i := 0; i < 3; i++ {
		if got := bad(); got != http.StatusUnauthorized {
			t.Fatalf("failure %d: code = %d, want 401", i, got)
		}
	}
	if got := bad(); got != http.StatusTooManyRequests {
		t.Fatalf("after 3 failures: code = %d, want 429", got)
	}

	// Good token -> 200 (use a fresh IP not yet rate-limited).
	r = httptest.NewRequest("GET", "/api/v1/x", nil)
	r.RemoteAddr = "1.1.1.1:1"
	r.Header.Set("Authorization", "Bearer "+testToken)
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("good token: code = %d, want 200", w.Code)
	}
}

func TestHealthzUnauthenticated(t *testing.T) {
	h := New(Deps{Auth: auth.NewAuthenticator(testToken), Limiter: auth.NewRateLimiter(10, time.Minute), Logger: discardLogger()})
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz code = %d, want 200", w.Code)
	}
}

func TestWriteErrMapping(t *testing.T) {
	h := testHandler(t)
	cases := []struct {
		err  error
		want int
	}{
		{txn.Validation("bad", "x"), http.StatusBadRequest},
		{txn.Conflict("dup", "x"), http.StatusConflict},
		{txn.NotFound("nf", "x"), http.StatusNotFound},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.writeErr(w, c.err)
		if w.Code != c.want {
			t.Fatalf("err %v -> code %d, want %d", c.err, w.Code, c.want)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] == "" || body["error"] == "" {
			t.Fatalf("error body missing fields: %s", w.Body.String())
		}
	}
}
```

> Note: the test file also imports `"time"` (for `time.Minute`). `discardLogger` is defined in `api.go` (same package, Step 3) — no extra import needed for it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -v`
Expected: FAIL — `undefined: Handler`.

- [ ] **Step 3: Write the core** — `internal/api/api.go`

```go
// Package api implements the rgdevenv management REST API (§12). All /api/v1/*
// routes are bearer-authenticated and rate-limited; /healthz is open.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/txn"
)

// Deps are the dependencies for the management handler.
type Deps struct {
	Txn         *txn.Manager
	Auth        *auth.Authenticator
	Limiter     *auth.RateLimiter
	CADir       string
	Version     string
	HTTPSPort   int
	HTTPPort    int
	PoolStart   int
	PoolEnd     int
	ActivePorts func() []int
	Logger      *slog.Logger
}

// Handler is the management-plane http.Handler.
type Handler struct {
	txn         *txn.Manager
	auth        *auth.Authenticator
	limiter     *auth.RateLimiter
	caDir       string
	version     string
	httpsPort   int
	httpPort    int
	poolStart   int
	poolEnd     int
	activePorts func() []int
	logger      *slog.Logger
	mux         http.Handler
}

// New builds the management handler with all routes wired.
func New(d Deps) *Handler {
	if d.Logger == nil {
		d.Logger = discardLogger()
	}
	h := &Handler{
		txn: d.Txn, auth: d.Auth, limiter: d.Limiter, caDir: d.CADir,
		version: d.Version, httpsPort: d.HTTPSPort, httpPort: d.HTTPPort,
		poolStart: d.PoolStart, poolEnd: d.PoolEnd,
		activePorts: d.ActivePorts, logger: d.Logger,
	}
	h.mux = h.buildMux()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *Handler) buildMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)

	api := http.NewServeMux()
	// AIDEV-NOTE: route registration is added by later tasks (lbs, mappings,
	// ports, cas, status). Keep all /api/v1/* routes on THIS sub-mux so the auth
	// middleware below covers every one of them.
	// <register-api-routes>

	mux.Handle("/api/v1/", h.authMiddleware(api))
	// AIDEV-TODO(phase2b): mount the static login shell + dashboard at "/".
	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- JSON / error helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// writeErr maps a (possibly txn-typed) error to an HTTP status + JSON body (§12).
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code, msg := "internal", "internal error"
	var te *txn.Error
	if errors.As(err, &te) {
		code, msg = te.Code, te.Msg
		switch {
		case errors.Is(err, txn.ErrValidation):
			status = http.StatusBadRequest
		case errors.Is(err, txn.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, txn.ErrNotFound):
			status = http.StatusNotFound
		}
	} else {
		h.logger.Error("api: unexpected error", "error", err)
	}
	writeJSON(w, status, errorBody{Error: msg, Code: code})
}

// decodeJSON reads a JSON body with unknown-field rejection.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return txn.Validation("bad_json", "invalid request body: "+err.Error())
	}
	return nil
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

- [ ] **Step 4: Write the middleware** — `internal/api/middleware.go`

```go
package api

import (
	"net"
	"net/http"

	"github.com/realgo/rgdevenv/internal/auth"
)

// authMiddleware enforces bearer auth + per-IP failed-attempt rate limiting (§15).
//
// AIDEV-NOTE: bearer-only — cookies are never consulted, so there is no CSRF
// surface. Rate-limit check precedes auth so a flood of bad tokens is capped.
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !h.limiter.Allowed(ip) {
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many failed attempts", Code: "rate_limited"})
			return
		}
		tok, ok := auth.ParseBearer(r.Header.Get("Authorization"))
		if !ok || !h.auth.Check(tok) {
			h.limiter.RecordFailure(ip)
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized", Code: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// audit logs a management mutation (actor = client IP) (§17).
func (h *Handler) audit(r *http.Request, action, target string) {
	h.logger.Info("audit", "actor", clientIP(r), "action", action, "target", target)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/api.go internal/api/middleware.go internal/api/middleware_test.go
git commit -m "feat(api): management mux, bearer+rate-limit middleware, error mapping

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Load-balancer endpoints (`internal/api/lbs.go`)

`GET/POST /lbs`, `GET/PATCH/DELETE /lbs/{name}` (*§12*).

**Files:**
- Modify: `internal/api/api.go` (register routes in `buildMux`)
- Create: `internal/api/lbs.go`
- Test: `internal/api/lbs_test.go` (also defines shared API test helpers)

- [ ] **Step 1: Write the failing test** — `internal/api/lbs_test.go`

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// newAPITestHandler builds a Handler over a real (temp) store with no-op apply
// and always-covered certs. Reused by the other API tests in this package.
func newAPITestHandler(t *testing.T) *Handler {
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
		ActivePorts: func() []int { return []int{443} }, Logger: discardLogger(),
	})
}

func authReq(method, target, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.RemoteAddr = "127.0.0.1:1"
	return r
}

func do(h *Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestLBLifecycle(t *testing.T) {
	h := newAPITestHandler(t)

	// Create (name canonicalized).
	w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"RG-1.sean.realgo.com","label":"demo"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", w.Code, w.Body)
	}
	var lb store.LoadBalancer
	_ = json.Unmarshal(w.Body.Bytes(), &lb)
	if lb.Name != "rg-1.sean.realgo.com" || lb.Label != "demo" {
		t.Fatalf("created LB wrong: %+v", lb)
	}

	// Duplicate -> 409.
	if w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`)); w.Code != http.StatusConflict {
		t.Fatalf("duplicate: code=%d", w.Code)
	}

	// List.
	w = do(h, authReq("GET", "/api/v1/lbs", ""))
	var list []store.LoadBalancer
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 1 {
		t.Fatalf("list: code=%d n=%d", w.Code, len(list))
	}

	// Get one / get missing.
	if w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != 200 {
		t.Fatalf("get: code=%d", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs/nope.sean.realgo.com", "")); w.Code != http.StatusNotFound {
		t.Fatalf("get missing: code=%d", w.Code)
	}

	// Patch label.
	w = do(h, authReq("PATCH", "/api/v1/lbs/rg-1.sean.realgo.com", `{"label":"renamed"}`))
	if w.Code != 200 {
		t.Fatalf("patch: code=%d", w.Code)
	}

	// Delete -> 204, then gone.
	if w := do(h, authReq("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != http.StatusNoContent {
		t.Fatalf("delete: code=%d", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: code=%d", w.Code)
	}
}

func TestCreateLBMissingName(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, authReq("POST", "/api/v1/lbs", `{}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing name: code=%d", w.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run 'TestLB|TestCreateLB' -v`
Expected: FAIL — `h.txn` nil / routes not registered (404s) — i.e., assertions fail.

- [ ] **Step 3: Register routes** — in `internal/api/api.go` `buildMux`, replace the marker line:
```go
	// <register-api-routes>
```
with:
```go
	h.registerLBRoutes(api)
	// <register-api-routes>
```

- [ ] **Step 4: Write the handlers** — `internal/api/lbs.go`

```go
package api

import (
	"net/http"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerLBRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/lbs", h.listLBs)
	mux.HandleFunc("POST /api/v1/lbs", h.createLB)
	mux.HandleFunc("GET /api/v1/lbs/{name}", h.getLB)
	mux.HandleFunc("PATCH /api/v1/lbs/{name}", h.patchLB)
	mux.HandleFunc("DELETE /api/v1/lbs/{name}", h.deleteLB)
}

func (h *Handler) listLBs(w http.ResponseWriter, r *http.Request) {
	lbs := h.txn.Snapshot().LoadBalancers
	if lbs == nil {
		lbs = []store.LoadBalancer{}
	}
	writeJSON(w, http.StatusOK, lbs)
}

type createLBReq struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

func (h *Handler) createLB(w http.ResponseWriter, r *http.Request) {
	var req createLBReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	if req.Name == "" {
		h.writeErr(w, txn.Validation("missing_name", "name is required"))
		return
	}
	st, err := h.txn.CreateLB(req.Name, req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(req.Name)
	h.audit(r, "create_lb", cn)
	writeJSON(w, http.StatusCreated, lbByName(st, cn))
}

func (h *Handler) getLB(w http.ResponseWriter, r *http.Request) {
	cn, err := canon.Host(r.PathValue("name"))
	if err != nil {
		h.writeErr(w, txn.Validation("invalid_hostname", err.Error()))
		return
	}
	lb := lbByName(h.txn.Snapshot(), cn)
	if lb == nil {
		h.writeErr(w, txn.NotFound("lb_not_found", "load balancer not found"))
		return
	}
	writeJSON(w, http.StatusOK, lb)
}

type patchLBReq struct {
	Label string `json:"label"`
}

func (h *Handler) patchLB(w http.ResponseWriter, r *http.Request) {
	var req patchLBReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.UpdateLBLabel(r.PathValue("name"), req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "update_lb", cn)
	writeJSON(w, http.StatusOK, lbByName(st, cn))
}

func (h *Handler) deleteLB(w http.ResponseWriter, r *http.Request) {
	if _, err := h.txn.DeleteLB(r.PathValue("name")); err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "delete_lb", cn)
	w.WriteHeader(http.StatusNoContent)
}

func lbByName(st *store.State, name string) *store.LoadBalancer {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return &st.LoadBalancers[i]
		}
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/api.go internal/api/lbs.go internal/api/lbs_test.go
git commit -m "feat(api): load-balancer CRUD endpoints

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Mapping endpoints (`internal/api/mappings.go`)

`POST /lbs/{name}/mappings`, `PUT/DELETE /lbs/{name}/mappings/{port}`, incl. the
`allocate` convenience and the PUT body/path port-match rule (*§11, §12*).

**Files:**
- Modify: `internal/api/api.go` (register routes)
- Create: `internal/api/mappings.go`
- Test: `internal/api/mappings_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/mappings_test.go`

```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func mustCreateLB(t *testing.T, h *Handler, name string) {
	t.Helper()
	if w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"`+name+`"}`)); w.Code != http.StatusCreated {
		t.Fatalf("create LB %s: code=%d body=%s", name, w.Code, w.Body)
	}
}

func TestMappingCreateReplaceDelete(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")

	body := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("create mapping: code=%d body=%s", w.Code, w.Body)
	}
	// Duplicate create -> 409.
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusConflict {
		t.Fatalf("dup mapping: code=%d", w.Code)
	}
	// Replace via PUT -> 200.
	put := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9099,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("PUT", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", put)); w.Code != http.StatusOK {
		t.Fatalf("put mapping: code=%d body=%s", w.Code, w.Body)
	}
	// PUT body/path port mismatch -> 400.
	bad := `{"listen_port":8443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9099,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("PUT", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", bad)); w.Code != http.StatusBadRequest {
		t.Fatalf("port mismatch: code=%d", w.Code)
	}
	// Delete -> 204.
	if w := do(h, authReq("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", "")); w.Code != http.StatusNoContent {
		t.Fatalf("delete mapping: code=%d", w.Code)
	}
}

func TestMappingAllocateConvenience(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-2.sean.realgo.com")
	body := `{"listen_port":443,"listen_tls":true,"allocate":true,"label":"web"}`
	w := do(h, authReq("POST", "/api/v1/lbs/rg-2.sean.realgo.com/mappings", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("allocate mapping: code=%d body=%s", w.Code, w.Body)
	}
	var m store.Mapping
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if !m.AutoAllocated || m.Upstream.Host != "localhost" || m.Upstream.Port != 9000 {
		t.Fatalf("allocate convenience wrong: %+v", m)
	}
}

func TestMappingListenPortInPool(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-3.sean.realgo.com")
	body := `{"listen_port":9500,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-3.sean.realgo.com/mappings", body)); w.Code != http.StatusConflict {
		t.Fatalf("listen_port in pool: code=%d", w.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestMapping -v`
Expected: FAIL — routes 404 (assertions fail).

- [ ] **Step 3: Register routes** — in `buildMux`, add before the marker:
```go
	h.registerMappingRoutes(api)
```

- [ ] **Step 4: Write the handlers** — `internal/api/mappings.go`

```go
package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerMappingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/lbs/{name}/mappings", h.createMapping)
	mux.HandleFunc("PUT /api/v1/lbs/{name}/mappings/{port}", h.putMapping)
	mux.HandleFunc("DELETE /api/v1/lbs/{name}/mappings/{port}", h.deleteMapping)
}

type mappingReq struct {
	ListenPort *int  `json:"listen_port"`
	ListenTLS  *bool `json:"listen_tls"`
	Upstream   *struct {
		Scheme string `json:"scheme"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		TLS    struct {
			Mode   string `json:"mode"`
			CAName string `json:"ca_name"`
		} `json:"tls"`
	} `json:"upstream"`
	Allocate bool   `json:"allocate"`
	Label    string `json:"label"`
}

// spec builds a txn.MappingSpec for the given listen port (defaults: tls=true).
func (req mappingReq) spec(port int) (txn.MappingSpec, error) {
	tls := true
	if req.ListenTLS != nil {
		tls = *req.ListenTLS
	}
	s := txn.MappingSpec{ListenPort: port, ListenTLS: tls, Allocate: req.Allocate, AllocLabel: req.Label}
	if !req.Allocate {
		if req.Upstream == nil {
			return s, txn.Validation("missing_upstream", "upstream is required unless allocate=true")
		}
		mode := req.Upstream.TLS.Mode
		if mode == "" {
			mode = "verify"
		}
		s.Upstream = store.Upstream{
			Scheme: req.Upstream.Scheme,
			Host:   req.Upstream.Host,
			Port:   req.Upstream.Port,
			TLS:    store.UpstreamTLS{Mode: mode, CAName: req.Upstream.TLS.CAName},
		}
	}
	return s, nil
}

func (h *Handler) createMapping(w http.ResponseWriter, r *http.Request) {
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	port := 443
	if req.ListenPort != nil {
		port = *req.ListenPort
	}
	spec, err := req.spec(port)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.PutMapping(r.PathValue("name"), spec, false)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "create_mapping", fmt.Sprintf("%s:%d", cn, port))
	writeJSON(w, http.StatusCreated, mappingInLB(st, cn, port))
}

func (h *Handler) putMapping(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "listen_port path must be an integer"))
		return
	}
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	if req.ListenPort != nil && *req.ListenPort != port {
		h.writeErr(w, txn.Validation("port_mismatch", "body listen_port must equal the path listen_port"))
		return
	}
	spec, err := req.spec(port)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.PutMapping(r.PathValue("name"), spec, true)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "replace_mapping", fmt.Sprintf("%s:%d", cn, port))
	writeJSON(w, http.StatusOK, mappingInLB(st, cn, port))
}

func (h *Handler) deleteMapping(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "listen_port path must be an integer"))
		return
	}
	if _, err := h.txn.DeleteMapping(r.PathValue("name"), port); err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "delete_mapping", fmt.Sprintf("%s:%d", cn, port))
	w.WriteHeader(http.StatusNoContent)
}

func mappingInLB(st *store.State, name string, port int) *store.Mapping {
	lb := lbByName(st, name)
	if lb == nil {
		return nil
	}
	for i := range lb.Mappings {
		if lb.Mappings[i].ListenPort == port {
			return &lb.Mappings[i]
		}
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/api.go internal/api/mappings.go internal/api/mappings_test.go
git commit -m "feat(api): mapping endpoints with allocate convenience and PUT match

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Ports, CAs, status endpoints (`internal/api/ports.go`, `misc.go`)

`GET /ports`, `POST /ports/allocate`, `DELETE /ports/{port}`; `GET /cas`,
`GET /status` (*§11, §12, §17*). Adds `upstream.ListCAs` (deferred from Phase 1).

**Files:**
- Modify: `internal/upstream/ca.go` (add `ListCAs`)
- Modify: `internal/api/api.go` (register routes)
- Create: `internal/api/ports.go`, `internal/api/misc.go`
- Test: `internal/api/ports_test.go`, `internal/api/misc_test.go`
- Test: `internal/upstream/ca_test.go` (append a `ListCAs` test)

- [ ] **Step 1: Write the failing tests**

`internal/upstream/ca_test.go` (append):
```go
func TestListCAs(t *testing.T) {
	dir := writeTestCAPEM(t, "corp") // helper already in ca_test.go
	// Add a second + a non-pem file.
	if err := os.WriteFile(filepath.Join(dir, "build-box.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := ListCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "build-box" || names[1] != "corp" {
		t.Fatalf("ListCAs = %v, want sorted [build-box corp]", names)
	}
	// Missing dir -> nil, no error.
	if n, err := ListCAs(filepath.Join(dir, "nope")); err != nil || n != nil {
		t.Fatalf("missing dir: %v %v", n, err)
	}
}
```

`internal/api/ports_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPortAllocateListReturn(t *testing.T) {
	h := newAPITestHandler(t)

	// Allocate -> 201 {id,port}.
	w := do(h, authReq("POST", "/api/v1/ports/allocate", `{"owner":"x","label":"y"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("allocate: code=%d body=%s", w.Code, w.Body)
	}
	var a map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if a["port"].(float64) != 9000 || a["id"] == "" {
		t.Fatalf("allocate body: %v", a)
	}

	// List -> used=1.
	w = do(h, authReq("GET", "/api/v1/ports", ""))
	var lp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &lp)
	if lp["used"].(float64) != 1 {
		t.Fatalf("ports used: %v", lp)
	}

	// Return -> 204.
	if w := do(h, authReq("DELETE", "/api/v1/ports/9000", "")); w.Code != http.StatusNoContent {
		t.Fatalf("return: code=%d", w.Code)
	}
	// Return unknown -> 404.
	if w := do(h, authReq("DELETE", "/api/v1/ports/9999", "")); w.Code != http.StatusNotFound {
		t.Fatalf("return unknown: code=%d", w.Code)
	}
}

func TestReturnPortInUse(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	body := `{"listen_port":443,"listen_tls":true,"allocate":true}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("alloc mapping: code=%d body=%s", w.Code, w.Body)
	}
	if w := do(h, authReq("DELETE", "/api/v1/ports/9000", "")); w.Code != http.StatusConflict {
		t.Fatalf("return in-use: code=%d", w.Code)
	}
}
```

`internal/api/misc_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListCAsEndpoint(t *testing.T) {
	h := newAPITestHandler(t)
	// h.caDir is accessible (same package); ListCAs only lists *.pem names.
	if err := os.WriteFile(filepath.Join(h.caDir, "corp.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := do(h, authReq("GET", "/api/v1/cas", ""))
	var names []string
	_ = json.Unmarshal(w.Body.Bytes(), &names)
	if w.Code != 200 || len(names) != 1 || names[0] != "corp" {
		t.Fatalf("cas: code=%d names=%v", w.Code, names)
	}
}

func TestStatusEndpoint(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	w := do(h, authReq("GET", "/api/v1/status", ""))
	var s map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s["version"] != "test" || s["load_balancers"].(float64) != 1 {
		t.Fatalf("status: code=%d body=%v", w.Code, s)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/upstream/ -run TestListCAs -v` and `go test ./internal/api/ -run 'TestPort|TestReturn|TestListCAsEndpoint|TestStatus' -v`
Expected: FAIL — `undefined: ListCAs` / routes 404.

- [ ] **Step 3: Add `ListCAs`** — append to `internal/upstream/ca.go` (add `"sort"` to the import block):

```go
// ListCAs returns the sorted names of *.pem CA files in caDir (extension
// stripped). A missing directory yields nil with no error (§12 GET /cas).
func ListCAs(caDir string) ([]string, error) {
	entries, err := os.ReadDir(caDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); strings.HasSuffix(n, ".pem") {
			names = append(names, strings.TrimSuffix(n, ".pem"))
		}
	}
	sort.Strings(names)
	return names, nil
}
```

- [ ] **Step 4: Register routes** — in `buildMux`, add before the marker:
```go
	h.registerPortRoutes(api)
	h.registerMiscRoutes(api)
```

- [ ] **Step 5: Write the handlers**

`internal/api/ports.go`:
```go
package api

import (
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerPortRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ports", h.listPorts)
	mux.HandleFunc("POST /api/v1/ports/allocate", h.allocatePort)
	mux.HandleFunc("DELETE /api/v1/ports/{port}", h.returnPort)
}

type portsResp struct {
	Start       int                    `json:"start"`
	End         int                    `json:"end"`
	Used        int                    `json:"used"`
	Free        int                    `json:"free"`
	Allocations []store.PortAllocation `json:"allocations"`
}

func (h *Handler) listPorts(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	allocs := snap.PortAllocations
	if allocs == nil {
		allocs = []store.PortAllocation{}
	}
	total := h.poolEnd - h.poolStart + 1
	writeJSON(w, http.StatusOK, portsResp{
		Start: h.poolStart, End: h.poolEnd,
		Used: len(allocs), Free: total - len(allocs), Allocations: allocs,
	})
}

type allocReq struct {
	Owner string `json:"owner"`
	Label string `json:"label"`
}

func (h *Handler) allocatePort(w http.ResponseWriter, r *http.Request) {
	var req allocReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	_, a, err := h.txn.AllocatePort(req.Owner, req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.audit(r, "allocate_port", strconv.Itoa(a.Port))
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "port": a.Port})
}

func (h *Handler) returnPort(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "port path must be an integer"))
		return
	}
	if _, err := h.txn.ReturnPort(port); err != nil {
		h.writeErr(w, err)
		return
	}
	h.audit(r, "return_port", strconv.Itoa(port))
	w.WriteHeader(http.StatusNoContent)
}
```

`internal/api/misc.go`:
```go
package api

import (
	"net/http"

	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func (h *Handler) registerMiscRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cas", h.listCAs)
	mux.HandleFunc("GET /api/v1/status", h.status)
}

func (h *Handler) listCAs(w http.ResponseWriter, r *http.Request) {
	names, err := upstream.ListCAs(h.caDir)
	if err != nil {
		h.writeErr(w, txn.Validation("ca_dir_error", err.Error()))
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

type statusResp struct {
	Version         string `json:"version"`
	HTTPSPort       int    `json:"https_port"`
	HTTPPort        int    `json:"http_port"`
	ActiveListeners []int  `json:"active_listeners"`
	LoadBalancers   int    `json:"load_balancers"`
	Mappings        int    `json:"mappings"`
	Allocations     int    `json:"allocations"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	mappings := 0
	for _, lb := range snap.LoadBalancers {
		mappings += len(lb.Mappings)
	}
	active := []int{}
	if h.activePorts != nil {
		active = h.activePorts()
	}
	writeJSON(w, http.StatusOK, statusResp{
		Version: h.version, HTTPSPort: h.httpsPort, HTTPPort: h.httpPort,
		ActiveListeners: active, LoadBalancers: len(snap.LoadBalancers),
		Mappings: mappings, Allocations: len(snap.PortAllocations),
	})
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/upstream/ ./internal/api/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/upstream/ca.go internal/upstream/ca_test.go internal/api/api.go internal/api/ports.go internal/api/misc.go internal/api/ports_test.go internal/api/misc_test.go
git commit -m "feat(api): port/cas/status endpoints; upstream.ListCAs

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Serve wiring (`cmd/rgdevenv/serve.go`)

Build the management plane (auth + txn + API), install it on the proxy at the
MgmtHost seam, and start the optional `management.bind` plaintext listener
(*§6, §15*). Adds small accessors: `config.Config.MgmtBindPort()` and
`proxy.Server.ActivePorts()`.

**Files:**
- Modify: `internal/config/config.go` (add exported `MgmtBindPort`)
- Modify: `internal/proxy/server.go` (add `ActivePorts` passthrough)
- Modify: `cmd/rgdevenv/serve.go` (wire management plane + management.bind)
- Modify: `cmd/rgdevenv/serve_test.go` (extend the wiring test)

- [ ] **Step 1: Add the small accessors**

`internal/config/config.go` — add (it wraps the existing unexported `mgmtBindPort`):
```go
// MgmtBindPort returns the TCP port of management.bind, or 0 for none/unix.
func (c *Config) MgmtBindPort() int {
	p, ok := c.mgmtBindPort()
	if !ok {
		return 0
	}
	return p
}
```

`internal/proxy/server.go` — add:
```go
// ActivePorts returns the currently bound listener ports (for /status).
func (s *Server) ActivePorts() []int { return s.listeners.ActivePorts() }
```

- [ ] **Step 2: Write the failing test** — modify `cmd/rgdevenv/serve_test.go`

Update `TestSetupServerWiring`: set a token before `setupServer` (it now requires
one), and after the existing 404 + cert-CN assertions, add management-API checks.
Add `"strings"` to the imports if missing.

Insert at the very start of `TestSetupServerWiring` (before building `cfg`):
```go
	const mgmtToken = "0123456789abcdef0123456789abcdef"
	t.Setenv("RGDEVENV_TOKEN", mgmtToken)
```

Then append, after the existing assertions in that test:
```go
	// /healthz is unauthenticated.
	if resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/healthz", httpsPort)); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		// healthz is host-agnostic only on the mgmt host; set Host explicitly:
	}
	mgmtGet := func(path, token string) int {
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d%s", httpsPort, path), nil)
		req.Host = "rgdevenv.sean.realgo.com"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := mgmtGet("/healthz", ""); code != http.StatusOK {
		t.Fatalf("mgmt /healthz = %d, want 200", code)
	}
	if code := mgmtGet("/api/v1/status", ""); code != http.StatusUnauthorized {
		t.Fatalf("mgmt /api/v1/status without token = %d, want 401", code)
	}
	if code := mgmtGet("/api/v1/status", mgmtToken); code != http.StatusOK {
		t.Fatalf("mgmt /api/v1/status with token = %d, want 200", code)
	}
```

> The first `client.Get(.../healthz)` block above is illustrative; the real checks
> go through `mgmtGet`, which sets `Host: rgdevenv.sean.realgo.com` so the proxy
> routes to the management handler. You may delete the illustrative `client.Get`
> block and keep only the `mgmtGet` checks.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./cmd/rgdevenv/ -run TestSetupServerWiring -v`
Expected: FAIL — `setupServer` returns a token error / management routes 404.

- [ ] **Step 4: Rewrite `setupServer` and add wiring** — `cmd/rgdevenv/serve.go`

Add imports: `"net"`, `"strings"`, `"time"`, and the new packages
`github.com/realgo/rgdevenv/internal/{api,auth,txn}` (keep existing imports).

Add a version constant near the top:
```go
const version = "0.1.0"
```

Replace `setupServer` with:
```go
// setupServer opens + validates + reconciles the store, builds the proxy server,
// and installs the management plane (auth + transaction + REST API) at the
// MgmtHost seam. It does NOT bind sockets (call srv.Apply); it returns the
// management handler so the caller can also serve it on the optional bind.
func setupServer(cfg *config.Config, logger *slog.Logger) (*proxy.Server, *store.Store, http.Handler, error) {
	st, err := store.Open(cfg.StateFile)
	if err != nil {
		return nil, nil, nil, err
	}
	snap := st.Snapshot()
	if err := store.Validate(snap); err != nil {
		st.Close()
		return nil, nil, nil, fmt.Errorf("state invalid: %w", err)
	}
	if allocs, changed := registry.Reconcile(snap); changed {
		snap.PortAllocations = allocs
		if err := st.Save(snap); err != nil {
			st.Close()
			return nil, nil, nil, fmt.Errorf("persist reconciled state: %w", err)
		}
		st.Publish(snap)
		logger.Info("reconciled orphaned port allocations")
	}

	resolver, err := proxy.NewCertResolver(cfg.AllCertPairs())
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}

	limits := proxy.DefaultLimits()
	srv := proxy.NewServer(proxy.ServerConfig{
		BindAddr:    cfg.BindAddr,
		HTTPSPort:   cfg.HTTPSPort,
		HTTPPort:    cfg.HTTPPort,
		CADir:       cfg.CADir,
		MgmtHost:    cfg.ManagementHostname,
		DialTimeout: limits.DialTimeout,
	}, upstream.NewPolicy(cfg.Upstreams.Allow), resolver, limits, logger)

	// --- management plane (Phase 2) ---
	token, err := auth.LoadToken(cfg.TokenFile)
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}
	mgr := txn.New(st, func(state *store.State) {
		for _, d := range srv.Apply(state) {
			logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
		}
	}, resolver.Covers, upstream.NewPolicy(cfg.Upstreams.Allow), txn.Config{
		PoolStart:    cfg.PortPool.Start,
		PoolEnd:      cfg.PortPool.End,
		HTTPSPort:    cfg.HTTPSPort,
		HTTPPort:     cfg.HTTPPort,
		MgmtBindPort: cfg.MgmtBindPort(),
		MgmtHost:     cfg.ManagementHostname,
		CADir:        cfg.CADir,
	})
	mgmtHandler := api.New(api.Deps{
		Txn:         mgr,
		Auth:        auth.NewAuthenticator(token),
		Limiter:     auth.NewRateLimiter(cfg.Management.AuthRateLimitPerMin, time.Minute),
		CADir:       cfg.CADir,
		Version:     version,
		HTTPSPort:   cfg.HTTPSPort,
		HTTPPort:    cfg.HTTPPort,
		PoolStart:   cfg.PortPool.Start,
		PoolEnd:     cfg.PortPool.End,
		ActivePorts: srv.ActivePorts,
		Logger:      logger,
	})
	srv.SetManagementHandler(mgmtHandler)
	return srv, st, mgmtHandler, nil
}

// startMgmtBind serves the management handler on a separate plaintext listener
// (loopback TCP or unix socket; validated by config) (§15).
func startMgmtBind(bind string, h http.Handler, limits proxy.Limits, logger *slog.Logger) (*http.Server, error) {
	network, addr := "tcp", bind
	if strings.HasPrefix(bind, "/") || strings.HasPrefix(bind, "@") || strings.HasPrefix(bind, "unix:") {
		network = "unix"
		addr = strings.TrimPrefix(bind, "unix:")
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, fmt.Errorf("management bind %q: %w", bind, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: limits.ReadHeaderTimeout}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("management bind stopped", "error", err)
		}
	}()
	logger.Info("management plane on separate bind", "bind", bind)
	return srv, nil
}
```

Update `runServe` to use the new return signature and start the bind:
```go
func runServe(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, levelVar := newLogger(cfg.Log.Level)

	srv, st, mgmtHandler, err := setupServer(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	for _, d := range srv.Apply(st.Snapshot()) {
		logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
	}

	var mgmtBind *http.Server
	if cfg.Management.Bind != "" {
		mgmtBind, err = startMgmtBind(cfg.Management.Bind, mgmtHandler, proxy.DefaultLimits(), logger)
		if err != nil {
			return err
		}
	}

	logger.Info("rgdevenv listening", "https_port", cfg.HTTPSPort, "http_port", cfg.HTTPPort, "bind", cfg.BindAddr)
	return runSignals(configPath, srv, st, mgmtBind, logger, levelVar)
}
```

Update `runSignals` to also shut down the management bind on SIGTERM/SIGINT
(add the `mgmtBind *http.Server` parameter):
```go
func runSignals(configPath string, srv *proxy.Server, st *store.Store, mgmtBind *http.Server, logger *slog.Logger, levelVar *slog.LevelVar) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	for sig := range sigs {
		switch sig {
		case syscall.SIGHUP:
			newCfg, err := config.Load(configPath)
			if err != nil {
				logger.Error("config reload failed; keeping previous config", "error", err)
				break
			}
			levelVar.Set(parseLevel(newCfg.Log.Level))
			if err := srv.Resolver().Reload(newCfg.AllCertPairs()); err != nil {
				logger.Error("cert reload failed; keeping previous certs", "error", err)
			} else {
				logger.Info("certificates reloaded")
			}
			for _, d := range srv.Apply(st.Snapshot()) {
				logger.Warn("mapping degraded after reload", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
			}
		case syscall.SIGTERM, syscall.SIGINT:
			logger.Info("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if mgmtBind != nil {
				_ = mgmtBind.Shutdown(ctx)
			}
			err := srv.Shutdown(ctx)
			cancel()
			return err
		}
	}
	return nil
}
```

In `TestSetupServerWiring`, update the `setupServer` call to the new 4-value
signature: `srv, st, _, err := setupServer(cfg, logger)`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/rgdevenv/ -v -race`
Expected: PASS — management host serves `/healthz` (200), `/api/v1/status`
(401 without token, 200 with).

- [ ] **Step 6: Full build/vet + commit**

```bash
gofmt -w ./... && go build ./... && go vet ./...
git add internal/config/config.go internal/proxy/server.go cmd/rgdevenv/serve.go cmd/rgdevenv/serve_test.go
git commit -m "feat(serve): wire management plane (auth+txn+api) + optional management.bind

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: End-to-end management-API integration (`cmd/rgdevenv/integration_test.go`)

Drive the full stack — real TLS listener → proxy dispatch → management handler →
auth → txn → store — for a CRUD + cascade flow (*§11, §12, §15, §16*).

**Files:**
- Create: `cmd/rgdevenv/integration_test.go`

- [ ] **Step 1: Write the integration test** — `cmd/rgdevenv/integration_test.go`

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
	"path/filepath"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/config"
)

func TestManagementAPIEndToEnd(t *testing.T) {
	certFile, keyFile := writeMainTestCert(t) // helper in serve_test.go
	httpsPort := freeTCPPort(t)               // helper in serve_test.go
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
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, _, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv.Apply(st.Snapshot())
	defer srv.Shutdown(context.Background())
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	call := func(method, path, body, token string) (int, string) {
		var r *http.Request
		if body != "" {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, strings.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, "https://rgdevenv.sean.realgo.com"+path, nil)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Unauthenticated -> 401.
	if code, _ := call("GET", "/api/v1/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	// Create LB.
	if code, body := call("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`, token); code != http.StatusCreated {
		t.Fatalf("create lb: %d %s", code, body)
	}
	// Create an allocate mapping on the (always-on) https port.
	mbody := fmt.Sprintf(`{"listen_port":%d,"listen_tls":true,"allocate":true,"label":"web"}`, httpsPort)
	if code, body := call("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", mbody, token); code != http.StatusCreated {
		t.Fatalf("create mapping: %d %s", code, body)
	}
	// One port in use.
	if code, body := call("GET", "/api/v1/ports", "", token); code != 200 || !strings.Contains(body, `"used":1`) {
		t.Fatalf("ports: %d %s", code, body)
	}
	// Returning the in-use port -> 409.
	if code, _ := call("DELETE", "/api/v1/ports/9000", "", token); code != http.StatusConflict {
		t.Fatalf("return in-use: %d", code)
	}
	// Delete the LB -> 204; the auto port cascades free.
	if code, _ := call("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com", "", token); code != http.StatusNoContent {
		t.Fatalf("delete lb: %d", code)
	}
	if code, body := call("GET", "/api/v1/ports", "", token); code != 200 || !strings.Contains(body, `"used":0`) {
		t.Fatalf("ports after delete: %d %s", code, body)
	}
}
```

- [ ] **Step 2: Run the integration test**

Run: `go test ./cmd/rgdevenv/ -run TestManagementAPIEndToEnd -v -race`
Expected: PASS.

- [ ] **Step 3: Run the entire suite + lint**

Run: `go build ./... && go vet ./... && go test ./... -race && golangci-lint run && test -z "$(gofmt -l .)"`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add cmd/rgdevenv/integration_test.go
git commit -m "test(serve): end-to-end management API (CRUD + auth + cascade)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Definition of done (Phase 2a)

- [ ] `go build ./...`, `go vet ./...`, `go test ./... -race`, `golangci-lint run`, gofmt all pass.
- [ ] `rgdevenv serve` serves a bearer-protected `/api/v1/*` REST API + open `/healthz`
  at the management hostname (and the optional loopback/unix `management.bind`).
- [ ] Full CRUD works end-to-end via the real proxy: create/list/get/patch/delete
  LBs; create/replace/delete mappings (incl. `allocate` convenience + PUT
  body/path match); allocate/list/return ports (in-use → 409); list CAs; status.
- [ ] Every mutation is serialized, validated (structural + config-dependent), and
  applied atomically; a validation/conflict error never mutates `state.json`.
- [ ] Auto-port cascade frees orphaned allocations on mapping/LB delete and replace.
- [ ] Bearer auth (constant-time) + per-IP rate limit (429); unauthenticated only
  on `/healthz`.

## What Phase 2b adds (not in this plan)

`internal/health` (protocol-aware upstream health checks via the shared safe
dialer, with hysteresis; surfaced in `/status` and list responses — which report
`"unknown"` until then), `internal/ui` (the static login shell + dashboard served
at the management `/` path, currently a 404), and `internal/client` + the
`lb`/`map`/`port`/`ca`/`status` CLI subcommands. Phase 2b gets its own
spec-derived plan.










