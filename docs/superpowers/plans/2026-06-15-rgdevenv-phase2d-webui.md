# rgdevenv Phase 2d — Web Management UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a token-protected, single-page web dashboard with full CRUD parity to the CLI, served from the single Go binary at `/` with no JS build step and no new API endpoints.

**Architecture:** A new `internal/ui` package embeds a data-free static bundle (`index.html` + `app.css` + `app.js`) via `embed.FS` and serves it at `/` with a strict CSP. The browser's vanilla JavaScript fetches all data client-side from the existing `/api/v1/*` endpoints with a `Authorization: Bearer <token>` header (token kept only in `sessionStorage`). The bundle is mounted into the existing management mux (`internal/api`) **outside** the auth middleware; Go `ServeMux` longest-pattern precedence keeps `/api/v1/` and `/healthz` ahead of the `/` catch-all.

**Tech Stack:** Go 1.22 stdlib (`embed`, `net/http`, `io/fs`), vanilla ES2017 JavaScript (no framework, no build), CSS custom properties (`prefers-color-scheme`).

**Source spec:** `docs/superpowers/specs/2026-06-15-rgdevenv-phase2d-webui-design.md`

**Branch:** create and work on `phase2d-webui` (the executor's worktree/branch skill handles this); ff-merge to `master` when all tasks are green, per the established Phase 2a–2c pattern.

**Conventions (from CLAUDE.md + repo):** keep `gofmt`, `go vet ./...`, and `golangci-lint run` clean; preserve `AIDEV-*` anchors; commit only the steps below; every commit message ends with the trailer:

```
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## Wire shapes this UI consumes (reference — already implemented, do not change)

Verified against `internal/api` + `internal/store` + `internal/health`. The JSON tags ARE the contract.

**`GET /api/v1/lbs`** → `200`, array of:
```json
{ "name": "api.dev.example.com", "label": "Main API", "created_at": "2026-06-15T...Z",
  "mappings": [
    { "listen_port": 443, "listen_tls": true,
      "upstream": { "scheme": "https", "host": "localhost", "port": 8001,
                    "tls": { "mode": "verify", "ca_name": "" } },
      "allocation_id": "", "auto_allocated": false, "health": "up" } ] }
```
- `health` is always one of `"up" | "down" | "unknown"`.
- `label`, `allocation_id`, `auto_allocated`, `ca_name` are omitempty (may be absent).

**`POST /api/v1/lbs`** ← `{ "name": "<fqdn>", "label": "<optional>" }` → `201` (the new LB view). `name` required.
**`PATCH /api/v1/lbs/{name}`** ← `{ "label": "<value>" }` → `200`.
**`DELETE /api/v1/lbs/{name}`** → `204` (cascades mappings + auto-allocated ports).
**`POST /api/v1/lbs/{name}/mappings`** ← (see below) → `201`. `listen_port` defaults to 443, `listen_tls` defaults to true.
**`PUT /api/v1/lbs/{name}/mappings/{port}`** ← (see below) → `200`. If body has `listen_port` it must equal the path port.
**`DELETE /api/v1/lbs/{name}/mappings/{port}`** → `204`.

Mapping request body (both POST and PUT):
```json
{ "listen_port": 443, "listen_tls": true,
  "upstream": { "scheme": "http", "host": "localhost", "port": 3000,
                "tls": { "mode": "verify", "ca_name": "" } },
  "allocate": false, "label": "" }
```
- When `allocate: true`, **omit** `upstream` — the server auto-allocates a pool port and points the upstream at it; `label` becomes the allocation label.
- `tls.mode` is `verify | ca | skip` (defaults to `verify` when empty).
- Unknown JSON fields are rejected (`DisallowUnknownFields`), so send exactly these keys.

**`GET /api/v1/cas`** → `200`, JSON array of strings, e.g. `["corp","internal-ca"]`.
**`GET /api/v1/ports`** → `200`:
```json
{ "start": 9000, "end": 9099, "used": 2, "free": 98,
  "allocations": [ { "id": "...", "port": 9000, "owner": "app", "label": "vite",
                     "auto": false, "allocated_at": "2026-...Z" } ] }
```
**`POST /api/v1/ports/allocate`** ← `{ "owner": "<optional>", "label": "<optional>" }` → `201` `{ "id": "...", "port": 9000 }`.
**`DELETE /api/v1/ports/{port}`** → `204`.
**`GET /api/v1/status`** → `200`:
```json
{ "version": "0.1.0", "https_port": 443, "http_port": 80, "active_listeners": [443,8443],
  "load_balancers": 3, "mappings": 4, "allocations": 2, "upstreams": [ ... ] }
```
**Error body** (any non-2xx except 204): `{ "error": "<message>", "code": "<code>" }` with the HTTP status (`400` validation, `401` unauthorized, `404` not found, `409` conflict, `429` rate_limited, `500` internal).

---

## File structure

```
internal/ui/
  ui.go              # //go:embed assets ; Handler() http.Handler — CSP, content-types, 404-otherwise
  ui_test.go         # serving + security tests (Go)
  assets/
    index.html       # login view + dashboard skeleton; no inline JS/CSS, no data
    app.css          # light/dark palettes (prefers-color-scheme), two-column layout, components
    app.js           # auth/session, fetch wrapper, render, forms, polling, confirm dialog, error banner
internal/api/
  api.go             # MODIFY buildMux: mount ui.Handler() at "/" (outside authMiddleware)
  ui_mount_test.go   # NEW integration test: "/" serves shell; /api/v1/* auth unchanged
cmd/rgdevenv/
  serve_test.go      # MODIFY: mgmt-host "/" now returns 200 text/html (was 404 in phase 1)
cmd/uidev/
  main.go            # NEW, GITIGNORED dev-only harness: serves the handler + seeded data on 127.0.0.1:8088 over HTTP
```

`internal/api` imports `internal/ui`. `internal/ui` imports only the stdlib (no project packages) — keep it a leaf so there is no import cycle.

---

## Manual verification harness (used by Tasks 3–6)

The JS has no automated test harness (a conscious limitation in the spec). All JS verification is manual, against a throwaway loopback server that serves the **real** management handler (so the real `/` mount + `/api/v1/*` are exercised) over plain HTTP with seeded sample data — no TLS, no `/etc/hosts`, no root. It is created in Task 3, gitignored, and reused by Tasks 4–6.

Run it (in a second terminal) with:
```bash
go run ./cmd/uidev
# prints: rgdevenv UI dev server: http://127.0.0.1:8088/  (token: 0123456789abcdef0123456789abcdef)
```
Then open `http://127.0.0.1:8088/` and paste the printed token. Stop it with Ctrl-C. Quick-launch links point at `https://*.dev.example.com` hosts that don't resolve in dev — that's expected; don't click them to verify the dashboard.

---

### Task 1: `internal/ui` serving layer + stub assets (TDD)

**Files:**
- Create: `internal/ui/assets/index.html`, `internal/ui/assets/app.css`, `internal/ui/assets/app.js` (stubs; fleshed out in Tasks 3–5)
- Create: `internal/ui/ui.go`
- Test: `internal/ui/ui_test.go`

- [ ] **Step 1: Create stub assets** (the `//go:embed` directive needs these files to exist to compile)

`internal/ui/assets/index.html`:
```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>rgdevenv</title>
  <link rel="stylesheet" href="/app.css">
</head>
<body>
  <main id="login">loading…</main>
  <script src="/app.js" defer></script>
</body>
</html>
```

`internal/ui/assets/app.css`:
```css
/* rgdevenv dashboard styles (Task 4) */
```

`internal/ui/assets/app.js`:
```javascript
// rgdevenv dashboard (Task 5)
```

- [ ] **Step 2: Write the failing tests**

`internal/ui/ui_test.go`:
```go
package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestRootServesShell(t *testing.T) {
	w := get(t, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET / content-type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"<title>rgdevenv</title>", "/app.js", "/app.css"} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing %q", want)
		}
	}
}

