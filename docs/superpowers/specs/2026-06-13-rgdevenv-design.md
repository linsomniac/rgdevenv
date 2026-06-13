# rgdevenv — Design Spec

- **Date:** 2026-06-13
- **Status:** Approved in brainstorming; revised after external (Codex) review;
  pending implementation plan
- **Authors:** Sean Reifschneider, with Claude

## 1. Summary

`rgdevenv` is a single-binary Go service — a Traefik-like HTTPS reverse proxy for
managing multiple virtual development environments on a developer host. It
terminates TLS using a **supplied** wildcard certificate (e.g.
`*.sean.realgo.com`), routes incoming requests by hostname to a single upstream
per mapping, and maintains a **port-reservation registry** for dev servers. It
ships with a token-protected **web UI**, a **REST API**, and a **CLI** that are
functionally equivalent.

It is **route-only**: rgdevenv never launches or supervises the upstream dev
processes. Those are started by the developer (or, in the future, by an external
supervisor that integrates via the API).

The port registry is **bookkeeping only** — it reserves numbers so two
environments do not pick the same port; it does not bind OS sockets, and apps
bind their reserved ports themselves (see §11).

## 2. Goals and non-goals

### Goals (v1)

- Add/remove named **load balancers** (hostnames under the wildcard).
- Add/update/remove **mappings**: a `(listener port, hostname)` forwards to
  exactly one upstream (HTTP or HTTPS, localhost or an allowlisted host) with
  upstream TLS options `verify` / `ca` (named server-side CA) / `skip`.
- Allocate/return ports from a configurable pool as a **registry of reservations**
  (collision-free at the registry level), with a convenience flow to
  auto-allocate a port when mapping to localhost.
- A **web management UI** (full CRUD + quick-launch links + status), a **REST
  API**, and a **CLI** — all equivalent.
- **Live reconfiguration** without restart, via a staged transaction (§16);
  durable JSON state across restarts.

### Non-goals (v1; see §21 Future)

- Launching/supervising dev processes (kept external; future "mode B").
- Raw, non-HTTP TCP/TLS passthrough (future listener mode).
- Multiple upstreams per mapping / real load balancing (exactly one upstream).
- Certificate generation / ACME (the cert is supplied).
- Multi-user accounts / RBAC (single shared token).
- DNS management (the wildcard `A`/`AAAA` record is configured externally).

## 3. Locked decisions

| Area | Decision |
|------|----------|
| Ownership | Route-only; process management external/future |
| Upstream cardinality | Exactly one upstream per mapping (virtual host) |
| Proxy engine | Custom L7 on Go stdlib (`net/http`, `crypto/tls`, `httputil.ReverseProxy`) |
| Protocols (v1) | HTTP/HTTPS only; raw TCP is a future listener mode |
| Listeners | Always-on `:443` HTTPS + `:80` redirect; other ports on demand, **disjoint from the port pool** |
| TLS cert | Supply-only (cert/key paths); no generation/ACME |
| Upstream TLS | `verify` (system roots) / `ca` (named **private** CA only, server-side CA dir) / `skip` |
| Port pool | Registry-only reservations; apps bind directly; OS still owns binding |
| Management access | Reserved hostname over `:443`, **bearer-token only**, rate-limited, constant-time compare; **optional** separate management bind (default off; loopback/unix **plaintext**) |
| Upstream policy | localhost allowed; other hosts require an allowlist; link-local / cloud-metadata / self-listener always denied |
| UI auth | Bearer-only (token in `sessionStorage`, `Authorization` header); no cookies, no CSRF surface |
| Web UI scope | Full CRUD + quick-launch links + status |
| Persistence | Single JSON state file, atomic writes (incl. parent-dir fsync), `0600`, single-instance lock |

## 4. Architecture and components

A single daemon process (`rgdevenv serve`) holds all state in memory behind an
atomically published snapshot, applies changes via a staged transaction, and
persists them to a JSON file. The same binary provides the CLI subcommands, which
are thin clients over the REST API.

Components:

- **Listeners** — TCP listeners per public port, each with a single TLS mode.
- **TLS** — loads the supplied cert/key pair(s); an SNI resolver selects the cert.
- **Router** — maps `(listener, canonical Host)` to a mapping; the reserved
  management hostname (or the optional management bind) reaches the management
  plane.
