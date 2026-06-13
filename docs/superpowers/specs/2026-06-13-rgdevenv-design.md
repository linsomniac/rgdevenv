# rgdevenv — Design Spec

- **Date:** 2026-06-13
- **Status:** Approved in brainstorming; pending implementation plan
- **Authors:** Sean Reifschneider, with Claude

## 1. Summary

`rgdevenv` is a single-binary Go service — a Traefik-like HTTPS reverse proxy for
managing multiple virtual development environments on a developer host. It
terminates TLS using a **supplied** wildcard certificate (e.g.
`*.sean.realgo.com`), routes incoming requests by hostname to a single upstream
per mapping, and maintains a collision-free **port registry** for dev servers. It
ships with a token-protected **web UI**, a **REST API**, and a **CLI** that are
functionally equivalent.

It is **route-only**: rgdevenv never launches or supervises the upstream dev
processes. Those are started by the developer (or, in the future, by an external
supervisor that integrates via the API).

## 2. Goals and non-goals

### Goals (v1)

- Add/remove named **load balancers** (hostnames under the wildcard).
- Add/remove **mappings**: a `(listener port, hostname)` forwards to exactly one
  upstream (HTTP or HTTPS, localhost or another host) with upstream TLS options
  `verify` / `ca` (custom CA file) / `skip`.
- Allocate/return ports from a configurable pool as a **collision-free registry**,
  with a convenience flow to auto-allocate a port when mapping to localhost.
- A **web management UI** (full CRUD + quick-launch links + status), a **REST
  API**, and a **CLI** — all equivalent.
- **Live reconfiguration** without restart; durable JSON state across restarts.

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
| Listeners | Always-on `:443` HTTPS + `:80` redirect; other ports opened on demand |
| TLS cert | Supply-only (cert/key paths); no generation/ACME |
| Upstream TLS | `verify` / `ca` (custom CA) / `skip` |
| Port pool | Configurable range; collision-free registry; apps bind directly |
| Management access | Reserved hostname over `:443`, token-gated |
| Web UI scope | Full CRUD + quick-launch links + status |
| Persistence | Single JSON state file, atomic writes |

## 4. Architecture and components

A single daemon process (`rgdevenv serve`) holds all state in memory, applies
changes live, and persists them to a JSON file. The same binary provides the CLI
subcommands, which are thin clients over the REST API.

Components:

- **Listeners** — TCP listeners per public port, each with a TLS mode.
- **TLS** — loads the supplied cert/key pair(s); an SNI resolver selects the cert.
- **Router** — maps `(listener, Host)` to a mapping; the reserved management
  hostname routes to the management plane.
- **Reverse proxy** — an `httputil.ReverseProxy` per upstream; applies the
  upstream TLS mode; transparently supports WebSocket upgrades.
- **Port registry** — the configurable pool; allocate/return with owner + label.
  Pure bookkeeping — never in the request path.
- **Store** — in-memory source of truth + atomic JSON persistence.
- **Management plane** — REST API + server-rendered web UI, served only on the
  reserved hostname, token-gated.
- **CLI** — `rgdevenv` subcommands that call the REST API.

## 5. Domain model

- **LoadBalancer** — a hostname under the wildcard (e.g.
  `RG-27788-cpcart-cleanups.sean.realgo.com`). Fields: `name` (FQDN, unique key),
  `label`, `created_at`. Has 1..N **Mappings**.
- **Mapping** — belongs to a LoadBalancer. Fields: `listen_port` (default `443`),
  `listen_tls` (default `true`), and one **Upstream**. Unique within a
  LoadBalancer by `listen_port`.
- **Upstream** — `scheme` (`http`|`https`), `host`, `port`, and (for `https`) a
  `tls` block `{ mode: verify|ca|skip, ca_file? }`. Exactly one per mapping.
- **PortAllocation** — a reserved port from the pool. Fields: `port`, `owner`
  (optional, usually a LoadBalancer name), `label`, `allocated_at`. Independent of
  mappings; linked only by convention (and the auto-allocate convenience).

Cardinalities: `LoadBalancer 1—N Mapping`, `Mapping 1—1 Upstream`,
`PortAllocation` standalone.

## 6. Listener model

- Always-on: HTTPS on `https_port` (default `443`, terminates TLS, routes by
  SNI/Host) and an HTTP redirect listener on `http_port` (default `80`, issues
  `301` to the HTTPS URL). Setting `http_port = 0` disables the redirect listener.
- **On-demand listeners:** when a mapping references a `listen_port` that has no
  active listener, rgdevenv opens one. Default TLS mode is HTTPS (the wildcard
  cert covers it); `listen_tls = false` makes it plain HTTP. When the last mapping
  on an on-demand port is removed, its listener is closed.