func TestAssetContentTypes(t *testing.T) {
	if ct := get(t, "/app.css").Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Fatalf("app.css content-type = %q", ct)
	}
	if ct := get(t, "/app.js").Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("app.js content-type = %q", ct)
	}
}

func TestCSPHeaderPresent(t *testing.T) {
	const want = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:"
	if got := get(t, "/").Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("CSP = %q, want %q", got, want)
	}
}

func TestUnknownPath404(t *testing.T) {
	for _, p := range []string{"/secret.txt", "/assets/index.html", "/../ui.go"} {
		if w := get(t, p); w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", p, w.Code)
		}
	}
}

// TestShellIsDataFree guards the model's core security property: the served shell
// carries no runtime data and no token. Handler() takes no data source, so this is
// structurally true; the assertion is a regression guard against anyone later
// inlining data or a token into index.html.
func TestShellIsDataFree(t *testing.T) {
	body := get(t, "/").Body.String()
	for _, forbidden := range []string{"Bearer", "Authorization", "load_balancers", "sessionStorage"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("shell unexpectedly contains %q — the served HTML must be data-free", forbidden)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/ui/`
Expected: FAIL — `undefined: Handler`.

- [ ] **Step 4: Implement `internal/ui/ui.go`**

```go
// Package ui serves the rgdevenv web dashboard: a data-free static bundle
// (index.html + app.css + app.js) embedded in the binary. It performs NO
// authentication — the bundle carries no data; the browser fetches everything
// from /api/v1/* with a bearer token kept only in sessionStorage (§14, §15).
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

//go:embed assets
var assets embed.FS

// contentSecurityPolicy is the strict CSP applied to every served asset (§15).
// It forbids inline scripts/styles, which is why app.css and app.js are separate
// embedded files. connect-src 'self' permits the dashboard's same-origin fetches
// to /api/v1/*.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:"

type file struct {
	body  []byte
	ctype string
}

// Handler returns the dashboard handler. "/" serves index.html; "/app.css" and
// "/app.js" serve those assets; every other path is 404 (no directory listing,
// no traversal — paths resolve by exact map lookup, not the filesystem).
//
// AIDEV-NOTE: panics here are startup programmer errors (a missing/renamed
// embedded asset), surfaced immediately rather than as a runtime 404.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic("ui: embed sub: " + err.Error())
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		panic("ui: read embedded assets: " + err.Error())
	}
	files := make(map[string]file, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(sub, e.Name())
		if err != nil {
			panic("ui: read asset " + e.Name() + ": " + err.Error())
		}
		files["/"+e.Name()] = file{body: b, ctype: ctypeFor(e.Name())}
	}
	index, ok := files["/index.html"]
	if !ok {
		panic("ui: assets/index.html missing")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if r.URL.Path == "/" {
			serve(w, index)
			return
		}
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serve(w, f)
	})
}

func serve(w http.ResponseWriter, f file) {
	w.Header().Set("Content-Type", f.ctype)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.body)
}

func ctypeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ui/`
Expected: PASS (all 5 tests).

- [ ] **Step 6: Format, vet, commit**

```bash
gofmt -w internal/ui/ui.go internal/ui/ui_test.go
go vet ./internal/ui/
git add internal/ui/
git commit -m "feat(ui): embedded data-free dashboard shell handler with strict CSP

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```
Expected: vet clean; commit succeeds.

---

### Task 2: Mount the UI in the management mux + fix the phase-1 root test (TDD)

**Files:**
- Modify: `internal/api/api.go` (add import; replace the `AIDEV-TODO(phase2b)` seam at the end of `buildMux`)
- Modify: `cmd/rgdevenv/serve_test.go:126-129` (mgmt root now 200, not 404)
- Test: `internal/api/ui_mount_test.go` (new)

- [ ] **Step 1: Write the failing integration test**

`internal/api/ui_mount_test.go`:
```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func unauthReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:1"
	return r
}

func TestUIServedAtRootWithoutAuth(t *testing.T) {
	h := newAPITestHandler(t)
	w := do(h, unauthReq("GET", "/"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content-type = %q, want text/html", ct)
	}
}

func TestUIMountDoesNotShadowAPIAuth(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, unauthReq("GET", "/api/v1/lbs")); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /api/v1/lbs = %d, want 401 (UI mount must not bypass auth)", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs", "")); w.Code != http.StatusOK {
		t.Fatalf("auth /api/v1/lbs = %d, want 200", w.Code)
	}
}

func TestHealthzStillOpenAfterUIMount(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, unauthReq("GET", "/healthz")); w.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", w.Code)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestUI`
Expected: FAIL — `TestUIServedAtRootWithoutAuth` gets `404` (nothing is mounted at `/` yet).

- [ ] **Step 3: Add the import to `internal/api/api.go`**

In the import block (currently lines 5-15), add the ui import (keep the block gofmt-sorted):
```go
	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/ui"
```

- [ ] **Step 4: Mount the UI at the `buildMux` seam**

In `internal/api/api.go`, replace this (end of `buildMux`):
```go
	mux.Handle("/api/v1/", h.authMiddleware(api))
	// AIDEV-TODO(phase2b): mount the static login shell + dashboard at "/".
	return mux
```
with:
```go
	mux.Handle("/api/v1/", h.authMiddleware(api))
	// AIDEV-NOTE: the dashboard shell mounts at "/" OUTSIDE authMiddleware — it is
	// data-free static assets (Phase 2d, §14). Go ServeMux longest-pattern precedence
	// keeps "/api/v1/" and "/healthz" ahead of this root catch-all, so the mount does
	// not bypass auth (asserted by ui_mount_test.go).
	mux.Handle("/", ui.Handler())
	return mux
```

- [ ] **Step 5: Run the integration test to verify it passes**

Run: `go test ./internal/api/ -run TestUI`
Expected: PASS.

- [ ] **Step 6: Update the phase-1 mgmt-root assertion in `cmd/rgdevenv/serve_test.go`**

The root `/` on the management host now serves the shell. Replace this block (around lines 125-129):
```go
	resp.Body.Close()
	// TLS handshake proves the cert loaded + listener bound; mgmt host 404s in phase 1.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
```
with:
```go
	// TLS handshake proves the cert loaded + listener bound; the mgmt-host root now
	// serves the static dashboard shell (Phase 2d).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("root content-type = %q, want text/html; charset=utf-8", ct)
	}
	resp.Body.Close()
```
(Note: the `resp.Body.Close()` moved below the header read; reading a header after close is fine.)

- [ ] **Step 7: Run the full affected suites**

Run: `go test ./internal/api/ ./cmd/rgdevenv/`
Expected: PASS (the updated serve test now expects 200 + html; everything else unchanged).