- **Reverse proxy** — an `httputil.ReverseProxy` per upstream; applies the
  upstream TLS mode, normalizes forwarding headers, enforces timeouts/limits
  (§8); transparently supports WebSocket upgrades.
- **Port registry** — the configurable pool; allocate/return with a stable id,
  owner, and label. Pure bookkeeping — never in the request path; never binds
  sockets.
- **Store** — in-memory snapshot + atomic JSON persistence + single-instance lock.
- **Management plane** — REST API + server-rendered web UI, bearer-only,
  rate-limited.
- **CLI** — `rgdevenv` subcommands that call the REST API.

## 5. Domain model

All hostnames are **canonicalized** before use (§6): lowercased, trailing dot
stripped, any port stripped, IDNA-normalized; malformed hosts are rejected. The
canonical form is the key used for validation, routing, auth, persistence, and
certificate matching.

- **LoadBalancer** — a hostname under the wildcard (e.g.
  `rg-27788-cpcart-cleanups.sean.realgo.com`). Fields: `name` (canonical FQDN,
  unique key), `label`, `created_at`. Has **0..N Mappings** (an LB may exist with
  none yet). Its `name` must be covered by a configured certificate and must not
  equal the management hostname.
- **Mapping** — belongs to a LoadBalancer. Fields: `listen_port` (default `443`,
  must be outside the port-pool range), `listen_tls` (default `true`), one
  **Upstream**, and an optional `allocation_id` + `auto_allocated` flag linking it
  to a PortAllocation. Unique within a LoadBalancer by `listen_port` (conflict
  scope is the pair `(name, listen_port)`).
- **Upstream** — `scheme` (`http`|`https`), `host`, `port`, and (for `https`) a
  `tls` block `{ mode: verify|ca|skip, ca_name? }`. Exactly one per mapping.
  `host` must satisfy the upstream policy (§15). For `mode = ca`, `ca_name` must be
  a **path-safe identifier** (no path separators or `..`) resolving to
  `<ca_dir>/<ca_name>.pem` (§7).
- **PortAllocation** — a reserved port from the pool. Fields: `id` (stable
  reference), `port`, `owner` (optional, usually a LoadBalancer name), `label`,
  `auto` (true if created by the auto-allocate convenience), `allocated_at`.
  Lifecycle in §11.

Cardinalities: `LoadBalancer 0—N Mapping`, `Mapping 1—1 Upstream`, `Mapping
0..1—1 PortAllocation` (when auto-allocated).

## 6. Listener and routing model

- Always-on: HTTPS on `https_port` (default `443`, terminates TLS, routes by
  SNI/Host) and an HTTP redirect listener on `http_port` (default `80`). Setting
  `http_port = 0` disables the redirect listener.
- **On-demand listeners:** when a mapping references a `listen_port` that has no
  active listener, rgdevenv opens one (during the transaction's pre-bind step,
  §16). Default TLS mode is HTTPS; `listen_tls = false` makes it plain HTTP. When
  the last mapping on an on-demand port is removed, its listener is closed.
- **Port-pool disjointness:** a `listen_port` must lie **outside**
  `[pool.start, pool.end]` and must not equal `http_port`, `https_port`, or the
  `management.bind` port. Conversely, the pool range itself must **not** contain
  any of those always-on/management ports; both directions are validated at
  startup (§16). This keeps proxy listeners from colliding with reserved
  application ports.
- **One TLS mode per port:** a `listen_port` has exactly one TLS mode across all
  mappings/hosts. A mapping whose `listen_tls` conflicts with the port's
  established mode is rejected (`409 Conflict`).
- The reserved `https_port`/`http_port` cannot be repurposed to other modes.
- **Canonical routing:** the request `Host` is canonicalized (§5) before lookup.
  An unknown host returns a generic `404`. SNI and Host are both canonicalized; a
  benign mismatch routes by Host, but a request whose Host is not covered by the
  served certificate is refused.
- **Management routing:** if a separate `management.bind` is configured, the
  management plane is served only there and is **not** reachable via the data
  plane. That bind is **plaintext** and permitted only on a loopback address or a
  unix socket (a non-loopback plaintext bind is refused at startup); the bearer
  token is still required, and confidentiality for remote use comes from an SSH
  tunnel (§15). Otherwise the management plane is served on the HTTPS listener
  when the canonical Host equals `management_hostname` (after auth). Creating a
  load balancer named `management_hostname` is forbidden.