- **One TLS mode per port:** a `listen_port` has exactly one TLS mode across all
  mappings. A mapping whose `listen_tls` conflicts with the port's established
  mode is rejected (`409 Conflict`).
- The reserved `https_port` and `http_port` cannot be repurposed to other modes.
- **Management routing:** on the HTTPS listener, `Host == management_hostname`
  routes to the management plane (after auth); all other hostnames route to user
  mappings. Management is served only on the HTTPS listener.

## 7. TLS and certificates

- rgdevenv loads one or more supplied `(cert_file, key_file)` pairs at startup and
  serves them via an SNI-based `tls.Config.GetCertificate` resolver. With a single
  wildcard pair, every `*.sean.realgo.com` name (including the management
  hostname) is covered.
- No certificate generation, no ACME. Client trust of the issuing CA is handled
  outside rgdevenv (developers install the CA into their trust stores).
- **Reload:** `SIGHUP` reloads the certificate(s) and re-reads static config
  values that are safe to change at runtime (e.g. log level), without dropping
  existing connections where practical.
- **Upstream TLS:** for `https` upstreams, the per-upstream `tls.mode` controls
  certificate verification — `verify` (default system roots), `ca` (verify against
  `ca_file`), or `skip` (`InsecureSkipVerify`, dev-only).

## 8. Request flows

**Data plane (user traffic):**
1. Client opens `https://<name>.sean.realgo.com` → hits the `:443` listener.
2. TLS terminates with the wildcard cert (selected by SNI).
3. Router looks up `(443, Host)` → mapping. Unknown host → branded `404`.
4. `httputil.ReverseProxy` forwards to the upstream using its scheme/host/port and
   TLS mode. Upstream unreachable → branded `502`.
5. Response (including WebSocket upgrades) streams back to the client.

**Management plane:**
1. Browser/CLI → `https://rgdevenv.sean.realgo.com/...` (or `/api/v1/...`).
2. Auth check (bearer token for API; basic-auth/cookie login for UI). Failure →
   `401`.
3. Handler validates the request, applies the change to in-memory state under a
   single mutex, persists `state.json` atomically, and reconfigures listeners/
   routes live.

## 9. Configuration (static)

Static config is read at startup from a TOML file, overridable by environment
variables (`RGDEVENV_*`) and flags. Precedence: **flags > env > file > defaults**.
Changing values that affect bound sockets (ports, bind address) requires a
restart; cert paths and log level can be reloaded via `SIGHUP`.

```toml
# /etc/rgdevenv/config.toml
https_port = 443
http_port  = 80            # 0 disables the HTTP->HTTPS redirect listener
bind_addr  = "0.0.0.0"

cert_file = "/etc/rgdevenv/certs/wildcard.crt"
key_file  = "/etc/rgdevenv/certs/wildcard.key"
# Optional extra SNI cert pairs:
# [[certs]]
#   cert_file = "/etc/rgdevenv/certs/other.crt"
#   key_file  = "/etc/rgdevenv/certs/other.key"

management_hostname = "rgdevenv.sean.realgo.com"
token_file = "/etc/rgdevenv/token"     # or: token = "..."

state_file = "/var/lib/rgdevenv/state.json"

[port_pool]
start = 9000
end   = 9999

[log]
level  = "info"
access = true
```

## 10. State (dynamic) and persistence

Dynamic state is mutated at runtime via the API and persisted to a single JSON
file with **atomic writes** (write temp file in the same dir, `fsync`, `rename`).
The file is loaded on startup; in-memory state is the runtime source of truth. All
mutations are serialized by one mutex.

```json
{
  "version": 1,
  "load_balancers": [
    {
      "name": "RG-27788-cpcart-cleanups.sean.realgo.com",
      "label": "cpcart cleanups",
      "created_at": "2026-06-13T09:30:00Z",
      "mappings": [
        { "listen_port": 443, "listen_tls": true,
          "upstream": { "scheme": "http", "host": "localhost", "port": 9011,
                        "tls": { "mode": "verify" } } }
      ]
    }
  ],
  "port_allocations": [
    { "port": 9011, "owner": "RG-27788-cpcart-cleanups.sean.realgo.com",
      "label": "cpcart web", "allocated_at": "2026-06-13T09:29:00Z" }
  ]
}
```

`version` enables forward-compatible schema migration.

## 11. Port registry semantics

- `allocate` returns the **lowest free port** in `[start, end]`, marked used with
  optional `owner` and `label`. Exhaustion → explicit error (`409`).