- [ ] **Step 8: Format, vet, commit**

```bash
gofmt -w internal/api/api.go internal/api/ui_mount_test.go cmd/rgdevenv/serve_test.go
go vet ./internal/api/ ./cmd/rgdevenv/
git add internal/api/api.go internal/api/ui_mount_test.go cmd/rgdevenv/serve_test.go
git commit -m "feat(api): mount the dashboard shell at / outside auth middleware

Go ServeMux longest-pattern precedence keeps /api/v1/ and /healthz ahead of the
root catch-all; the mgmt-host root now serves the static shell (was 404 in phase 1).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Manual dev harness (gitignored) + the full `index.html` shell

**Files:**
- Modify: `.gitignore` (ignore `cmd/uidev/`)
- Create: `cmd/uidev/main.go` (dev-only, never committed)
- Modify: `internal/ui/assets/index.html` (replace the Task-1 stub with the full shell)

- [ ] **Step 1: Gitignore the dev harness**

Append to `.gitignore`:
```gitignore

# Manual UI dev harness (Phase 2d) — never committed
/cmd/uidev/
```

- [ ] **Step 2: Create the dev harness**

`cmd/uidev/main.go`:
```go
// Command uidev serves the rgdevenv management handler (dashboard UI + API) over
// plain HTTP on loopback with seeded sample data, for MANUAL dashboard development.
// It is NOT part of the product: gitignored, never committed. Stop with Ctrl-C.
package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/realgo/rgdevenv/internal/api"
	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

const (
	devToken = "0123456789abcdef0123456789abcdef"
	addr     = "127.0.0.1:8088"
)

// fakeHealth gives the seeded mappings a deterministic mix of up/down/unknown so
// the health dots and per-mapping health render with variety.
type fakeHealth struct{}

func (fakeHealth) Status(u store.Upstream) health.Status {
	switch u.Port {
	case 8001:
		return health.Up
	case 3000:
		return health.Down
	default:
		return health.Unknown
	}
}

func (fakeHealth) List() []health.Entry {
	return []health.Entry{
		{Scheme: "https", Host: "localhost", Port: 8001, TLSMode: "verify", Health: health.Up},
		{Scheme: "http", Host: "localhost", Port: 3000, Health: health.Down},
	}
}

func main() {
	dir, err := os.MkdirTemp("", "uidev")
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	caDir := filepath.Join(dir, "ca")
	if err := os.MkdirAll(caDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caDir, "corp.pem"), []byte("x"), 0o600); err != nil {
		log.Fatal(err)
	}

	mgr := txn.New(st, func(*store.State) {}, func(string) bool { return true },
		upstream.NewPolicy(nil), txn.Config{
			PoolStart: 9000, PoolEnd: 9099, HTTPSPort: 443, HTTPPort: 80,
			MgmtHost: "rgdevenv.dev.example.com", CADir: caDir,
		})
	seed(mgr)

	h := api.New(api.Deps{
		Txn:         mgr,
		Auth:        auth.NewAuthenticator(devToken),
		Limiter:     auth.NewRateLimiter(1000, time.Minute),
		CADir:       caDir,
		Version:     "dev",
		HTTPSPort:   443,
		HTTPPort:    80,
		PoolStart:   9000,
		PoolEnd:     9099,
		ActivePorts: func() []int { return []int{443, 8443} },
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Health:      fakeHealth{},
	})

	log.Printf("rgdevenv UI dev server: http://%s/  (token: %s)", addr, devToken)
	log.Fatal(http.ListenAndServe(addr, h))
}

func seed(mgr *txn.Manager) {
	must := func(_ *store.State, err error) {
		if err != nil {
			log.Fatalf("seed: %v", err)
		}
	}
	must(mgr.CreateLB("api.dev.example.com", "Main API"))
	must(mgr.PutMapping("api.dev.example.com", txn.MappingSpec{
		ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "https", Host: "localhost", Port: 8001,
			TLS: store.UpstreamTLS{Mode: "verify"}},
	}, false))

	must(mgr.CreateLB("blog.dev.example.com", "Blog (staging)"))
	must(mgr.PutMapping("blog.dev.example.com", txn.MappingSpec{
		ListenPort: 8443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 3000},
	}, false))

	must(mgr.CreateLB("app.dev.example.com", "App"))
	must(mgr.PutMapping("app.dev.example.com", txn.MappingSpec{
		ListenPort: 443, ListenTLS: true,
		Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 5173},
	}, false))
	must(mgr.PutMapping("app.dev.example.com", txn.MappingSpec{
		ListenPort: 8080, ListenTLS: false, Allocate: true, AllocLabel: "vite dev",
	}, false))

	if _, _, err := mgr.AllocatePort("api", "worker"); err != nil {
		log.Fatalf("seed alloc: %v", err)
	}
}
```

- [ ] **Step 3: Verify the harness builds and serves**

Run: `go run ./cmd/uidev` (in a separate terminal; leave it running for Steps 4–5)
Expected: logs `rgdevenv UI dev server: http://127.0.0.1:8088/  (token: 0123456789abcdef0123456789abcdef)` and stays running.

Sanity-check the API path with curl (separate terminal):
```bash
curl -s -H "Authorization: Bearer 0123456789abcdef0123456789abcdef" http://127.0.0.1:8088/api/v1/lbs | head -c 200
```
Expected: a JSON array beginning `[{"name":"api.dev.example.com"...`.

- [ ] **Step 4: Replace the stub `index.html` with the full shell**

`internal/ui/assets/index.html`:
```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>rgdevenv</title>
  <link rel="stylesheet" href="/app.css">
</head>
<body>
  <!-- LOGIN VIEW (visible by default; app.js hides it once authenticated) -->
  <main id="login" class="login">
    <form id="login-form" class="login-card" autocomplete="off">
      <h1>rgdevenv</h1>
      <p class="muted">Enter your management token to continue.</p>
      <input id="token" type="password" placeholder="Management token" autocomplete="off" required>
      <button class="btn-primary" type="submit">Connect</button>
      <p id="login-error" class="error-text" hidden></p>
    </form>
  </main>

  <!-- DASHBOARD VIEW -->
  <div id="dashboard" hidden>
    <header class="topbar">
      <span class="brand">rgdevenv</span>
      <span class="run"><span id="run-dot" class="dot dot-unknown"></span> Running</span>
      <span class="muted">Listeners <span id="listeners">—</span></span>
      <span class="spacer"></span>
      <span id="version" class="muted"></span>
      <button id="refresh" class="btn-ghost" type="button">Refresh</button>
      <button id="logout" class="btn-ghost" type="button">Logout</button>
    </header>

    <div id="error-banner" class="banner" hidden>
      <span id="error-text"></span>
      <button id="error-dismiss" class="btn-ghost" type="button">Dismiss</button>
    </div>

    <div class="columns">
      <section class="col col-lbs">
        <div class="col-head">
          <h2>Load balancers</h2>
          <button id="add-lb" class="btn-primary" type="button">+ Add load balancer</button>
        </div>
        <div id="lb-list"></div>
      </section>

      <section class="col col-ports">
        <h2>Port pool</h2>
        <div id="port-pool" class="card"></div>
      </section>
    </div>
  </div>

  <!-- REUSABLE CONFIRM DIALOG -->
  <div id="confirm-overlay" class="overlay" hidden>
    <div class="dialog" role="alertdialog" aria-modal="true">
      <p id="confirm-message"></p>
      <div class="dialog-actions">
        <button id="confirm-cancel" class="btn-ghost" type="button">Cancel</button>
        <button id="confirm-ok" class="btn-danger" type="button">Confirm</button>
      </div>
    </div>
  </div>

  <script src="/app.js" defer></script>
</body>
</html>
```