- **HTTP→HTTPS redirect:** the `:80` listener issues a `308` redirect **only** for
  hosts that are known/canonical and certificate-covered, to a canonical
  `https://<host>:<https_port>` URL (port omitted when `443`). Arbitrary/unknown
  `Host` values are not echoed back (no open redirect); they get a generic `404`.

## 7. TLS and certificates

- rgdevenv loads one or more supplied `(cert_file, key_file)` pairs at startup and
  serves them via an SNI-based `tls.Config.GetCertificate` resolver. With a single
  wildcard pair, every `*.sean.realgo.com` name (including the management
  hostname) is covered.
- No certificate generation, no ACME. Client trust of the issuing CA is handled
  outside rgdevenv (developers install the CA into their trust stores).
- **Reload:** `SIGHUP` reloads the certificate(s), the custom-CA directory, and
  runtime-safe config (e.g. log level), without dropping existing connections
  where practical. Reload is **validate-before-swap**: new material is parsed and
  validated first, and on any failure the previously loaded certs/CAs are
  **retained** (the reload is a logged no-op) — verification is never silently
  downgraded. If reloaded material no longer covers a mapping's host or the
  management hostname, the affected mapping is marked **degraded** (and a
  management-hostname mismatch is surfaced loudly) rather than served with weaker
  trust.
- **Upstream TLS** (for `https` upstreams):
  - `verify` — verify against the system root pool.
  - `ca` — verify against **only** the named private CA loaded from the
    server-side `ca_dir`; system roots are **not** trusted in this mode (use
    `verify` for publicly-trusted upstreams). `ca_name` must be a **path-safe
    identifier** (no path separators or `..`) resolving to `<ca_dir>/<ca_name>.pem`.
    A missing/invalid/unsafe `ca_name` makes the mapping invalid: **rejected** on
    create, and on startup the mapping is marked **degraded** and does **not**
    serve (fail-closed — never downgraded to a weaker mode).
  - `skip` — `InsecureSkipVerify` (dev-only).
  - `ServerName` for verification defaults to the upstream `host`.

## 8. Request flows and proxy policy

**Data plane (user traffic):**
1. Client opens `https://<name>.sean.realgo.com` → hits the `:443` listener.
2. TLS terminates with the wildcard cert (selected by SNI).
3. Router canonicalizes Host, looks up `(443, Host)` → mapping. Unknown host →
   generic `404`.
4. `httputil.ReverseProxy` forwards to the upstream using its scheme/host/port and
   TLS mode. Upstream unreachable → generic `502` (upstream identity is **not**
   exposed in the public page; full detail goes to logs).
5. Response (including WebSocket upgrades) streams back to the client.

**Proxy trust and limits:**
- Forwarding headers are **set by rgdevenv**, not trusted from the client:
  incoming `X-Forwarded-*`/`Forwarded` from data-plane clients are stripped and
  replaced (`X-Forwarded-For` = client IP, `X-Forwarded-Proto`, `X-Forwarded-Host`,
  `X-Real-IP`). Hop-by-hop headers are dropped per RFC 7230.
- `http.Server`: `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` (relaxed for
  upgraded/streaming connections), `IdleTimeout`, and `MaxHeaderBytes` are all set
  to safe defaults (configurable). A request body size limit is enforced.
- `http.Transport` (upstream): bounded `DialContext` timeout, `TLSHandshakeTimeout`,
  `ResponseHeaderTimeout`, `MaxIdleConns`/`MaxIdleConnsPerHost`, and an idle-conn
  timeout. WebSocket/streaming connections bypass the response-header/write
  deadlines but are subject to an overall max-duration cap.
- The transport's `DialContext` is a single shared **safe dialer** (§15): it
  resolves the upstream name, validates **every** returned address against the deny
  rules, then dials a **pinned** validated IP — it never lets the OS re-resolve,
  closing the DNS-rebinding gap. The health checker (§17) uses the same dialer.

**Management plane:**
1. Browser/CLI → `https://rgdevenv.sean.realgo.com/...` (or the management bind),
   `/api/v1/...`.
2. **Bearer-only** auth: `Authorization: Bearer <token>`, compared in constant
   time. Failure → `401`; repeated failures per source IP are rate-limited →
   `429`. No cookies are used. The only unauthenticated surface is `/healthz` and
   the **static login shell** (HTML/CSS/JS containing no dynamic data); every
   `/api/v1/*` call and all dynamic data require the bearer.
