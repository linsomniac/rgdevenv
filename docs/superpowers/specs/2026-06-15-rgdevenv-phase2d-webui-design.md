# rgdevenv Phase 2d — Web Management UI: Design

**Date:** 2026-06-15
**Status:** Approved (brainstorming) — ready for implementation plan
**Source spec:** `docs/superpowers/specs/2026-06-13-rgdevenv-design.md` (§14 Web UI, §15 Security)
**Builds on:** Phase 2a/2b management API (`internal/api`, `internal/health`) and Phase 2c CLI (`internal/client`, `cmd/rgdevenv`).

---

## Goal

A token-protected, single-page web dashboard for rgdevenv that provides **full CRUD parity with the CLI** (load balancers, mappings, port allocations) plus quick-launch links and live upstream-health status — served from the single Go binary with **no JS build step** and **no new API endpoints**.

## Architecture

The server serves a small, **data-free static bundle** (`index.html` + `app.css` + `app.js`) embedded via `embed.FS`, mounted at `/` on the management mux. The page loads **unauthenticated** (it contains no dynamic data); the browser's vanilla JavaScript fetches **all** data client-side from the existing `/api/v1/*` endpoints, sending `Authorization: Bearer <token>` on every call.

Consequences of this model:
- **No cookies**, no server-side data rendering, no CSRF surface.
- **No `html/template` is required** — there is no server-side data to interpolate, so the shell is served as plain static assets. This is a deliberate, minor deviation from §14's "server-rendered (Go `html/template`)" wording, justified by the bearer-in-`sessionStorage` model (the unauthenticated page load cannot legitimately contain data). A template seam may be reintroduced later only if a server value ever needs injecting; YAGNI for now.
- **No new API endpoints and no API changes.** Phase 2d is purely a consumer of the Phase 2a/2b REST API. An LB's `name` is already its full canonical hostname (the router matches the request `Host` directly against `canon.Host(lb.Name)`), so quick-launch links are built directly from `lb.name` + the mapping's `listen_tls`/`listen_port` — no base-domain configuration is needed.

## Auth & session model

A single page with two views toggled by JavaScript:

- **Login view:** a token field (password input) + **Connect** button. On submit: store the token in `sessionStorage`, then verify it with `GET /api/v1/status`. Success → show the dashboard. `401` → show an inline "Invalid or expired token" message and clear the stored token.
- **On app load:** if `sessionStorage` holds a token, verify via `GET /api/v1/status` → dashboard, or (on `401`) the login view. If no token, show the login view.
- **Logout:** clear `sessionStorage` → login view.
- **Mid-session `401`:** any `/api/v1/*` response of `401` clears the token and returns to the login view (token invalid or expired).

The token lives **only** in `sessionStorage` (per §14): it survives reload but is cleared when the tab closes. It is never placed in the URL and never logged. Because no cookies are used, there is no CSRF surface.

## Layout & components

Two-column dashboard (approved layout): load balancers on the left, port pool on the right. Below a minimum width the columns collapse to a single stacked column (CSS only).

### Header
`rgdevenv` · a **Running** status dot · active listeners (`status.active_listeners`) · version (`status.version`) · **Logout** button.

### Load balancers (left) — `GET /api/v1/lbs`
Each LB row shows:
- A **health dot** = worst-of its mappings' `health` (precedence: `down` > `unknown` > `up`; empty/missing health is treated as `unknown`).
- A **quick-launch link** built as `{listen_tls ? "https" : "http"}://{lb.name}` with `:{listen_port}` appended unless it is the scheme default (443 for https, 80 for http). When an LB has multiple mappings, each mapping's chip is its own link.
- The **label** (muted).
- **Mapping chips:** `{listen_port} → {upstream.host}:{upstream.port}` with an **upstream-TLS badge** showing `upstream.tls.mode` when the upstream scheme is `https`.
- **Actions:** `+ map`, edit label, delete.