- [ ] **Step 5: Verify the shell serves and is still data-free**

Run: `go test ./internal/ui/`
Expected: PASS (the full shell keeps the title + `/app.js` + `/app.css` references and contains none of the forbidden data markers).

Manual: refresh `http://127.0.0.1:8088/` in the browser.
Expected: an **unstyled** login form — "rgdevenv" heading, a "Management token" password field, and a "Connect" button. (The dashboard is hidden; app.js is still a stub, so the form does nothing yet. CSS comes in Task 4.)

- [ ] **Step 6: Commit (the gitignored `cmd/uidev/` is intentionally excluded)**

```bash
gofmt -l cmd/uidev/main.go   # expect no output (well-formatted)
go vet ./cmd/uidev/
git add internal/ui/assets/index.html .gitignore
git status --short           # confirm cmd/uidev/ is NOT staged (gitignored)
git commit -m "feat(ui): full login + dashboard shell markup

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `app.css` — light/dark theme, two-column layout, components

**Files:**
- Modify: `internal/ui/assets/app.css` (replace the stub)

- [ ] **Step 1: Write the full stylesheet**

`internal/ui/assets/app.css`:
```css
/* rgdevenv dashboard — automatic light/dark via prefers-color-scheme, desktop-first.
   No inline styles anywhere (strict CSP); all presentation lives here. */

:root {
  --bg: #f1f5f9;
  --panel: #ffffff;
  --text: #0f172a;
  --muted: #64748b;
  --border: #e2e8f0;
  --primary: #2563eb;
  --primary-text: #ffffff;
  --danger: #ef4444;
  --topbar: #1e293b;
  --topbar-text: #e2e8f0;
  --chip-bg: #f1f5f9;
  --badge-bg: #dbeafe;
  --badge-text: #1e40af;
  --bar-bg: #e2e8f0;
  --up: #22c55e;
  --down: #ef4444;
  --unknown: #f59e0b;
  --overlay: rgba(15, 23, 42, 0.45);
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0b1220;
    --panel: #111a2b;
    --text: #e2e8f0;
    --muted: #94a3b8;
    --border: #1e293b;
    --primary: #3b82f6;
    --topbar: #0f172a;
    --topbar-text: #e2e8f0;
    --chip-bg: #1e293b;
    --badge-bg: #1e3a8a;
    --badge-text: #bfdbfe;
    --bar-bg: #1e293b;
    --overlay: rgba(0, 0, 0, 0.6);
  }
}

* { box-sizing: border-box; }

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 14px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}

h1 { font-size: 20px; margin: 0 0 4px; }
h2 { font-size: 15px; margin: 0; }
.muted { color: var(--muted); }
.small { font-size: 12px; }
.spacer { flex: 1; }
.right { text-align: right; }

/* ---- buttons ---- */
button { font: inherit; cursor: pointer; }
.btn-primary {
  background: var(--primary); color: var(--primary-text);
  border: 0; border-radius: 6px; padding: 7px 13px;
}
.btn-outline {
  background: transparent; color: var(--primary);
  border: 1px solid var(--primary); border-radius: 6px; padding: 7px 13px; width: 100%;
}
.btn-ghost {
  background: transparent; color: var(--muted);
  border: 1px solid var(--border); border-radius: 6px; padding: 6px 11px;
}
.btn-danger {
  background: var(--danger); color: #fff;
  border: 0; border-radius: 6px; padding: 7px 13px;
}
.link {
  background: none; border: 0; padding: 2px 5px;
  color: var(--primary); font-size: 13px;
}
.link.danger { color: var(--danger); }

input, select {
  font: inherit; color: var(--text); background: var(--panel);
  border: 1px solid var(--border); border-radius: 6px; padding: 6px 9px;
}
input:disabled { opacity: 0.5; }

/* ---- login ---- */
.login { min-height: 100vh; display: flex; align-items: center; justify-content: center; }
.login-card {
  background: var(--panel); border: 1px solid var(--border); border-radius: 12px;
  padding: 28px; width: 320px; display: flex; flex-direction: column; gap: 12px;
}
.error-text { color: var(--danger); font-size: 13px; margin: 0; }

/* ---- topbar ---- */
.topbar {
  display: flex; align-items: center; gap: 16px;
  background: var(--topbar); color: var(--topbar-text); padding: 11px 18px;
}
.topbar .brand { font-weight: 700; font-size: 17px; }
.topbar .muted { color: #94a3b8; }
.topbar .run { display: flex; align-items: center; gap: 6px; font-size: 13px; }

/* ---- error banner ---- */
.banner {
  display: flex; align-items: center; gap: 12px;
  background: #fef2f2; color: #991b1b; border-bottom: 1px solid #fecaca;
  padding: 10px 18px;
}
.banner .spacer { flex: 1; }

/* ---- columns ---- */
.columns { display: flex; gap: 18px; padding: 18px; align-items: flex-start; }
.col-lbs { flex: 1.7; min-width: 0; }
.col-ports { flex: 1; min-width: 0; }
.col-head { display: flex; align-items: center; margin-bottom: 12px; }
.col-head h2 { flex: 1; }

@media (max-width: 820px) {
  .columns { flex-direction: column; }
  .col-lbs, .col-ports { width: 100%; flex: none; }
}

/* ---- status dots ---- */
.dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; flex: none; }
.dot-up { background: var(--up); }
.dot-down { background: var(--down); }
.dot-unknown { background: var(--unknown); }

/* ---- load-balancer rows ---- */
.lb-row {
  background: var(--panel); border: 1px solid var(--border); border-radius: 8px;
  padding: 12px 14px; margin-bottom: 10px;
}
.lb-row.open { border-color: var(--primary); }
.lb-head { display: flex; align-items: center; gap: 10px; }
.lb-name { color: var(--primary); text-decoration: none; font-weight: 600; }
.chips { margin-top: 8px; display: flex; gap: 6px; flex-wrap: wrap; }
.chip {
  background: var(--chip-bg); border: 1px solid var(--border); border-radius: 14px;
  padding: 3px 10px; font-size: 12px; color: var(--text); text-decoration: none;
}
.badge {
  background: var(--badge-bg); color: var(--badge-text);
  border-radius: 8px; padding: 1px 6px; margin-left: 5px; font-size: 11px;
}
.empty { padding: 8px 2px; }

/* ---- tables (mappings + ports) ---- */
table.grid { width: 100%; border-collapse: collapse; font-size: 12.5px; margin-top: 10px; }
table.grid th { text-align: left; font-weight: 500; color: var(--muted); padding: 5px 6px; }
table.grid td { padding: 6px; border-top: 1px solid var(--border); }
.health-up { color: var(--up); }
.health-down { color: var(--down); }
.health-unknown { color: var(--unknown); }