3. The handler validates the request and applies it through the staged
   transaction (§16).

## 9. Configuration (static)

Static config is read at startup from a TOML file, overridable by environment
variables (`RGDEVENV_*`) and flags. Precedence: **flags > env > file > defaults**.
Changing values that affect bound sockets (ports, bind address) requires a
restart; cert paths, the CA dir, and log level reload via `SIGHUP`. The config
file, key, and token files should be `0600`.

```toml
# /etc/rgdevenv/config.toml
https_port = 443
http_port  = 80            # 0 disables the HTTP->HTTPS redirect listener
bind_addr  = "0.0.0.0"

cert_file = "/etc/rgdevenv/certs/wildcard.crt"
key_file  = "/etc/rgdevenv/certs/wildcard.key"
# Optional extra SNI cert pairs (the top-level pair is the primary):
# [[certs]]
#   cert_file = "/etc/rgdevenv/certs/other.crt"
#   key_file  = "/etc/rgdevenv/certs/other.key"

ca_dir = "/etc/rgdevenv/cas"            # named upstream CAs: <ca_name>.pem

management_hostname = "rgdevenv.sean.realgo.com"
token_file = "/etc/rgdevenv/token"      # 0600; or RGDEVENV_TOKEN. >= 256-bit random

state_file = "/var/lib/rgdevenv/state.json"

[management]
# bind = "127.0.0.1:8443"               # optional isolated mgmt listener: loopback/unix, PLAINTEXT
                                        # (non-loopback plaintext refused); empty = via wildcard :443
auth_rate_limit_per_min = 10            # failed-auth attempts per source IP -> 429

[port_pool]
start = 9000                            # range must exclude http_port/https_port/mgmt bind port
end   = 9999

[upstreams]
# localhost is always allowed. Non-localhost hosts must match an entry here.
allow = ["build-box", "10.0.0.0/8"]     # hostnames and/or CIDRs
# link-local (169.254.0.0/16), cloud-metadata (169.254.169.254), and rgdevenv's
# own listener addresses are ALWAYS denied (loop/SSRF protection), even if allowed.

[log]
level  = "info"
access = true
```

## 10. State (dynamic) and persistence

Dynamic state is mutated at runtime via the API and persisted to a single JSON
file with **atomic, durable writes**: write a temp file in the same directory,
`fsync` it, `rename` over the target, then `fsync` the parent directory. The file
is created `0600`. A **single-instance lock** (flock on a lockfile next to the
state file) prevents two daemons from racing on it. In-memory state is an
immutable snapshot published atomically (§16).

**Startup:**
- Missing state → start empty.
- Malformed/unparseable, or a **newer** `version` → abort with a clear error
  (operator intervention) rather than silently losing or downgrading data.
- Semantically invalid or duplicate/conflicting records → abort with a precise
  diagnostic.
- A listener port that cannot be bound (e.g. taken) → start anyway, mark the
  affected mapping **degraded**, and surface it in `/status`/logs.
- A crash after persist but before publish (§16) is benign: startup loads the
  **persisted (committed)** state and re-derives runtime from it.
- **Reconcile port allocations against mappings:** free orphaned `auto=true`
  allocations whose mapping no longer exists; an allocation that conflicts with a
  mapping or another allocation is a hard error (abort with a diagnostic).

```json
{
  "version": 1,
  "load_balancers": [
    {
      "name": "rg-27788-cpcart-cleanups.sean.realgo.com",
      "label": "cpcart cleanups",
      "created_at": "2026-06-13T09:30:00Z",
      "mappings": [
        { "listen_port": 443, "listen_tls": true,
          "upstream": { "scheme": "http", "host": "localhost", "port": 9011,
                        "tls": { "mode": "verify" } },
          "allocation_id": "alloc-9011", "auto_allocated": true }
      ]
    }
  ],
  "port_allocations": [
    { "id": "alloc-9011", "port": 9011,
      "owner": "rg-27788-cpcart-cleanups.sean.realgo.com",
      "label": "cpcart web", "auto": true, "allocated_at": "2026-06-13T09:29:00Z" }
  ]
}
```

`version` enables forward-compatible schema migration.

## 11. Port registry semantics

- The registry tracks **reservations of numbers**, not OS sockets. "Collision-free"
  means two reservations never share a number — it does **not** guarantee the port
  is free in the OS, and there is an inherent allocate-then-bind gap that apps own.