Row interactions:
- **Edit label** → `PATCH /api/v1/lbs/{name}` `{ "label": ... }`.
- **Delete LB** → `DELETE /api/v1/lbs/{name}` (guarded by a confirm dialog warning that mappings and auto-allocated ports cascade).
- **Expand** → an inline mappings table (Listen, Upstream, TLS, Health, delete) plus an **add/edit mapping form**: listen port, a **listen-scheme select (`https`/`http`)** that maps to `listen_tls` (https is the default, matching the server; `http` is the equivalent of the CLI's `--no-tls`), upstream URL, upstream-TLS mode, a **CA-name dropdown populated from `GET /api/v1/cas`**, and an `allocate` checkbox.
  - Create mapping → `POST /api/v1/lbs/{name}/mappings`.
  - Replace mapping → `PUT /api/v1/lbs/{name}/mappings/{port}`.
  - Delete mapping → `DELETE /api/v1/lbs/{name}/mappings/{port}`.
- **+ Add load balancer** → `POST /api/v1/lbs` `{ "name": <full hostname>, "label"?: ... }`.

### Port pool (right) — `GET /api/v1/ports`
- Range (`start`–`end`) and a used/free usage bar (`used`/`free`).
- An allocations table: `port`, `owner`, `label`, and a **return** action → `DELETE /api/v1/ports/{port}`.
- An **Allocate** button → `POST /api/v1/ports/allocate` `{ "owner"?: ..., "label"?: ... }`.

## Live updates

The dashboard polls `GET /api/v1/lbs`, `GET /api/v1/ports`, and `GET /api/v1/status` every **5 seconds** (a single tunable constant in `app.js`) and also offers a manual **Refresh** control. Polling is **suspended while an inline form/editor is open or an input is focused/dirty**, so a background refresh never clobbers in-progress input. List re-renders are data-driven and **preserve which LB row is expanded**. A `401` from any poll routes to the login view.

## Errors & confirmations

- **API errors** (`{ "error", "code" }` body + HTTP status) surface in a **dismissible banner** at the top of the dashboard. `400` validation errors are shown inline next to the relevant form when feasible, otherwise in the banner.
- **Destructive actions** (delete LB, delete mapping, return port) route through a **single reusable, themed confirm dialog** — not the native `confirm()`. The delete-LB confirmation explicitly warns about the cascade (its mappings and any auto-allocated ports).

## Theme & responsive

**Automatic light/dark** via `prefers-color-scheme`, implemented with CSS custom properties (a light and a dark palette). Desktop-first; the two-column layout collapses to a single stacked column below a minimum width. Mobile polish is explicitly not a goal.

## Security model

- The static shell is served **open** (no bearer) but is **provably data-free** — asserted by test.
- A strict **Content-Security-Policy** is set on served assets: `default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self' data:; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`. This is why CSS and JS live in **separate embedded files with no inline** scripts or styles. (`font-src 'self' data:` is permissive for fonts only — the page itself uses system fonts; allowing `data:` fonts keeps browser-extension-injected fonts, e.g. Dark Reader, from tripping `default-src 'none'`. Fonts are inert and `style-src 'self'` still gates `@font-face`.) `frame-ancestors`/`base-uri`/`form-action` are set explicitly because `default-src 'none'` does not cover them: `frame-ancestors 'none'` blocks clickjacking of the loopback dashboard, and `base-uri 'none'`/`form-action 'self'` close `<base>`-injection and form-exfiltration vectors.
- Assets are served from an `embed.FS` **sub-filesystem** so only bundled files resolve — unknown paths return `404`, with no directory traversal and no directory listing.
- `/api/v1/*` remains behind `authMiddleware`. Mounting the UI at `/` does not shadow it: Go's `ServeMux` uses longest-pattern precedence, so `/api/v1/` and `/healthz` win over the `/` catch-all.
- The bearer token is never logged and never written anywhere but the browser's `sessionStorage`.

## File structure & wiring

```
internal/ui/
  ui.go              // //go:embed assets/* ; Handler() http.Handler
                     //   - serves "/"  -> index.html
                     //   - sets CSP + correct content-types
                     //   - serves only embedded assets (404 otherwise)
  ui_test.go         // serving + security tests
  assets/
    index.html       // login view + dashboard skeleton (no data, no inline JS/CSS)
    app.css          // light/dark palettes, layout, components
    app.js           // auth, fetch wrapper, render, forms, polling, confirm dialog, error banner
```

Wiring: `internal/api/api.go` `buildMux` mounts `ui.Handler()` at `/` — the existing `AIDEV-TODO(phase2b)` seam at `api.go:85` — **outside** `authMiddleware`. `internal/api` imports `internal/ui`.

## Testing strategy

- **Go tests** (the real testable surface):
  - `GET /` returns `200` + `text/html`, served **without** a bearer.
  - The shell is **data-free** (negative assertion: no token, no LB/port data in the bytes).
  - `app.js` / `app.css` are served with correct content-types and the CSP header is present.
  - Unknown asset path → `404` (no traversal, no listing).
  - **Integration** (extending the in-process API test pattern): with the UI mounted, `/` serves the shell **and** `/api/v1/lbs` still returns `401` without a token / `200` with — proving the mount did not break auth or route precedence.
- **JavaScript behavior:** with no build step there is no JS unit harness, so the JS is kept deliberately small and **verified manually**. Automated JS/DOM behavior testing (e.g. a headless-browser harness such as Playwright) is **out of scope for this cut** — it would introduce a Node + CI dependency that conflicts with the single-binary, no-build-step ethos. This is the one conscious test-coverage limitation of Phase 2d.

## Scope, non-goals & deferred

- **In scope:** full CRUD parity with the CLI (LBs, mappings, port allocate/return, label edits), quick-launch links, live health/status, auto light/dark, full keyboard-reachable forms.
- **Non-goals:** roles / multi-user (the bearer is all-or-nothing), server-side UI preference storage, mobile-optimized layouts, any new/changed API endpoint.
- **Deferred (would require API additions):** surfacing **degraded** mappings distinctly (configured but not served — e.g. host not certificate-covered); the first cut consumes only existing endpoints, so a configured mapping appears with its upstream-health dot but without a separate "not served" indicator.

## API endpoints consumed (reference)

| Method & path | Used for |
|---|---|
| `GET /api/v1/status` | header (version, listeners), token verification, health summary, poll |
| `GET /api/v1/lbs` | load-balancer list + mappings + health, poll |
| `POST /api/v1/lbs` | add load balancer |
| `PATCH /api/v1/lbs/{name}` | edit label |
| `DELETE /api/v1/lbs/{name}` | delete load balancer (cascade) |
| `POST /api/v1/lbs/{name}/mappings` | add mapping |
| `PUT /api/v1/lbs/{name}/mappings/{port}` | replace mapping |
| `DELETE /api/v1/lbs/{name}/mappings/{port}` | delete mapping |
| `GET /api/v1/cas` | CA-name dropdown |
| `GET /api/v1/ports` | port pool + allocations, poll |
| `POST /api/v1/ports/allocate` | allocate a port |
| `DELETE /api/v1/ports/{port}` | return a port |