/* ---- inline forms ---- */
.panel { margin-top: 6px; }
.inline-form {
  display: flex; gap: 6px; flex-wrap: wrap; align-items: center;
  background: var(--bg); border: 1px dashed var(--border); border-radius: 6px;
  padding: 8px; margin-top: 10px;
}
.inline-form .grow { flex: 1; min-width: 160px; }
.inline-form .w-port { width: 100px; }
.inline-form .chk { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--muted); }
.label-edit .lb-name { color: var(--text); }

/* ---- port pool card ---- */
.card { background: var(--panel); border: 1px solid var(--border); border-radius: 8px; padding: 14px; }
.bar { height: 10px; background: var(--bar-bg); border-radius: 6px; overflow: hidden; margin: 10px 0; }
.bar-fill { height: 100%; background: var(--primary); }
.alloc { margin-top: 12px; }

/* ---- confirm dialog ---- */
.overlay {
  position: fixed; inset: 0; background: var(--overlay);
  display: flex; align-items: center; justify-content: center;
}
.dialog {
  background: var(--panel); border-radius: 10px; padding: 20px;
  width: 360px; max-width: calc(100vw - 32px);
}
.dialog p { margin: 0 0 16px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; }
```

- [ ] **Step 2: Verify the Go tests still pass and eyeball the styling**

Run: `go test ./internal/ui/`
Expected: PASS (CSS content-type still `text/css; charset=utf-8`).

Manual (harness still running from Task 3, or restart `go run ./cmd/uidev`): hard-refresh `http://127.0.0.1:8088/`.
Expected: a centered, styled login card. Toggle your OS between light and dark appearance and refresh — the palette switches (light slate vs. dark navy). (The form still does nothing; app.js is Task 5.)

- [ ] **Step 3: Commit**

```bash
git add internal/ui/assets/app.css
git commit -m "feat(ui): dashboard stylesheet with auto light/dark and two-column layout

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: `app.js` — auth, fetch, rendering, CRUD, polling, dialogs

This is the one cohesive, non-unit-testable file (per the spec). Write it complete, then verify the whole feature set manually against the dev harness. Every event listener is attached programmatically (CSP forbids inline handlers); DOM is built with the `el()` helper (no `innerHTML` for interactive nodes).

**Files:**
- Modify: `internal/ui/assets/app.js` (replace the stub)

- [ ] **Step 1: Write the full `app.js`**

`internal/ui/assets/app.js`:
```javascript
'use strict';
// rgdevenv dashboard. Vanilla JS, no build step. All data is fetched client-side
// from /api/v1/* with a bearer token kept ONLY in sessionStorage. The CSP forbids
// inline scripts, so every event listener is attached programmatically here.

const POLL_MS = 5000;
const TOKEN_KEY = 'rgdevenv.token';
const AUTH = Symbol('auth-required'); // thrown by api() on 401; action handlers ignore it

// UI-only module state (never the source of truth — the API is).
const expanded = new Set();     // lb names whose mapping panel is open
const labelEditing = new Set(); // lb names whose label is being edited
let addingLB = false;           // the "add load balancer" form is open
let cas = [];                   // CA names for the upstream-TLS dropdown
let pollTimer = null;

// ---- tiny DOM helpers ------------------------------------------------------
function byId(id) { return document.getElementById(id); }
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

// el(tag, props, ...children): props supports class, text, onX listeners, boolean
// and string attributes. Children may be nodes or strings.
function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v == null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on') && typeof v === 'function') n.addEventListener(k.slice(2), v);
      else if (v === true) n.setAttribute(k, '');
      else n.setAttribute(k, v);
    }
  }
  for (const kid of kids) {
    if (kid == null) continue;
    n.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return n;
}

function swallow(e) { if (e !== AUTH) showError((e && e.message) || String(e)); }

// ---- session + fetch -------------------------------------------------------
function getToken() { return sessionStorage.getItem(TOKEN_KEY); }
function setToken(t) { sessionStorage.setItem(TOKEN_KEY, t); }
function clearToken() { sessionStorage.removeItem(TOKEN_KEY); }

async function api(method, path, body) {
  const opts = { method, headers: {} };
  const tok = getToken();
  if (tok) opts.headers['Authorization'] = 'Bearer ' + tok;
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 401) { handleUnauthorized(); throw AUTH; }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch (_) { /* non-JSON body */ } }
  if (!res.ok) {
    throw new Error((data && data.error) || (method + ' ' + path + ' failed (' + res.status + ')'));
  }
  return data;
}

function handleUnauthorized() {
  clearToken();
  stopPolling();
  showLogin();
}

// ---- views -----------------------------------------------------------------
function showLogin() {
  byId('dashboard').hidden = true;
  byId('login').hidden = false;
  byId('token').value = '';
  byId('token').focus();
}
function showDashboard() {
  byId('login').hidden = true;
  byId('dashboard').hidden = false;
}

// ---- error banner ----------------------------------------------------------
function showError(msg) { byId('error-text').textContent = msg; byId('error-banner').hidden = false; }
function clearError() { byId('error-banner').hidden = true; }

// ---- reusable confirm dialog -----------------------------------------------
let confirmResolver = null;
function confirmAction(message) {
  byId('confirm-message').textContent = message;
  byId('confirm-overlay').hidden = false;
  return new Promise((resolve) => { confirmResolver = resolve; });
}
function settleConfirm(ok) {
  byId('confirm-overlay').hidden = true;
  const r = confirmResolver;
  confirmResolver = null;
  if (r) r(ok);
}

// ---- polling (suspended while a field is focused or dirty) ------------------
function fieldFocused() {
  const a = document.activeElement;
  return !!a && (a.tagName === 'INPUT' || a.tagName === 'SELECT' || a.tagName === 'TEXTAREA');
}
function hasDirtyField() {
  for (const i of document.querySelectorAll('#dashboard input, #dashboard select')) {
    if (i.type === 'checkbox') { if (i.checked) return true; }
    else if (i.value && i.value.trim() !== '') return true;
  }
  return false;
}
// busy() is stateless (reads the DOM) so it can never leak a stuck "editing" flag:
// a background poll is skipped only while the user is actually mid-input.
function busy() { return fieldFocused() || hasDirtyField(); }

function startPolling() { stopPolling(); pollTimer = setInterval(poll, POLL_MS); }
function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
async function poll() {
  if (busy()) return;
  try { await refresh(); } catch (e) { if (e !== AUTH) setRunDot(false); }
}

async function refresh() {
  const [lbs, ports, status] = await Promise.all([
    api('GET', '/api/v1/lbs'),
    api('GET', '/api/v1/ports'),
    api('GET', '/api/v1/status'),
  ]);
  setRunDot(true);
  renderHeader(status);
  renderLBs(lbs || []);
  renderPorts(ports);
}

function setRunDot(up) { byId('run-dot').className = 'dot ' + (up ? 'dot-up' : 'dot-down'); }

// ---- header ----------------------------------------------------------------
function renderHeader(status) {
  byId('version').textContent = status && status.version ? 'v' + status.version : '';
  const ls = ((status && status.active_listeners) || []).map((p) => ':' + p).join(' · ');
  byId('listeners').textContent = ls || '—';
}