- `allocate` returns `{ id, port }` for the **lowest free port** in
  `[start, end]`, with optional `owner`/`label`. Exhaustion → `409`.
- `return` frees a port. Returning a port still referenced by a mapping is
  **rejected** (`409`) unless the mapping is removed first.
- **Auto-allocate** convenience: creating a `localhost` mapping with
  `allocate=true` (CLI `--allocate`) allocates a port (`auto=true`), wires the
  upstream to `http://localhost:<port>`, and links it via `allocation_id`.
- **Cascade:** deleting a mapping (or its LB) frees any `auto=true` allocation it
  owns; manually allocated ports persist until explicitly returned. **Replacing** a
  mapping (`PUT`) that owned an `auto=true` allocation frees that allocation when
  the replacement no longer references it (a fresh `allocate=true` mints a new one).
- Listener ports are disjoint from the pool (§6), so proxy listeners never consume
  reservable application ports.
- Allocations persist in `state.json` and survive restarts.

## 12. REST API

Base: `https://<management_hostname>/api/v1` (or the management bind). Auth:
`Authorization: Bearer <token>` on every `/api/v1/*` endpoint; bearer only
(no cookies); constant-time compare; rate-limited. The only unauthenticated
surfaces are `/healthz` and the static login shell (no dynamic data).

| Method & path | Purpose | Success |
|---|---|---|
| `GET /lbs` | List load balancers (with mappings + health) | 200 |
| `POST /lbs` | Create `{ name, label? }` (0 mappings) | 201 |
| `GET /lbs/{name}` | Get one | 200 |
| `PATCH /lbs/{name}` | Update `{ label }` | 200 |
| `DELETE /lbs/{name}` | Delete (and its mappings; cascade auto ports) | 204 |
| `POST /lbs/{name}/mappings` | Create mapping (body below) | 201 |
| `PUT /lbs/{name}/mappings/{listen_port}` | Replace a mapping | 200 |
| `DELETE /lbs/{name}/mappings/{listen_port}` | Delete mapping | 204 |
| `GET /ports` | Pool status: range, used/free, allocations | 200 |
| `POST /ports/allocate` | `{ owner?, label? }` → `{ id, port }` | 201 |
| `DELETE /ports/{port}` | Return a port (409 if in use) | 204 |
| `GET /cas` | List available custom-CA names (from `ca_dir`) | 200 |
| `GET /status` | Version, listeners, counts, upstream health | 200 |
| `GET /healthz` | Liveness (unauthenticated) | 200 |

Mapping create/replace body:
```json
{ "listen_port": 443, "listen_tls": true,
  "upstream": { "scheme": "http", "host": "localhost", "port": 9011,
                "tls": { "mode": "verify", "ca_name": null } },
  "allocate": false }
```
When `allocate` is `true`, omit `upstream.port`; the server allocates a port and
sets the upstream to `http://localhost:<port>`. For `tls.mode = "ca"`, `ca_name`
must reference an entry from `GET /cas`. On `PUT`, a `listen_port` in the body (if
present) must equal the path `{listen_port}` (else `400`); replacing a mapping that
owned an `auto=true` port frees that allocation when the new mapping no longer uses
it (§11).

**Status codes:** `400` validation error, `401` bad/missing token, `404` unknown
resource, `409` conflict (duplicate name, `(host, listen_port)` already mapped,
listener TLS-mode conflict, pool exhausted, return-in-use, listen_port inside the
pool), `429` auth rate-limited. `502`/`404` data-plane responses are generic
(no upstream details). Error body: `{ "error": "human message", "code":
"machine_code" }`. Every mutation validates **before** applying; an invalid
request never partially mutates or corrupts `state.json` (§16).

## 13. CLI

A single `rgdevenv` binary. `serve` runs the daemon; all other subcommands call
the REST API with a bearer token.

```
rgdevenv serve [--config FILE]                      run the proxy daemon

rgdevenv lb add  <name> [--label TEXT]
rgdevenv lb set  <name> --label TEXT                update label
rgdevenv lb rm   <name>
rgdevenv lb ls

rgdevenv map add <name> --upstream URL [--listen-port 443] [--no-tls]
                        [--upstream-tls verify|skip|ca] [--ca-name NAME]
rgdevenv map add <name> --allocate [--listen-port 443]   # port -> localhost:<port>
rgdevenv map set <name> --listen-port 443 [--upstream URL] [--upstream-tls ...] [--ca-name NAME]
rgdevenv map rm  <name> [--listen-port 443]
rgdevenv map ls  <name>

rgdevenv port get [--owner NAME] [--label TEXT]     # allocate; prints id + port
rgdevenv port return <port>
rgdevenv port ls

rgdevenv ca ls                                       # available custom-CA names
rgdevenv status
```