- `return` frees a port for reuse.
- Independent of mappings, **plus** the convenience: creating a `localhost`
  mapping with `allocate=true` (CLI `--allocate`) allocates a port and wires the
  upstream to `http://localhost:<port>` in one step.
- Allocations persist in `state.json` and survive restarts.
- The registry only tracks ports; it does not verify whether anything is bound to
  them (apps bind directly).

## 12. REST API

Base: `https://<management_hostname>/api/v1`. Auth: `Authorization: Bearer
<token>` on every endpoint except `/healthz`. Request/response bodies are JSON.

| Method & path | Purpose | Success |
|---|---|---|
| `GET /lbs` | List load balancers (with mappings + health) | 200 |
| `POST /lbs` | Create `{ name, label? }` | 201 |
| `GET /lbs/{name}` | Get one | 200 |
| `DELETE /lbs/{name}` | Delete (and its mappings) | 204 |
| `POST /lbs/{name}/mappings` | Create mapping (see body below) | 201 |
| `DELETE /lbs/{name}/mappings/{listen_port}` | Delete mapping | 204 |
| `GET /ports` | Pool status: range, used/free, allocations | 200 |
| `POST /ports/allocate` | `{ owner?, label? }` → `{ port }` | 201 |
| `DELETE /ports/{port}` | Return a port | 204 |
| `GET /status` | Version, listeners, counts, upstream health | 200 |
| `GET /healthz` | Liveness (unauthenticated) | 200 |

Mapping create body:
```json
{ "listen_port": 443, "listen_tls": true,
  "upstream": { "scheme": "http", "host": "localhost", "port": 9011,
                "tls": { "mode": "verify", "ca_file": null } },
  "allocate": false }
```
When `allocate` is `true`, omit `upstream.port`; the server allocates a port and
sets the upstream to `http://localhost:<port>`.

**Status codes:** `400` validation error, `401` bad/missing token, `404` unknown
resource, `409` conflict (duplicate name, port already mapped, listener TLS-mode
conflict, pool exhausted), `502` is a data-plane (not API) response. Error body:
`{ "error": "human message", "code": "machine_code" }`.

Every mutation validates **before** applying; an invalid request never partially
mutates or corrupts `state.json`.

## 13. CLI

A single `rgdevenv` binary. `serve` runs the daemon; all other subcommands call
the REST API.

```
rgdevenv serve [--config FILE]                      run the proxy daemon

rgdevenv lb add  <name> [--label TEXT]
rgdevenv lb rm   <name>
rgdevenv lb ls

rgdevenv map add <name> --upstream URL [--listen-port 443] [--no-tls]
                        [--upstream-tls verify|skip|ca] [--ca-file FILE]
rgdevenv map add <name> --allocate [--listen-port 443]   # port -> localhost:<port>
rgdevenv map rm  <name> [--listen-port 443]
rgdevenv map ls  <name>

rgdevenv port get [--owner NAME] [--label TEXT]     # allocate; prints the port
rgdevenv port return <port>
rgdevenv port ls

rgdevenv status
```

- `--upstream` accepts a URL like `http://localhost:9011` or
  `https://build-box:8443`; scheme/host/port are parsed from it.
- `--allocate` without `--upstream` allocates a port and maps
  `:443 → http://localhost:<port>` (the convenience flow).
- Client config: `RGDEVENV_API` (default `https://<management_hostname>`) and
  `RGDEVENV_TOKEN`, or `~/.config/rgdevenv/cli.toml`.
- Output is human-readable tables by default, with `--json` for scripting.

## 14. Web management UI

Server-rendered (Go `html/template`) with a small amount of vanilla JavaScript
that calls the REST API; all assets embedded via `embed.FS`. **No JS build step.**
Served only on the management hostname behind auth.

Dashboard contents:
- **Header:** product name, run status, active listeners, version, logout.
- **Load balancers** (left): each row shows the hostname as a quick-launch link
  (opens `https://<name>` in a new tab), label, mapping chips
  (`443 → localhost:9011` + upstream-TLS badge), an up/down health dot, and
  actions (`+ map`, delete). Rows expand to manage mappings inline (add form with
  listen port, upstream URL, upstream-TLS mode, and an `allocate` checkbox; per-
  mapping delete). An `+ Add load balancer` action creates one.
- **Port pool** (right): range + used/free usage bar, allocations table
  (`port`, `owner`, `label`, `return`), and an `Allocate` button.

All create/delete actions are available from the UI (full CRUD).

## 15. Security model