// ---- health + link helpers -------------------------------------------------
function worstHealth(maps) {
  let any = false;
  let worst = 'up';
  for (const m of maps || []) {
    any = true;
    const h = m.health || 'unknown';
    if (h === 'down') return 'down';
    if (h === 'unknown') worst = 'unknown';
  }
  return any ? worst : 'unknown';
}
function launchURL(name, m) {
  const scheme = m.listen_tls ? 'https' : 'http';
  const def = m.listen_tls ? 443 : 80;
  const port = (m.listen_port && m.listen_port !== def) ? ':' + m.listen_port : '';
  return scheme + '://' + name + port;
}
function sortedMappings(lb) {
  return (lb.mappings || []).slice().sort((a, b) => a.listen_port - b.listen_port);
}

// ---- load balancers (left column) ------------------------------------------
function renderLBs(lbs) {
  const list = byId('lb-list');
  clear(list);
  if (addingLB) list.append(renderAddLBForm());
  if (!lbs.length && !addingLB) {
    list.append(el('p', { class: 'muted empty' }, 'No load balancers yet.'));
    return;
  }
  for (const lb of lbs.slice().sort((a, b) => a.name.localeCompare(b.name))) {
    list.append(renderLBRow(lb));
  }
}

function renderLBRow(lb) {
  if (labelEditing.has(lb.name)) return renderLabelEditor(lb);
  const maps = sortedMappings(lb);
  const worst = worstHealth(maps);
  const open = expanded.has(lb.name);

  const name = maps.length
    ? el('a', { class: 'lb-name', href: launchURL(lb.name, maps[0]), target: '_blank', rel: 'noopener' }, lb.name + ' ↗')
    : el('span', { class: 'lb-name' }, lb.name);

  const head = el('div', { class: 'lb-head' },
    el('span', { class: 'dot dot-' + worst, title: worst }),
    name,
    lb.label ? el('span', { class: 'muted' }, lb.label) : null,
    el('span', { class: 'spacer' }),
    el('button', { class: 'link', type: 'button', title: 'mappings', onclick: () => toggleExpand(lb.name) }, open ? '− mappings' : '+ map'),
    el('button', { class: 'link', type: 'button', title: 'edit label', onclick: () => startLabelEdit(lb.name) }, '✎'),
    el('button', { class: 'link danger', type: 'button', title: 'delete', onclick: () => deleteLB(lb) }, '🗑'),
  );

  const chips = el('div', { class: 'chips' });
  for (const m of maps) chips.append(renderChip(lb.name, m));

  const row = el('div', { class: 'lb-row' + (open ? ' open' : '') }, head, chips);
  if (open) row.append(renderMappingsPanel(lb, maps));
  return row;
}

function renderChip(name, m) {
  const badge = (m.upstream && m.upstream.scheme === 'https')
    ? el('span', { class: 'badge' }, '🔒 ' + (m.upstream.tls ? (m.upstream.tls.mode || 'verify') : 'verify'))
    : null;
  const label = m.listen_port + ' → ' + (m.upstream ? m.upstream.host + ':' + m.upstream.port : '—');
  return el('a', { class: 'chip', href: launchURL(name, m), target: '_blank', rel: 'noopener' }, label, badge);
}

function toggleExpand(name) {
  if (expanded.has(name)) expanded.delete(name); else expanded.add(name);
  refresh().catch(swallow);
}

// ---- add / edit-label / delete load balancer -------------------------------
function renderAddLBForm() {
  const name = el('input', { type: 'text', placeholder: 'hostname (app.dev.example.com)' });
  const label = el('input', { type: 'text', placeholder: 'label (optional)' });
  const form = el('form', { class: 'inline-form add-lb' },
    name, label,
    el('button', { class: 'btn-primary', type: 'submit' }, 'Create'),
    el('button', { class: 'btn-ghost', type: 'button', onclick: () => { addingLB = false; refresh().catch(swallow); } }, 'Cancel'),
  );
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await api('POST', '/api/v1/lbs', { name: name.value.trim(), label: label.value.trim() });
      addingLB = false;
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });
  setTimeout(() => name.focus(), 0);
  return form;
}

function startLabelEdit(name) { labelEditing.add(name); refresh().catch(swallow); }

function renderLabelEditor(lb) {
  const input = el('input', { type: 'text', value: lb.label || '', placeholder: 'label' });
  const form = el('form', { class: 'inline-form label-edit' },
    el('span', { class: 'dot dot-' + worstHealth(sortedMappings(lb)) }),
    el('span', { class: 'lb-name' }, lb.name),
    input,
    el('button', { class: 'btn-primary', type: 'submit' }, 'Save'),
    el('button', { class: 'btn-ghost', type: 'button', onclick: () => { labelEditing.delete(lb.name); refresh().catch(swallow); } }, 'Cancel'),
  );
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await api('PATCH', '/api/v1/lbs/' + encodeURIComponent(lb.name), { label: input.value.trim() });
      labelEditing.delete(lb.name);
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });
  setTimeout(() => input.focus(), 0);
  return el('div', { class: 'lb-row' }, form);
}

async function deleteLB(lb) {
  const ok = await confirmAction('Delete load balancer "' + lb.name + '"? Its mappings and any auto-allocated ports will be removed.');
  if (!ok) return;
  try {
    await api('DELETE', '/api/v1/lbs/' + encodeURIComponent(lb.name));
    expanded.delete(lb.name);
    labelEditing.delete(lb.name);
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- mappings panel + add/replace/delete form ------------------------------
function mapURL(name, port) {
  const base = '/api/v1/lbs/' + encodeURIComponent(name) + '/mappings';
  return port != null ? base + '/' + port : base;
}

function renderMappingsPanel(lb, maps) {
  const form = renderMappingForm(lb); // built first so row ✎ buttons can prefill it
  const tbody = el('tbody');
  for (const m of maps) {
    tbody.append(el('tr', null,
      el('td', null, m.listen_port + ' ', el('span', { class: 'muted' }, m.listen_tls ? 'https' : 'http')),
      el('td', null, m.upstream ? m.upstream.host + ':' + m.upstream.port : '—',
        m.auto_allocated ? el('span', { class: 'badge' }, 'allocated') : null),
      el('td', { class: 'muted' }, m.upstream && m.upstream.scheme === 'https' ? (m.upstream.tls.mode || 'verify') : '—'),
      el('td', null, el('span', { class: 'health health-' + (m.health || 'unknown') }, '● ' + (m.health || 'unknown'))),
      el('td', { class: 'right' },
        el('button', { class: 'link', type: 'button', title: 'replace', onclick: () => form._fill(m) }, '✎'),
        el('button', { class: 'link danger', type: 'button', title: 'delete', onclick: () => deleteMapping(lb, m) }, '🗑')),
    ));
  }
  const table = el('table', { class: 'grid' },
    el('thead', null, el('tr', null,
      el('th', null, 'Listen'), el('th', null, 'Upstream'), el('th', null, 'TLS'),
      el('th', null, 'Health'), el('th', null, ''))),
    tbody);
  return el('div', { class: 'panel' }, table, form);
}

function renderMappingForm(lb) {
  const port = el('input', { type: 'number', min: '1', max: '65535', placeholder: 'listen port', class: 'w-port' });
  const url = el('input', { type: 'text', placeholder: 'upstream URL (http://localhost:3000)', class: 'grow' });
  const mode = el('select', null,
    el('option', { value: 'verify' }, 'verify'),
    el('option', { value: 'ca' }, 'ca'),
    el('option', { value: 'skip' }, 'skip'));
  const ca = el('select', null, el('option', { value: '' }, 'CA: none'));
  for (const c of cas) ca.append(el('option', { value: c }, c));
  const allocate = el('input', { type: 'checkbox' });
  const submit = el('button', { class: 'btn-primary', type: 'submit' }, 'Add');

  const form = el('form', { class: 'inline-form map-form' },
    port, url, mode, ca,
    el('label', { class: 'chk' }, allocate, ' allocate'),
    submit);
  form._editPort = null; // non-null → replace (PUT) that listen port

  allocate.addEventListener('change', () => { url.disabled = allocate.checked; });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const body = buildMappingBody(port.value, url.value, mode.value, ca.value, allocate.checked);
      if (form._editPort != null) await api('PUT', mapURL(lb.name, form._editPort), body);
      else await api('POST', mapURL(lb.name), body);
      clearError();
      await refresh();
    } catch (err) { swallow(err); }
  });

  // _fill prefills this panel's form from an existing mapping for a replace (PUT).
  form._fill = (m) => {
    form._editPort = m.listen_port;
    port.value = m.listen_port;
    port.readOnly = true;
    url.value = m.upstream ? (m.upstream.scheme + '://' + m.upstream.host + ':' + m.upstream.port) : '';
    mode.value = (m.upstream && m.upstream.tls && m.upstream.tls.mode) || 'verify';
    ca.value = (m.upstream && m.upstream.tls && m.upstream.tls.ca_name) || '';
    allocate.checked = !!m.auto_allocated;
    url.disabled = allocate.checked;
    submit.textContent = 'Save';
    url.focus();
  };
  return form;
}