- `--upstream` accepts a URL like `http://localhost:9011` or
  `https://build-box:8443`; scheme/host/port are parsed from it.
- `--allocate` without `--upstream` allocates a port and maps
  `:443 → http://localhost:<port>`.
- Client config: `RGDEVENV_API` (default `https://<management_hostname>`) and
  `RGDEVENV_TOKEN`, or `~/.config/rgdevenv/cli.toml` (`0600`).
- Output is human-readable tables by default; `--json` for scripting.

## 14. Web management UI

Server-rendered (Go `html/template`) with a small amount of vanilla JavaScript
that calls the REST API; all assets embedded via `embed.FS`. **No JS build step.**

- **Auth (bearer-only):** the **static login shell** (HTML/CSS/JS, no dynamic
  data) loads **unauthenticated**; it accepts the token, stores it in
  `sessionStorage`, and sends it as the `Authorization` header on every API call.
  All `/api/v1/*` data requires the bearer. Logout clears it. No cookies, so there
  is no CSRF surface; the API rejects cookie auth.
- **Header:** product name, run status, active listeners, version, logout.
- **Load balancers** (left): each row shows the hostname as a quick-launch link
  built from the mapping's actual scheme + port (e.g.
  `https://name.sean.realgo.com:8443`), label, mapping chips + upstream-TLS badge,
  a protocol-aware up/down health dot, and actions (`+ map`, edit label, delete).
  Rows expand to manage mappings inline (add/edit form: listen port, upstream URL,
  upstream-TLS mode with a CA-name dropdown populated from `GET /cas`, and an
  `allocate` checkbox; per-mapping delete). `+ Add load balancer` creates one.
- **Port pool** (right): range + used/free usage bar, allocations table
  (`port`, `owner`, `label`, `return`), and an `Allocate` button.

All create/update/delete actions are available from the UI (full CRUD).

## 15. Security model

- **Management plane** is bearer-token only (constant-time compare; token from
  `token_file`/`RGDEVENV_TOKEN`, ≥256-bit random, never logged, never written to
  `state.json`). Failed attempts per source IP are rate-limited (`429`). The
  hostname is **not** an access boundary — the token is; an optional
  `management.bind` provides true network isolation. That bind is **plaintext on a
  loopback address or unix socket only** (a non-loopback plaintext bind is refused
  at startup); remote access is via an SSH tunnel, and the token is still required.
  The static login shell loads unauthenticated; all data requires the bearer.
- **Data plane is intentionally open**: anything that resolves a mapped hostname
  and reaches the host can hit that upstream. Exposure is bounded by `bind_addr`
  and the network.
- **Upstream policy (SSRF/loop protection):** `localhost` is always allowed; any
  other upstream host must match `[upstreams].allow` (hostnames/CIDRs). Regardless
  of the allowlist, link-local (`169.254.0.0/16`), cloud-metadata
  (`169.254.169.254`), and rgdevenv's own listener addresses are denied. This is
  enforced by a single shared **safe dialer** used for both proxying and health
  checks: it resolves the name, validates **all** returned IPv4/IPv6 addresses
  against the deny rules, then dials a **pinned validated IP** with no
  re-resolution — closing the DNS-rebinding window between check and connect. CIDRs
  match by parsed IP; CNAMEs are followed to their final addresses; an upstream
  resolving to multiple addresses is denied if **any** address is denied. Health
  checks do **not** follow redirects to denied targets.
- **Privilege:** prefer `CAP_NET_BIND_SERVICE` or systemd socket activation over
  running as root; if started as root to bind `:80`/`:443`, drop privileges
  afterward. The config/key/token files are `0600`.
- **Canonicalization** (§5) is applied uniformly to avoid host-confusion bypasses.
- Client trust of the certificate's CA is established out-of-band.

## 16. Transactions, error handling, and resilience

