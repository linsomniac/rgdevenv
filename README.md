# rgdevenv

A single-binary, Traefik-like **HTTPS reverse proxy for managing virtual
development environments** on a developer host.

`rgdevenv` terminates TLS with a **supplied** wildcard certificate (e.g.
`*.dev.example.com`), routes incoming requests by hostname to a single upstream
per mapping, and keeps a **port-reservation registry** so multiple dev servers
never fight over a port. It is managed through three equivalent interfaces — a
token-protected **web UI**, a **REST API**, and a **CLI** — all backed by the
same daemon and durable JSON state.

It is **route-only**: rgdevenv never launches or supervises your dev processes.
You (or an external supervisor) start them; rgdevenv just gives each one a stable
HTTPS hostname and an optional reserved port.

> Status: **v1 complete.** HTTPS proxy, management API, health checks, CLI, and
> web UI are implemented and tested. See [non-goals](#non-goals-v1) for what is
> intentionally out of scope.

---

## Contents

- [Why](#why)
- [Highlights](#highlights)
- [How it works](#how-it-works)
- [Concepts](#concepts)
- [Requirements](#requirements)
- [Build](#build)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [The three interfaces](#the-three-interfaces)
  - [CLI](#cli)
  - [REST API](#rest-api)
  - [Web UI](#web-ui)
- [Security model](#security-model)
- [Deployment & operations](#deployment--operations)
- [Project layout](#project-layout)
- [Development](#development)
- [Non-goals (v1)](#non-goals-v1)
- [Design spec](#design-spec)
- [License](#license)

---

## Why

When you run several services locally (an API on `:8001`, a Vite app on `:5173`,
a staging build on `:3000`, …), you end up juggling raw `localhost:PORT` URLs,
mixed HTTP/HTTPS, and port collisions. `rgdevenv` puts a single HTTPS front door
in front of all of them:

```
https://api.dev.example.com    →  http://localhost:8001
https://app.dev.example.com    →  http://localhost:5173
https://blog.dev.example.com   →  http://localhost:3000
```

Each environment gets a real, certificate-covered hostname under your wildcard,
served over a single `:443` listener, with live add/remove/update and no restart.

## Highlights

- **One wildcard cert, many hostnames.** SNI-based resolver; every name under
  the wildcard (including the management host) is covered. No ACME, no cert
  generation — you supply the cert/key.
- **Route-by-hostname.** `(listen port, canonical Host)` → exactly one upstream
  (HTTP or HTTPS, localhost or an allowlisted host).
- **Upstream TLS modes.** `verify` (system roots), `ca` (a named **private** CA
  from a server-side directory), or `skip` (dev only).
- **Port registry.** Allocate/return ports from a configurable pool as
  collision-free *reservations* (bookkeeping only — it never binds sockets).
  Auto-allocate a port when mapping to localhost.
- **Live reconfiguration.** Every change goes through a staged
  build → validate → pre-bind → persist → publish transaction. Invalid requests
  never partially mutate state; the running system is never left half-changed.
- **Durable state.** A single JSON file written atomically and durably
  (temp + fsync + rename + parent-dir fsync), `0600`, with a single-instance
  lock.
- **Protocol-aware health checks** with hysteresis, surfaced in `/status` and
  the UI.
- **SSRF / DNS-rebinding hardening.** A shared "safe dialer" validates every
  resolved address against deny rules and dials a pinned IP — used for both
  proxying and health checks.
- **WebSocket-aware** reverse proxy with sane timeouts, body-size limits, and
  forwarding headers set by the proxy (never trusted from the client).
- **Single static binary**, Go standard library, minimal dependencies, no JS
  build step.

## How it works

```
                  Developer browser / curl / CLI
                              │
        ┌─────────────────────┼──────────────────────────┐
        │  :80 redirect        │  :443 HTTPS (wildcard cert, SNI)
        ▼                      ▼
   308 → https://      ┌───────────────────────────────────────┐
                       │                rgdevenv                │
   Host: rgdevenv.dev… │   ┌────────────────────────────────┐  │  bearer token
   ───────────────────►│──►│  Management plane: REST + Web UI│  │  (constant-time,
                       │   └────────────────────────────────┘  │   rate-limited)
   Host: app.dev…      │   ┌────────────────────────────────┐  │
   ───────────────────►│──►│  Router (canonical Host)        │  │
                       │   │     → Mapping → Upstream         │  │
                       │   └───────────────┬────────────────┘  │
                       │   in-memory snapshot (atomic publish)  │
                       │   + durable JSON state + port registry │
                       └───────────────────┼────────────────────┘
                                           │ safe dialer (deny link-local /
                                           │ metadata / self; pinned IP)
                                           ▼
                              http(s)://localhost:8001   (your dev server)
```

- The **always-on** listeners are HTTPS on `:443` (terminates TLS, routes by
  Host) and an HTTP `:80` listener that issues `308` redirects to HTTPS (only
  for known, certificate-covered hosts — no open redirect).
- **On-demand listeners** open automatically when a mapping uses a non-default
  `listen_port`, and close when the last mapping on that port is removed.
- The **management plane** is reached either via the reserved
  `management_hostname` over `:443`, or via an optional isolated
  loopback/unix-socket bind (see [Security model](#security-model)).

## Concepts

| Concept | What it is |
|---|---|
| **Load balancer** | A hostname under your wildcard (e.g. `app.dev.example.com`). Has 0..N mappings. (The name is historical — there is exactly one upstream per mapping; this is virtual-hosting, not load balancing.) |
| **Mapping** | A `(listen_port, listen_tls)` front end that forwards to exactly one **upstream**. Unique within a load balancer by `listen_port`. |
| **Upstream** | `scheme` + `host` + `port`, plus (for `https`) a TLS mode `verify` / `ca` / `skip`. Must satisfy the upstream policy (localhost or allowlisted). |
| **Port allocation** | A reserved port number from the pool with a stable `id`, optional `owner`/`label`. Pure bookkeeping; apps bind the port themselves. |

All hostnames are **canonicalized** (lowercased, trailing dot stripped, port
stripped, IDNA-normalized) before they are used for validation, routing, auth,
persistence, or certificate matching.

## Requirements

- **Go 1.22+** to build.
- A **wildcard TLS certificate + key** covering the hostnames you'll serve
  (e.g. `*.dev.example.com`). rgdevenv does **not** generate certificates — for
  local development, [`mkcert`](https://github.com/FiloSottile/mkcert) or
  `openssl` work well.
- A **DNS wildcard record** (`A`/`AAAA`) for your domain pointing at the host —
  or matching `/etc/hosts` entries for local-only use. Managed externally.
- A **shared bearer token** (≥ 32 chars / 256-bit random) for the management
  plane.

## Build

```sh
go build -o rgdevenv ./cmd/rgdevenv
```

Or use the `Makefile` targets:

```sh
make build   # go build ./...
make test    # go test ./...
make race    # go test ./... -race
make vet     # go vet ./...
make lint    # golangci-lint run
make fmt     # gofmt -w .
```

The result is a single static binary. The same binary runs the daemon (`serve`)
and acts as the CLI client for every other subcommand.

## Quick start

This walks through a local setup using a separate loopback management bind, so
you can drive the API/UI over plain HTTP without first wiring the management
hostname's TLS.

**1. Generate a local wildcard cert** (example with `mkcert`):

```sh
mkcert -cert-file wildcard.crt -key-file wildcard.key '*.dev.example.com'
```

**2. Create a token:**

```sh
head -c 32 /dev/urandom | base64 > token   # ≥ 256-bit
chmod 600 token
```

**3. Write a minimal `config.toml`:**

```toml
https_port = 443
http_port  = 80
bind_addr  = "0.0.0.0"

cert_file = "wildcard.crt"
key_file  = "wildcard.key"

management_hostname = "rgdevenv.dev.example.com"
token_file          = "token"
state_file          = "state.json"

[management]
# Isolated, plaintext, loopback-only management listener (handy for local dev):
bind = "127.0.0.1:8443"

[port_pool]
start = 9000
end   = 9999

[upstreams]
allow = []           # localhost is always allowed; add hosts/CIDRs as needed
```

**4. Point DNS at the host.** For local-only use, add to `/etc/hosts`:

```
127.0.0.1  rgdevenv.dev.example.com app.dev.example.com
```

**5. Run the daemon** (binding `:80`/`:443` needs privileges — see
[Deployment](#deployment--operations)):

```sh
sudo ./rgdevenv serve --config config.toml
```

**6. Point the CLI at the management API.** Use the loopback bind for local dev:

```sh
export RGDEVENV_API="http://127.0.0.1:8443"
export RGDEVENV_TOKEN="$(cat token)"

./rgdevenv status
```

**7. Create a load balancer and a mapping:**

```sh
# Forward https://app.dev.example.com  →  http://localhost:5173
./rgdevenv lb  add app.dev.example.com --label "My app"
./rgdevenv map add app.dev.example.com --upstream http://localhost:5173

# Or let rgdevenv reserve a port and wire :443 → http://localhost:<port>
./rgdevenv map add api.dev.example.com --allocate --label "api worker"
```

Now `https://app.dev.example.com/` proxies to your local dev server.

**8. Open the web UI** at the management endpoint
(`http://127.0.0.1:8443/` for the loopback bind, or
`https://rgdevenv.dev.example.com/` over `:443`), paste the token, and manage
everything visually.

## Configuration

Static config is read at startup from a TOML file, overlaid by `RGDEVENV_*`
environment variables and (for client subcommands) flags.
**Precedence: flags > env > file > defaults.** Ports/bind address require a
restart to change; cert paths, the CA dir, and log level reload on `SIGHUP`.

Full annotated example:

```toml
https_port = 443
http_port  = 80            # 0 disables the HTTP→HTTPS redirect listener
bind_addr  = "0.0.0.0"

cert_file = "/etc/rgdevenv/certs/wildcard.crt"
key_file  = "/etc/rgdevenv/certs/wildcard.key"
# Optional extra SNI cert pairs (the top-level pair is the primary):
# [[certs]]
#   cert_file = "/etc/rgdevenv/certs/other.crt"
#   key_file  = "/etc/rgdevenv/certs/other.key"

ca_dir = "/etc/rgdevenv/cas"            # named upstream CAs: <ca_name>.pem

management_hostname = "rgdevenv.dev.example.com"
token_file = "/etc/rgdevenv/token"      # 0600; or RGDEVENV_TOKEN. ≥ 256-bit random

state_file = "/var/lib/rgdevenv/state.json"

[management]
# bind = "127.0.0.1:8443"               # optional isolated mgmt listener:
                                        # loopback/unix, PLAINTEXT (non-loopback refused).
                                        # empty = served via the wildcard :443 listener.
auth_rate_limit_per_min = 10            # failed-auth attempts per source IP → 429

[port_pool]
start = 9000                            # range must exclude http/https/mgmt-bind ports
end   = 9999

[upstreams]
# localhost is always allowed. Non-localhost hosts must match an entry here.
allow = ["build-box", "10.0.0.0/8"]     # hostnames and/or CIDRs
# link-local (169.254.0.0/16), cloud-metadata (169.254.169.254), and rgdevenv's
# own listener addresses are ALWAYS denied, even if listed above.

[log]
level  = "info"                         # debug | info | warn | error
access = true                           # per-request access logs

[health]
enabled          = true
interval_seconds = 15
timeout_seconds  = 5
path             = "/"                  # "" → TCP-connect probe; else HTTP(S) GET
threshold        = 2                    # consecutive like results to flip status
```

**Defaults** (used when the file/env omit them): `https_port=443`, `http_port=80`,
`bind_addr=0.0.0.0`, `ca_dir=/etc/rgdevenv/cas`, `token_file=/etc/rgdevenv/token`,
`state_file=/var/lib/rgdevenv/state.json`, `management.auth_rate_limit_per_min=10`,
`port_pool=9000–9999`, `log.level=info`, `log.access=true`, health enabled at
15s/5s/`"/"`/threshold 2. `cert_file`/`key_file`/`management_hostname` have no
default and must be supplied.

**Environment variables** (override the file): `RGDEVENV_TOKEN` (preferred over
`token_file`), `RGDEVENV_HTTPS_PORT`, `RGDEVENV_HTTP_PORT`, `RGDEVENV_BIND_ADDR`,
`RGDEVENV_CERT_FILE`, `RGDEVENV_KEY_FILE`, `RGDEVENV_CA_DIR`,
`RGDEVENV_MANAGEMENT_HOSTNAME`, `RGDEVENV_MANAGEMENT_BIND`, `RGDEVENV_TOKEN_FILE`,
`RGDEVENV_STATE_FILE`, `RGDEVENV_LOG_LEVEL`.

**Signals:** `SIGHUP` reloads certificates, the CA directory, and the log level
(validate-before-swap — on any failure the previous material is retained, never
silently downgraded). `SIGTERM`/`SIGINT` trigger a graceful shutdown (drain
in-flight requests, then exit).

## The three interfaces

The CLI, REST API, and web UI are functionally equivalent — pick whichever fits
your workflow.

### CLI

`rgdevenv serve` runs the daemon; every other subcommand is a thin REST client.

```
rgdevenv serve [--config FILE]                       run the proxy daemon (default /etc/rgdevenv/config.toml)

rgdevenv lb  add <name> [--label TEXT]               create a load balancer
rgdevenv lb  set <name> --label TEXT                 update its label
rgdevenv lb  rm  <name>                              delete it (and its mappings)
rgdevenv lb  ls                                      list load balancers

rgdevenv map add <name> --upstream URL [--listen-port 443] [--no-tls]
                        [--upstream-tls verify|skip|ca] [--ca-name NAME]
rgdevenv map add <name> --allocate [--listen-port 443] [--label TEXT]   # → http://localhost:<port>
rgdevenv map set <name> --listen-port N [--upstream URL] [--upstream-tls ...] [--ca-name NAME] [--allocate]
rgdevenv map rm  <name> [--listen-port 443]
rgdevenv map ls  <name>

rgdevenv port get [--owner NAME] [--label TEXT]      allocate a port (prints id + port)
rgdevenv port return <port>                          return a reserved port
rgdevenv port ls                                     show the pool and allocations

rgdevenv ca ls                                       available custom-CA names
rgdevenv status                                      version, listeners, counts, upstream health
```

Persistent client flags (on every subcommand): `--api URL`, `--token TOKEN`,
`--cli-config FILE`, `--insecure` (skip TLS verification — dev only), and
`--json` (machine-readable output instead of tables).

`--upstream` takes a URL like `http://localhost:9011` or
`https://build-box:8443`; scheme/host/port are parsed from it.

**Client config** comes from `~/.config/rgdevenv/cli.toml` (`0600`), overlaid by
`RGDEVENV_API` / `RGDEVENV_TOKEN` / `RGDEVENV_INSECURE`, then flags:

```toml
# ~/.config/rgdevenv/cli.toml
api      = "https://rgdevenv.dev.example.com"
token    = "…"
insecure = false
```

### REST API

Base URL: `https://<management_hostname>/api/v1` (or the management bind). Every
`/api/v1/*` request requires `Authorization: Bearer <token>` (constant-time
compare, rate-limited). The only unauthenticated surfaces are `/healthz` and the
static login shell.

| Method & path | Purpose | Success |
|---|---|---|
| `GET /lbs` | List load balancers (with mappings + health) | 200 |
| `POST /lbs` | Create `{ name, label? }` | 201 |
| `GET /lbs/{name}` | Get one | 200 |
| `PATCH /lbs/{name}` | Update `{ label }` | 200 |
| `DELETE /lbs/{name}` | Delete (cascades mappings + auto ports) | 204 |
| `POST /lbs/{name}/mappings` | Create a mapping | 201 |
| `PUT /lbs/{name}/mappings/{listen_port}` | Replace a mapping | 200 |
| `DELETE /lbs/{name}/mappings/{listen_port}` | Delete a mapping | 204 |
| `GET /ports` | Pool status: range, used/free, allocations | 200 |
| `POST /ports/allocate` | `{ owner?, label? }` → `{ id, port }` | 201 |
| `DELETE /ports/{port}` | Return a port (409 if in use) | 204 |
| `GET /cas` | List custom-CA names (from `ca_dir`) | 200 |
| `GET /status` | Version, listeners, counts, upstream health | 200 |
| `GET /healthz` | Liveness (unauthenticated) | 200 |

Mapping create/replace body:

```json
{
  "listen_port": 443,
  "listen_tls": true,
  "upstream": { "scheme": "http", "host": "localhost", "port": 9011,
                "tls": { "mode": "verify", "ca_name": null } },
  "allocate": false
}
```

When `allocate` is `true`, omit `upstream.port`; the server reserves a port and
sets the upstream to `http://localhost:<port>`. Error bodies are
`{ "error": "human message", "code": "machine_code" }`. Status codes: `400`
validation, `401` bad/missing token, `404` unknown, `409` conflict (duplicate
name, `(host, listen_port)` taken, listener TLS-mode conflict, pool exhausted,
return-in-use, listen_port inside the pool), `429` auth rate-limited.

### Web UI

Server-rendered shell with vanilla JavaScript that calls the REST API — all
assets embedded in the binary, **no build step**. Auth is bearer-only: the
static login shell loads unauthenticated, accepts the token, stores it in
`sessionStorage`, and sends it as the `Authorization` header on every call (no
cookies, so no CSRF surface). The dashboard provides full CRUD over load
balancers, mappings, and the port pool, plus quick-launch links, upstream-TLS
badges, and live health dots. It adapts to light/dark via
`prefers-color-scheme`.

## Security model

- **The token is the management boundary, not the hostname.** Bearer-only,
  constant-time comparison, never logged, never persisted to `state.json`,
  ≥ 256-bit. Repeated failures per source IP are rate-limited (`429`).
- **Optional network isolation.** Set `management.bind` to a loopback address or
  unix socket for an isolated **plaintext** management listener (a non-loopback
  plaintext bind is refused at startup). Reach it remotely over an SSH tunnel;
  the token is still required.
- **The data plane is intentionally open** — anything that resolves a mapped
  hostname and reaches the host can hit that upstream. Bound it with `bind_addr`
  and your network.
- **Upstream policy (SSRF / loop protection).** `localhost` is always allowed;
  any other host must match `[upstreams].allow`. Regardless of the allowlist,
  link-local, cloud-metadata, and rgdevenv's own listener addresses are always
  denied. A shared **safe dialer** resolves the name, validates **all** returned
  addresses, then dials a **pinned** IP with no re-resolution — closing the
  DNS-rebinding window — and is used for both proxying and health checks.
- **Forwarding headers are set by the proxy**, not trusted from clients; incoming
  `X-Forwarded-*`/`Forwarded` are stripped and replaced, and hop-by-hop headers
  are dropped.
- **Upstream TLS never silently downgrades.** `ca` mode trusts only the named
  private CA (not system roots); a missing/invalid CA marks the mapping
  *degraded* and it does not serve (fail-closed).
- **Secrets/files** (config, key, token) should be `0600`; the state file is
  created `0600`.

## Deployment & operations

- **Single binary, one instance per host** (enforced by the state-file lock).
  Run it as a long-lived daemon (e.g. a systemd unit).
- **Default locations:** config `/etc/rgdevenv/config.toml`, certs
  `/etc/rgdevenv/certs/`, custom CAs `/etc/rgdevenv/cas/`, state
  `/var/lib/rgdevenv/state.json`.
- **Binding `:80`/`:443`** needs privilege: prefer `CAP_NET_BIND_SERVICE` or
  systemd socket activation over running as root; if started as root, drop
  privileges afterward.
- **Reload/shutdown:** `SIGHUP` reloads certs/CA dir/log level;
  `SIGTERM`/`SIGINT` shut down gracefully.
- **State on startup:** missing → start empty; malformed or a newer schema
  `version` → abort with a clear error (no silent data loss); an unbindable
  listener port → start anyway, mark the affected mapping *degraded*, and surface
  it in `/status` and the logs.
- **Observability:** structured `log/slog` output, optional per-request access
  logs, an audit line per management mutation, and authenticated `/status` with
  upstream-health detail (public surfaces stay generic — no upstream identity
  leaks in `404`/`502` pages).

## Project layout

```
cmd/rgdevenv/       main: cobra root + subcommands (serve + REST clients)
internal/config/    static config load/merge (file/env), validation
internal/canon/     hostname/URL canonicalization (shared)
internal/store/     snapshot model + atomic JSON persistence + instance lock
internal/registry/  port pool allocate/return + lifecycle/reconcile
internal/proxy/     listeners, SNI/TLS, router, reverse proxy, redirect, errors
internal/upstream/  upstream policy (allowlist/deny) + CA pool loading + safe dialer
internal/health/    protocol-aware upstream health checks
internal/auth/      bearer token + rate limiter
internal/txn/       staged transaction (build/validate/pre-bind/persist/publish)
internal/api/       REST handlers + UI mount
internal/ui/        embedded web UI assets (html/css/js)
internal/client/    REST client used by the CLI
docs/superpowers/   design spec and implementation plans
```

## Development

```sh
make test    # unit + integration tests
make race    # the full suite under the race detector
make vet
make lint    # golangci-lint
make fmt     # gofmt -w .
```

Conventions (from the design spec):

- `gofmt`/`goimports`, `go vet`, and `golangci-lint` must be clean.
- Table-driven tests; the suite must pass under `go test ./... -race`.
- `AIDEV-NOTE:` / `AIDEV-TODO:` / `AIDEV-QUESTION:` anchor comments mark complex,
  important, or subtle code — `grep` for them before changing related code, and
  keep them updated. Don't remove them without reason.
- Boring over clever; maintainability over cleverness.

## Non-goals (v1)

Intentionally out of scope for now (some are planned — see the design spec's
"Future" section):

- Launching/supervising dev processes (kept external; a future "mode B").
- Raw, non-HTTP TCP/TLS passthrough.
- Multiple upstreams per mapping / real load balancing (exactly one upstream).
- Certificate generation / ACME (the cert is supplied).
- Multi-user accounts / RBAC (a single shared token).
- DNS management (the wildcard record is configured externally).

## Design spec

The full design — architecture, domain model, routing rules, transaction
semantics, security model, and testing strategy — lives in
[`docs/superpowers/specs/2026-06-13-rgdevenv-design.md`](docs/superpowers/specs/2026-06-13-rgdevenv-design.md).
Per-phase implementation plans are under `docs/superpowers/plans/`.

## License

This project is dedicated to the public domain under the
[CC0 1.0 Universal](LICENSE) (Creative Commons Zero, `CC0-1.0`) public-domain
dedication. To the extent possible under law, the author has waived all
copyright and related rights to this work. You may copy, modify, distribute,
and use it — including for commercial purposes — without asking permission or
providing attribution.

Authored by Sean Reifschneider.