function buildMappingBody(portStr, urlStr, mode, caName, allocate) {
  const body = {};
  if (String(portStr).trim() !== '') body.listen_port = parseInt(portStr, 10);
  if (allocate) {
    body.allocate = true;
  } else {
    const up = parseUpstream(urlStr);
    body.upstream = { scheme: up.scheme, host: up.host, port: up.port, tls: { mode: mode, ca_name: caName } };
  }
  return body;
}

function parseUpstream(raw) {
  let s = (raw || '').trim();
  if (!s) throw new Error('upstream URL is required (or check "allocate")');
  if (!/^https?:\/\//i.test(s)) s = 'http://' + s;
  let u;
  try { u = new URL(s); } catch (_) { throw new Error('invalid upstream URL: ' + raw); }
  const scheme = u.protocol.replace(':', '');
  const port = u.port ? parseInt(u.port, 10) : (scheme === 'https' ? 443 : 80);
  return { scheme, host: u.hostname, port };
}

async function deleteMapping(lb, m) {
  const tgt = m.upstream ? m.upstream.host + ':' + m.upstream.port : '';
  const ok = await confirmAction('Delete mapping ' + m.listen_port + ' → ' + tgt + '?');
  if (!ok) return;
  try {
    await api('DELETE', mapURL(lb.name, m.listen_port));
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- port pool (right column) ----------------------------------------------
function renderPorts(pool) {
  const card = byId('port-pool');
  clear(card);
  if (!pool) return;
  const total = (pool.used || 0) + (pool.free || 0);
  const pct = total > 0 ? Math.round((pool.used / total) * 100) : 0;
  card.append(
    el('div', { class: 'muted' }, 'Range ', el('strong', null, pool.start + '–' + pool.end)),
    el('div', { class: 'bar' }, el('div', { class: 'bar-fill', style: 'width:' + pct + '%' })),
    el('div', { class: 'muted small' }, (pool.used || 0) + ' used · ' + (pool.free || 0) + ' free'),
  );
  const tbody = el('tbody');
  for (const a of (pool.allocations || []).slice().sort((x, y) => x.port - y.port)) {
    tbody.append(el('tr', null,
      el('td', null, String(a.port)),
      el('td', { class: a.owner ? '' : 'muted' }, a.owner || '—'),
      el('td', { class: 'muted' }, a.label || '—', a.auto ? el('span', { class: 'badge' }, 'auto') : null),
      el('td', { class: 'right' }, el('button', { class: 'link', type: 'button', onclick: () => returnPort(a) }, 'return')),
    ));
  }
  card.append(el('table', { class: 'grid' },
    el('thead', null, el('tr', null,
      el('th', null, 'Port'), el('th', null, 'Owner'), el('th', null, 'Label'), el('th', null, ''))),
    tbody));
  card.append(renderAllocateControl());
}

function renderAllocateControl() {
  const wrap = el('div', { class: 'alloc' });
  const open = el('button', { class: 'btn-outline', type: 'button' }, 'Allocate port');
  open.addEventListener('click', () => {
    const owner = el('input', { type: 'text', placeholder: 'owner (optional)' });
    const label = el('input', { type: 'text', placeholder: 'label (optional)' });
    const form = el('form', { class: 'inline-form alloc-form' },
      owner, label,
      el('button', { class: 'btn-primary', type: 'submit' }, 'Allocate'),
      el('button', { class: 'btn-ghost', type: 'button', onclick: () => refresh().catch(swallow) }, 'Cancel'));
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        await api('POST', '/api/v1/ports/allocate', { owner: owner.value.trim(), label: label.value.trim() });
        clearError();
        await refresh();
      } catch (err) { swallow(err); }
    });
    clear(wrap);
    wrap.append(form);
    owner.focus();
  });
  wrap.append(open);
  return wrap;
}

async function returnPort(a) {
  const ok = await confirmAction('Return port ' + a.port + '? Any mapping using it will be affected.');
  if (!ok) return;
  try {
    await api('DELETE', '/api/v1/ports/' + a.port);
    clearError();
    await refresh();
  } catch (err) { swallow(err); }
}

// ---- bootstrap -------------------------------------------------------------
function init() {
  byId('login-form').addEventListener('submit', onLogin);
  byId('logout').addEventListener('click', onLogout);
  byId('refresh').addEventListener('click', () => refresh().catch(swallow));
  byId('add-lb').addEventListener('click', () => { addingLB = !addingLB; refresh().catch(swallow); });
  byId('error-dismiss').addEventListener('click', clearError);
  byId('confirm-ok').addEventListener('click', () => settleConfirm(true));
  byId('confirm-cancel').addEventListener('click', () => settleConfirm(false));
  byId('confirm-overlay').addEventListener('click', (e) => { if (e.target === byId('confirm-overlay')) settleConfirm(false); });
  boot();
}

async function boot() {
  if (!getToken()) { showLogin(); return; }
  try {
    await api('GET', '/api/v1/status'); // verify the stored token
    await startDashboard();
  } catch (e) {
    if (e !== AUTH) showLogin(); // network error → login (can't verify)
  }
}

async function onLogin(e) {
  e.preventDefault();
  const t = byId('token').value.trim();
  if (!t) return;
  setToken(t);
  byId('login-error').hidden = true;
  try {
    await api('GET', '/api/v1/status');
    await startDashboard();
  } catch (err) {
    const msg = err === AUTH ? 'Invalid or expired token' : ((err && err.message) || 'Connection failed');
    byId('login-error').textContent = msg;
    byId('login-error').hidden = false;
  }
}

function onLogout() {
  stopPolling();
  clearToken();
  expanded.clear();
  labelEditing.clear();
  addingLB = false;
  showLogin();
}