**Staged mutation transaction** (every management change):
1. **Build** a candidate snapshot from the live snapshot + the request.
2. **Validate** semantically (canonical names, 0..N cardinality, `(host,port)` and
   TLS-mode conflicts, pool disjointness incl. always-on/mgmt ports, PUT body/path
   port match, upstream policy, path-safe CA existence).
3. **Pre-bind** resources that can fail: open any new listener sockets, parse CA
   pools. On failure → abort, release partial resources, return an error.
4. **Persist** the candidate atomically and durably (§10).
5. **Publish** by atomically swapping the live snapshot pointer; hand pre-bound
   listeners to their servers.
6. **Cleanup**: close now-unreferenced listeners; affected in-flight connections
   (incl. WebSockets) drain within a timeout, then close.

Pre-bind (step 3) acquires every resource that can fail, so **publish (step 5) is
infallible** — a pointer swap plus handing already-bound listeners to their
servers. The **commit point is successful persistence (step 4)**: the API returns
success only after publish, a failure before step 5 leaves the running system
unchanged, and a crash after persist but before publish is benign because startup
loads the persisted (committed) state and re-derives runtime (§10). All mutations
are serialized; reads use the lock-free published snapshot.

**Data-plane errors:** unknown host → generic `404`; upstream
unreachable/refused/timeout → generic `502`. Neither page exposes internal
upstream identity; details go to logs.

**Shutdown:** graceful — stop accepting new connections, drain in-flight requests
within a timeout (closing long-lived WebSockets at the deadline), then exit.

## 17. Observability

- Structured logging via `log/slog`. Per-request **access logs** (host, method,
  path, upstream, status, bytes, latency) when `log.access` is on, and an **audit
  line** per management mutation (actor = client IP, action, target).
- `GET /status` (authenticated) reports version, active listeners, counts, and
  upstream health **with** detail; public surfaces stay generic.
- **Health checks:** a background checker probes each distinct upstream identity
  (`scheme/host/port/tls`, not just `host:port`) — an HTTP(S) request to a
  configurable path, falling back to TCP connect — on a configurable interval.
  Status flips only after **N consecutive** like results (hysteresis) to avoid
  flapping. Live proxy failures also feed status. Results surface in `/status` and
  the UI dot.

## 18. Tech stack and project layout

- **Go** (1.22+), standard library for the server/proxy (`net/http`, `crypto/tls`,
  `net/http/httputil`), `log/slog` for logging, `embed` for UI assets.
- **CLI:** `spf13/cobra` for subcommands/help.
- **Static config:** `BurntSushi/toml` plus env/flags.
- Dependencies kept minimal and well-known.

Proposed layout:
```
cmd/rgdevenv/            main: cobra root + subcommands
internal/config/         static config load/merge (file/env/flags)
internal/canon/          hostname/URL canonicalization (shared)
internal/store/          snapshot model + atomic JSON persistence + instance lock
internal/registry/       port pool allocate/return + lifecycle
internal/proxy/          listeners, SNI/TLS, router, reverse proxy, redirect, error pages
internal/upstream/       upstream policy (allowlist, deny rules) + CA pool loading
internal/health/         protocol-aware upstream health checks
internal/auth/           bearer token + rate limiter
internal/txn/            staged transaction (build/validate/pre-bind/publish)
internal/api/            REST handlers
internal/ui/             html/template + embedded assets
internal/client/         REST client used by the CLI
docs/superpowers/specs/  this spec
```

**Conventions:** `gofmt`/`goimports`, `go vet`, `golangci-lint`; `AIDEV-NOTE:` /
`AIDEV-TODO:` / `AIDEV-QUESTION:` anchor comments for complex/important/subtle
code; boring over clever.

## 19. Testing strategy

- **Unit:** canonicalization (case/trailing-dot/port/IDNA/malformed); port
  registry (allocation order, exhaustion, return-in-use rejection, cascade of auto
  ports); store (atomic+durable write, parent-dir fsync, load, round-trip,
  malformed/newer-version handling, instance lock); config precedence; router
  matching incl. management hostname, reserved-name refusal, listener TLS-mode
  conflicts, pool-disjointness; upstream policy (localhost allowed, allowlist
  enforced, link-local/metadata/self denied via the shared safe dialer;
  **validated-IP pinning** defeats a DNS-rebinding upstream; multi-address
  deny-if-any).
- **Transaction:** pre-bind failure leaves state unchanged; persist failure rolls
  back; startup reconciliation after a simulated crash; degraded-mapping on
  unbindable port.