- The **management plane** (API + UI) is reachable **only** via the reserved
  hostname and requires the shared token (bearer for API; basic-auth/cookie for
  UI). No other hostname can reach management.
- The **data plane is intentionally open**: anything that resolves the wildcard
  and reaches the host can hit mapped upstreams — that is the purpose. Exposure is
  limited via `bind_addr` and the surrounding network, not per-route auth.
- The token is read from `token_file` (preferred) or `token`; it is never logged
  and never written to `state.json`.
- Binding `:80`/`:443` requires elevated privilege (run as root, use
  `CAP_NET_BIND_SERVICE`, or front with the OS); documented in operations.
- Client trust of the certificate's CA is established out-of-band.

## 16. Error handling and resilience

- **Data plane:** unknown host → branded `404` page; upstream unreachable/refused/
  timeout → branded `502` page naming the load balancer and upstream.
- **Management plane:** validate → apply → persist, all under one mutex; a failed
  validation returns an error and leaves state untouched. A failed disk persist
  rolls back the in-memory change and returns `500`.
- **Startup:** a missing `state.json` starts empty; a malformed one aborts startup
  with a clear error (operator fixes or removes it) rather than silently losing
  data.
- **Shutdown:** graceful — stop accepting new connections, drain in-flight
  requests within a timeout, then exit.

## 17. Observability

- Structured logging via `log/slog`. Per-request **access logs** (host, method,
  path, upstream, status, bytes, latency) when `log.access` is on, and an **audit
  line** per management mutation (actor = client IP, action, target).
- `GET /status` reports version, active listeners, counts, and upstream health.
- **Health checks:** a background checker periodically `net.DialTimeout`s each
  distinct upstream `host:port` (configurable interval) and records up/down/
  unknown, surfaced in `/status` and the UI dot. Passive failures from live
  proxying also update the status.

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
internal/store/          in-memory state + atomic JSON persistence
internal/registry/       port pool allocate/return
internal/proxy/          listeners, SNI/TLS, router, reverse proxy, error pages
internal/health/         background upstream health checks
internal/api/            REST handlers + auth middleware
internal/ui/             html/template + embedded assets
internal/client/         REST client used by the CLI
docs/superpowers/specs/  this spec
```

**Conventions:** `gofmt`/`goimports`, `go vet`, `golangci-lint`; `AIDEV-NOTE:` /
`AIDEV-TODO:` / `AIDEV-QUESTION:` anchor comments for complex/important/subtle
code; boring over clever.

## 19. Testing strategy

- **Unit:** port registry (allocation order, exhaustion, return/reuse), store
  (atomic write, load, round-trip, corrupt-file handling), config precedence,
  router matching (incl. management hostname + listener TLS-mode conflicts),
  validation rules.
- **API:** `net/http/httptest` over the handlers, including auth (401), conflicts
  (409), and full CRUD round-trips.
- **Proxy integration:** spin up test upstreams (HTTP and HTTPS with a self-signed
  CA) and assert routing, the three upstream TLS modes, WebSocket upgrade, and the
  502/404 error pages.
- **CLI:** run subcommands against an in-process test API server; assert
  human-readable and `--json` output.
- Table-driven tests; tests must pass under `go test ./...` and `-race`.

## 20. Deployment and operations

- Distributed as a single static binary; runs as a long-lived daemon (e.g. a
  systemd unit) — one instance per developer host.
- Default file locations: config `/etc/rgdevenv/config.toml`, cert under
  `/etc/rgdevenv/certs/`, state `/var/lib/rgdevenv/state.json`.
- Requires privilege to bind `:80`/`:443` (root or `CAP_NET_BIND_SERVICE`).
- `SIGHUP` reloads certs/log level; `SIGTERM` triggers graceful shutdown.
- The wildcard DNS record pointing at the host is configured externally.

## 21. Future / out of scope

- **Process management ("mode B"):** launch/stop/supervise upstream dev servers,
  integrated via the API once the external experiment solidifies.
- **Raw TCP / TLS passthrough** as an additional listener mode (non-HTTP
  services).
- **Multiple upstreams per mapping** (round-robin/failover) — real load balancing.
- **Dynamic listener CRUD** via the API (declaring/removing listener ports
  explicitly rather than on-demand).
- **Certificate generation / ACME**, a Prometheus metrics endpoint, and per-route
  authentication.

## 22. Assumptions

- The wildcard DNS record resolves to the rgdevenv host (managed externally).
- A single developer operates each instance (one shared token, no RBAC).
- Clients trust the certificate's issuing CA via out-of-band installation.
- Inter-app traffic is HTTP/HTTPS in v1 (raw TCP is future).