async function startDashboard() {
  showDashboard();
  try { cas = (await api('GET', '/api/v1/cas')) || []; } catch (e) { if (e === AUTH) return; cas = []; }
  try { await refresh(); } catch (e) { if (e === AUTH) return; swallow(e); }
  startPolling();
}

// The <script> tag is deferred, so the DOM is parsed before this runs.
init();
```

- [ ] **Step 2: Confirm the Go tests still pass**

Run: `go test ./internal/ui/`
Expected: PASS (app.js still served as `text/javascript; charset=utf-8`; the shell is still data-free — the JS lives in app.js, not index.html).

- [ ] **Step 3: Manual verification checklist** (harness running: `go run ./cmd/uidev`; open `http://127.0.0.1:8088/`)

Work through every item:
- **Login:** paste the printed token → dashboard appears. Reload the page → still authenticated (sessionStorage). Enter a wrong token (e.g. `xxxx`) → inline "Invalid or expired token" and you stay on the login screen.
- **Header:** shows `vdev`, `Listeners :443 · :8443`, and a green Running dot.
- **Load balancers:** three rows, alphabetical (`api`, `app`, `blog`). `api` has a green dot + a `🔒 verify` badge on its `443 → localhost:8001` chip; `blog` has a red dot; `app` has an amber (unknown) dot and shows an `allocated` badge on its `8080` mapping when expanded.
- **Quick-launch:** the LB name and each chip are links of the form `https://api.dev.example.com` (default :443 omitted) and `https://blog.dev.example.com:8443`. (They won't load in dev — the hosts don't resolve. That's expected.)
- **Add LB:** click "+ Add load balancer" → inline form → create `test.dev.example.com` with label `Temp` → it appears in the list. Try creating it again → red error banner with the server's conflict message.
- **Edit label:** click ✎ on a row → inline editor prefilled → change the label → Save → updates. Cancel on another → no change.
- **Expand + add mapping:** click "+ map" on `test.dev.example.com` → panel opens. Add `8443` / `http://localhost:7000` / mode `verify` → mapping appears with an `up`/`unknown` health cell. Check "allocate", note the upstream field disables, add another listen port → an auto-allocated mapping appears (and a new row shows in the port pool).
- **Replace mapping:** click ✎ on a mapping row → form prefills, button reads "Save", listen port read-only → change the upstream port → Save → row updates.
- **Delete mapping / LB:** click 🗑 on a mapping → themed confirm dialog → Confirm → removed. Click 🗑 on `test.dev.example.com` → confirm dialog mentions the cascade → Confirm → row gone (and its auto-allocated port disappears from the pool).
- **Port pool:** usage bar + `N used · M free`. Click "Allocate port" → inline form → set owner `me` / label `scratch` → Allocate → new row. Click "return" on it → confirm → removed.
- **Live updates:** with no form focused, open DevTools → Network: `lbs`, `ports`, `status` refetch every ~5s. Open an inline form and type → polling pauses (no new requests) until you blur/clear it. Manual "Refresh" always refetches.
- **Errors:** trigger a validation error (e.g. add a mapping with an empty upstream and allocate unchecked) → red banner with the message → Dismiss clears it.
- **Theme:** toggle OS light/dark → palette follows on reload.
- **Logout:** click Logout → back to login; reload → still logged out (token cleared).

- [ ] **Step 4: Commit**

```bash
git add internal/ui/assets/app.js
git commit -m "feat(ui): dashboard client — auth, CRUD, live polling, confirm dialog

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Full-suite verification gate

**Files:** none (verification only).

- [ ] **Step 1: Format check**

Run: `gofmt -l internal/ui/ internal/api/ cmd/`
Expected: no output (everything formatted).

- [ ] **Step 2: Vet + build the whole module**

Run: `go vet ./... && go build ./...`
Expected: no errors. (This compiles the gitignored `cmd/uidev` too — it must stay clean.)

- [ ] **Step 3: Full test suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS, including `internal/ui`, `internal/api` (with `ui_mount_test.go`), and `cmd/rgdevenv` (with the updated `serve_test.go`).

- [ ] **Step 4: Lint (if available)**

Run: `golangci-lint run`
Expected: clean. (If `golangci-lint` is not installed, note that and rely on `go vet`.)

- [ ] **Step 5: Confirm the working tree is clean and the dev harness stayed out of git**

Run: `git status --short`
Expected: empty (no tracked changes pending). `cmd/uidev/` does not appear because it is gitignored.

This is the final task. After it passes, hand off to **superpowers:finishing-a-development-branch** to run the final whole-implementation review and ff-merge `phase2d-webui` into `master`.

---

## Self-review (plan author)

**Spec coverage** — every spec section maps to a task:
- Architecture / data-free static bundle / no API changes → Task 1 (`ui.go` embed) + Task 2 (mount). ✔
- Auth & session (login, sessionStorage, verify via `/status`, 401 → login, logout) → Task 5 (`boot`/`onLogin`/`onLogout`/`api`/`handleUnauthorized`). ✔
- Layout & components (header, LB list w/ health dot + quick-launch + chips + TLS badge + actions, expand → mappings table + add/edit form w/ CA dropdown + allocate, port pool) → Tasks 3 (markup) + 4 (CSS) + 5 (render + forms). ✔
- Live updates (5s poll of lbs+ports+status, suspend while editing, manual Refresh, preserve expanded) → Task 5 (`startPolling`/`poll`/`busy`/`expanded`). ✔
- Errors & confirmations (dismissible banner; single reusable themed confirm dialog; delete-LB cascade warning) → Tasks 3 (markup) + 5 (`showError`/`confirmAction`). ✔
- Theme & responsive (auto light/dark; collapse to stacked) → Task 4 (CSS custom properties + media query). ✔
- Security (open data-free shell asserted by test; strict CSP; embed sub-FS 404-otherwise; `/api/v1/*` stays behind auth; ServeMux precedence) → Task 1 tests + Task 2 integration test. ✔
- File structure & wiring (`internal/ui/...`, mount at `api.go` seam) → Tasks 1–2. ✔
- Testing strategy (Go serving + integration tests; JS manual) → Tasks 1, 2, 5 (manual checklist), 6 (gate). ✔
- Deferred "degraded mapping" indicator → intentionally NOT implemented (needs an API field); out of scope. ✔

**Placeholder scan:** no TBD/TODO/"handle errors"/"similar to Task N"; every code step is complete. ✔

**Type/name consistency:** verified API request/response keys against `internal/client/types.go`, `internal/api/{lbs,mappings,ports,misc,views}.go`, `internal/store/model.go`, `internal/health/health.go`; `txn.New`/`MappingSpec`/`Config`/`CreateLB`/`PutMapping`/`AllocatePort` and `api.Deps`/`store.Open`/`auth.*`/`upstream.NewPolicy` signatures verified against source. JS helper/function names (`el`, `api`, `refresh`, `renderLBRow`, `renderMappingForm._fill`, `mapURL`, `buildMappingBody`, `parseUpstream`, `worstHealth`, `launchURL`, `busy`, `confirmAction`/`settleConfirm`) are used consistently throughout. HTML element ids in Task 3 match every `byId(...)` in Task 5. CSS classes in Task 4 match every `class:` used in Task 5. ✔