- **API:** `net/http/httptest` over handlers — full CRUD incl. `PATCH`/`PUT`
  (body/path port match), bearer-only auth (401) with the static login shell
  reachable unauthenticated, rate-limit (429), conflicts (409).
- **Proxy integration:** test upstreams (HTTP, and HTTPS with a self-signed CA in
  `ca_dir`) asserting routing, the three upstream TLS modes, forwarding-header
  overwrite (spoof attempt stripped), WebSocket upgrade, open-redirect prevention
  on `:80`, self-proxy-loop refusal, generic `502`/`404` (no upstream leak), and
  timeout/body-size limits.
- **CLI:** subcommands against an in-process test API; human and `--json` output.
- Table-driven; must pass `go test ./...` and `-race`.

## 20. Deployment and operations

- Single static binary; long-lived daemon (e.g. a systemd unit) — one instance
  per developer host (enforced by the state-file lock).
- Default locations: config `/etc/rgdevenv/config.toml`, certs
  `/etc/rgdevenv/certs/`, custom CAs `/etc/rgdevenv/cas/`, state
  `/var/lib/rgdevenv/state.json`. Sensitive files `0600`.
- Binding `:80`/`:443` via `CAP_NET_BIND_SERVICE` or socket activation; drop
  privileges if started as root.
- `SIGHUP` reloads certs/CA dir/log level; `SIGTERM` triggers graceful shutdown.
- The wildcard DNS record pointing at the host is configured externally.

## 21. Future / out of scope

- **Process management ("mode B"):** launch/stop/supervise upstream dev servers,
  integrated via the API once the external experiment solidifies.
- **Raw TCP / TLS passthrough** as an additional listener mode.
- **Multiple upstreams per mapping** (round-robin/failover).
- **Dynamic listener CRUD** via the API (explicit listener objects vs on-demand).
- **Certificate generation / ACME**, a Prometheus metrics endpoint, per-route
  auth, and richer upstream policies (per-mapping overrides).

## 22. Assumptions

- The wildcard DNS record resolves to the rgdevenv host (managed externally).
- A single developer operates each instance (one shared token, no RBAC); the
  token is the management boundary.
- Clients trust the certificate's issuing CA via out-of-band installation.
- Remote (non-localhost) upstreams are explicitly allowlisted by the operator.
- Inter-app traffic is HTTP/HTTPS in v1 (raw TCP is future).

## 23. Revision log

- **2026-06-13 (initial):** brainstormed design approved.
- **2026-06-13 (post-Codex review):** added staged mutation transaction (§16);
  port-allocation lifecycle with stable ids + cascade + return-in-use rejection
  (§11); clarified "registry-only" and made listener ports disjoint from the pool
  (§§6,11); hostname canonicalization + reserved-name/cert rules (§§5,6); fixed
  cardinality to 0..N and added `PATCH`/`PUT` update ops + `(host,port)` conflict
  scope (§§5,12); specified forwarding-header/timeout/limit policy (§8); durable
  atomic writes (parent-dir fsync), `0600`, instance lock, and startup recovery
  (§10); safe `308` redirects + canonical URLs (§6); secrets/privilege hardening
  (§§9,15,20); protocol-aware health + error redaction (§§16,17). Decisions:
  management kept on hostname + token with rate-limiting/constant-time and an
  optional isolated bind; upstreams localhost-by-default + allowlist with hard
  deny of link-local/metadata/self; custom CAs by name from a server-side
  `ca_dir`; UI auth bearer-only (no cookies/CSRF).
- **2026-06-13 (post-Codex review #2):** tied SSRF/loop protection to a shared
  **safe dialer** with **validated-IP pinning** (no re-resolution) for proxy +
  health checks (§§8,15,19); defined the unauthenticated **static login shell** so
  bearer-only can bootstrap (§§8,12,14); made cert/CA reload
  **validate-before-swap / fail-closed** with CA errors degrade-not-downgrade
  (§§7,10); defined **publish as infallible** with successful persist as the commit
  point (§16). Decisions: the optional `management.bind` is **loopback/unix
  plaintext** only (§§3,6,9,15); `ca` mode trusts the **named private CA only**
  (not system roots) and `ca_name` must be path-safe (§§3,5,7). Also tightened PUT
  body/path-port match, auto-allocation release on replace, and pool exclusion of
  always-on/mgmt ports (§§6,9,10,11,12,16).
