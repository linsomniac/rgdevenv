# rgdevenv — Phase 1: Foundations + Data-Plane Proxy — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a working, testable HTTPS reverse-proxy data plane for `rgdevenv` — it loads config + a JSON state file, terminates TLS with a supplied wildcard cert, routes by canonical Host to a single upstream per mapping (HTTP/HTTPS with `verify`/`ca`/`skip`), enforces SSRF/loop protection through a shared safe dialer, redirects `:80→:443`, and serves generic error pages.

**Architecture:** A single daemon (`rgdevenv serve`) loads static config (TOML+env+flags), opens a flock-guarded JSON store, and publishes an immutable in-memory snapshot. A per-port listener set dispatches `(listen_port, canonical Host) → mapping`; each mapping is an `httputil.ReverseProxy` whose `Transport.DialContext` is a shared **safe dialer** that resolves, validates **every** returned IP against deny rules (link-local/metadata/self-loop) + the upstream allowlist, then dials a **pinned** validated IP (no re-resolution). State is mutated only by hand-editing `state.json` in this phase; the staged transaction, REST API, CLI, auth, health checks, and web UI arrive in **Phase 2**.

**Tech Stack:** Go 1.22+ (stdlib `net/http`, `crypto/tls`, `net/http/httputil`, `log/slog`, `embed`), `spf13/cobra` (CLI), `BurntSushi/toml` (config), `golang.org/x/net/idna` (host canonicalization).

---

## Plan scope (Phase 1 of 2)

This plan implements the **data plane** described in the design spec
(`docs/superpowers/specs/2026-06-13-rgdevenv-design.md`). At the end you can:

- run `rgdevenv serve` with a config + a hand-written `state.json`;
- reach `https://<name>.sean.realgo.com` and have it proxied to the configured
  upstream with the correct upstream-TLS behavior;
- get a safe `308` redirect from `:80`, generic `404`/`502` pages, WebSocket
  upgrades, and SSRF/DNS-rebinding/loop protection.

**Deferred to Phase 2** (do **not** build here): `internal/auth`, `internal/txn`,
`internal/api`, `internal/health`, `internal/ui`, `internal/client`, the
`lb`/`map`/`port`/`ca`/`status` CLI subcommands, port `allocate`/`return`
mutation, and serving the management plane (in this phase the management hostname
returns `404` — a seam is left for Phase 2). Port-allocation **reconcile** at
startup and the **registry data model** are built here because the daemon needs
them at load time.

Spec section references (e.g. *§8*) point into the design spec above.

---

## Post-execution revisions

This plan was executed via subagent-driven development with per-task spec + code-quality
review. The following deviations from the task text above were made during execution
(the committed code is the source of truth):

- **Dependencies / `go` version.** `golang.org/x/net@latest` requires `go 1.25`, which
  conflicts with the spec's `go 1.22` minimum. Pinned `x/net v0.21.0` (added in Task 2 on
  first import) and `BurntSushi/toml v1.3.2` (added in Task 3 on first import); `x/text`
  pulled transitively at `v0.14.0`. Task 1 fetches only `cobra` up front. `go.mod` stays at
  `go 1.22`.
- **`canon.Host` (Task 2)** also rejects the root label, empty labels (`..`), and **all IP
  literals** (IPv4 and IPv6) — an IP is not a valid hostname (§5).
- **`config.applyEnv` (Task 3)** returns an error and fails loudly on a non-integer port env
  var (no silent drop). The precedence test uses an env port outside the pool (the plan's
  `9443` was inside the default pool and would be correctly rejected by `normalize`).
- **`proxy.Listeners.Reconcile` (Task 16)** uses map **key-presence** checks
  (`_, ok := desired[port]`) for the keep/close and always-on decisions — the plan's boolean
  *value* lookups were a bug (a `false`/plaintext port would be opened then immediately
  closed). Removed listeners are also drained **outside** the lock.
- **Tooling.** `.golangci.yml` updated to golangci-lint **v2** format (`govet` + `staticcheck`;
  the broader set was noisy on intentional `defer Close()`/`defer os.Remove()` patterns).
- Many tasks gained **additional review-driven tests** beyond the plan's minimum (SNI
  selection, dialer fallback/resolve-error, router degrade/`WithoutPorts`, `Apply`
  bind-failure, body-size limit, mgmt-bind pool exclusion, etc.).

**Deferred to Phase 2** (noted by the final review, not bugs): an overall max-duration cap on
upgraded/streaming connections (§8), and a default request-body-size limit (the mechanism is
wired and tested; the default is `0`/unlimited, which suits a dev proxy that handles uploads).

---

## File structure (Phase 1)

```
go.mod / go.sum
Makefile
.golangci.yml
cmd/rgdevenv/
  main.go              cobra root; wires `serve`
  serve.go             `serve` subcommand: load → lock → reconcile → Apply → run → signals
internal/canon/
  canon.go             Host(s) canonicalization (shared key form)
  canon_test.go
internal/config/
  config.go            Config struct; Load (defaults→file→env); normalize/validate
  config_test.go
internal/store/
  model.go             State/LoadBalancer/Mapping/Upstream/PortAllocation + Validate
  store.go             Open (flock) + atomic durable Save + snapshot pointer
  model_test.go
  store_test.go
internal/registry/
  registry.go          Reconcile (free orphaned auto allocations; detect dangling refs)
  registry_test.go
internal/upstream/
  policy.go            allowlist + deny rules + SelfGuard
  ca.go                ValidCAName + LoadCA (path-safe named private CA)
  dialer.go            shared safe dialer (resolve → validate-all → pin → dial)
  policy_test.go
  ca_test.go
  dialer_test.go
internal/proxy/
  errors.go            generic 404/502 writers (no upstream leak)
  tls.go               SNI CertResolver (GetCertificate, Covers, Reload)
  reverseproxy.go      BuildReverseProxy (TLS modes, header rewrite, transport limits)
  redirect.go          :80 safe 308 handler (no open redirect)
  router.go            RoutingTable (build, Lookup, HasHost, DesiredListeners, Degraded)
  listeners.go         per-port listener set (Reconcile open/close, bind-failure report)
  server.go            Server.Apply (build→reconcile→publish) + per-port dispatch
  errors_test.go
  tls_test.go
  reverseproxy_test.go
  redirect_test.go
  router_test.go
  listeners_test.go
  server_test.go
  integration_test.go  end-to-end matrix (spec §19)
docs/superpowers/plans/  this plan
```

**Module path:** `github.com/realgo/rgdevenv` (adjust if the canonical import
path differs; it is a one-line change in `go.mod` plus imports).

## Conventions

- **TDD:** write the failing test, watch it fail, write minimal code, watch it
  pass, commit. One logical unit per task.
- **Formatting:** `gofmt`/`goimports`; `go vet ./...` and `go test ./... -race`
  must pass before each commit.
- **Anchor comments:** add `AIDEV-NOTE:` for subtle/important/security-sensitive
  code (the safe dialer, atomic writes, validate-before-swap), `AIDEV-TODO:` for
  Phase-2 seams. Never remove existing `AIDEV-` comments.
- **Commits:** small and frequent; one per task (or per step where noted). End
  commit messages with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Boring over clever. YAGNI: do not build Phase-2 packages.

---

## Task 1: Project scaffolding

**Files:**
- Create: `go.mod`, `Makefile`, `.golangci.yml`
- Create: `cmd/rgdevenv/main.go`, `cmd/rgdevenv/serve.go`

- [ ] **Step 1: Initialize the module and directory tree**

Run:
```bash
cd /home/sean/aix/rgdevenv
go mod init github.com/realgo/rgdevenv
mkdir -p cmd/rgdevenv \
  internal/canon internal/config internal/store internal/registry \
  internal/upstream internal/proxy
```

- [ ] **Step 2: Add dependencies**

Run:
```bash
go get github.com/spf13/cobra@latest
go get github.com/BurntSushi/toml@latest
go get golang.org/x/net@latest
```
Expected: `go.mod`/`go.sum` updated with the three modules.

- [ ] **Step 3: Write the cobra root** — `cmd/rgdevenv/main.go`

```go
// Command rgdevenv is an HTTPS reverse proxy that manages multiple virtual dev
// environments on a developer host. `serve` runs the daemon; other subcommands
// (added in Phase 2) are thin REST clients.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "rgdevenv",
		Short:         "HTTPS reverse proxy for managing dev environments",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newServeCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rgdevenv: error:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Write the serve stub** — `cmd/rgdevenv/serve.go`

```go
package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// newServeCmd returns the `serve` subcommand. The body is implemented in Task 19.
func newServeCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the rgdevenv proxy daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// AIDEV-TODO: implemented in Task 19 (load → lock → Apply → run).
			_ = configPath
			return errors.New("serve: not implemented yet")
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/rgdevenv/config.toml", "path to config file")
	return cmd
}
```

- [ ] **Step 5: Write `Makefile`**

```make
.PHONY: build test race vet fmt lint
build:
	go build ./...
test:
	go test ./...
race:
	go test ./... -race
vet:
	go vet ./...
fmt:
	gofmt -w .
lint:
	golangci-lint run
```

- [ ] **Step 6: Write `.golangci.yml`**

```yaml
run:
  timeout: 3m
linters:
  enable:
    - govet
    - staticcheck
    - errcheck
    - ineffassign
    - unused
```

- [ ] **Step 7: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: builds cleanly; `rgdevenv serve` would print `serve: not implemented yet`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum Makefile .golangci.yml cmd/
git commit -m "chore: scaffold rgdevenv module, cobra root, and serve stub

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Hostname canonicalization (`internal/canon`)

Canonicalization is the single key form for routing, validation, auth,
persistence, and cert matching (*§5*). All hostnames pass through here.

**Files:**
- Create: `internal/canon/canon.go`
- Test: `internal/canon/canon_test.go`

- [ ] **Step 1: Write the failing test** — `internal/canon/canon_test.go`

```go
package canon

import "testing"

func TestHost(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercase", "Example.COM", "example.com", false},
		{"trailing dot", "host.example.com.", "host.example.com", false},
		{"strip port", "host.example.com:8443", "host.example.com", false},
		{"mixed case + dot + port", "RG-27788-CpCart.Sean.Realgo.com.:443", "rg-27788-cpcart.sean.realgo.com", false},
		{"idna", "Bücher.example", "xn--bcher-kva.example", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"space inside", "bad host", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Host(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Host(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Host(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Host(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/canon/ -run TestHost -v`
Expected: FAIL — `undefined: Host`.

- [ ] **Step 3: Write the implementation** — `internal/canon/canon.go`

```go
// Package canon canonicalizes hostnames into the single form used as a key for
// routing, validation, auth, persistence, and certificate matching.
package canon

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// Host canonicalizes s: strips an optional :port, strips a trailing dot,
// lowercases, and applies IDNA (lookup profile) normalization. Malformed hosts
// return an error.
//
// AIDEV-NOTE: This is the ONLY canonicalizer. Routing, auth, persistence, and
// cert matching must all key off this exact form to avoid host-confusion
// bypasses (§5, §15).
func Host(s string) (string, error) {
	h := strings.TrimSpace(s)
	if h == "" {
		return "", fmt.Errorf("canon: empty host")
	}
	// Strip an optional port. SplitHostPort errors when there is no port, in
	// which case we keep the original string.
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	h = strings.ToLower(h)
	if h == "" {
		return "", fmt.Errorf("canon: empty host after normalization")
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", fmt.Errorf("canon: %q: %w", s, err)
	}
	return ascii, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/canon/ -run TestHost -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/canon/
git commit -m "feat(canon): hostname canonicalization

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Static configuration (`internal/config`)

Loads TOML, overlays `RGDEVENV_*` env, validates static invariants: port ranges,
**pool disjointness** from always-on/mgmt ports (*§6*), canonical management
hostname, and the **loopback/unix-only plaintext** management bind rule (*§15*).
Precedence is **flags > env > file > defaults** (flags are applied by the caller;
this package handles defaults→file→env).

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test** — `internal/config/config_test.go`

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 443 || cfg.HTTPPort != 80 {
		t.Fatalf("defaults wrong: %d %d", cfg.HTTPSPort, cfg.HTTPPort)
	}
	if cfg.PortPool.Start != 9000 || cfg.PortPool.End != 9999 {
		t.Fatalf("default pool wrong: %+v", cfg.PortPool)
	}
}

func TestLoadPrecedence(t *testing.T) {
	path := writeTOML(t, "https_port = 8443\nmanagement_hostname = \"Mgmt.Example.COM\"\n")

	// file overrides default
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 8443 {
		t.Fatalf("file precedence: got %d", cfg.HTTPSPort)
	}
	// management hostname is canonicalized
	if cfg.ManagementHostname != "mgmt.example.com" {
		t.Fatalf("mgmt host not canonicalized: %q", cfg.ManagementHostname)
	}

	// env overrides file
	t.Setenv("RGDEVENV_HTTPS_PORT", "9443")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 9443 {
		t.Fatalf("env precedence: got %d", cfg.HTTPSPort)
	}
}

func TestNormalizeRejectsPoolOverlap(t *testing.T) {
	path := writeTOML(t, "https_port = 9500\n[port_pool]\nstart = 9000\nend = 9999\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error: https_port inside pool")
	}
}

func TestNormalizeRejectsNonLoopbackMgmtBind(t *testing.T) {
	path := writeTOML(t, "[management]\nbind = \"0.0.0.0:8443\"\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error: non-loopback plaintext mgmt bind")
	}
}

func TestNormalizeAcceptsLoopbackMgmtBind(t *testing.T) {
	path := writeTOML(t, "[management]\nbind = \"127.0.0.1:8443\"\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("loopback mgmt bind should be allowed: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation** — `internal/config/config.go`

```go
// Package config loads and validates rgdevenv's static configuration from a TOML
// file overlaid with RGDEVENV_* environment variables. Precedence is
// flags > env > file > defaults (flags are applied by the caller).
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/realgo/rgdevenv/internal/canon"
)

type Config struct {
	HTTPSPort int    `toml:"https_port"`
	HTTPPort  int    `toml:"http_port"`
	BindAddr  string `toml:"bind_addr"`

	CertFile string     `toml:"cert_file"`
	KeyFile  string     `toml:"key_file"`
	Certs    []CertPair `toml:"certs"`

	CADir string `toml:"ca_dir"`

	ManagementHostname string `toml:"management_hostname"`
	TokenFile          string `toml:"token_file"`

	StateFile string `toml:"state_file"`

	Management ManagementConfig `toml:"management"`
	PortPool   PortPoolConfig   `toml:"port_pool"`
	Upstreams  UpstreamsConfig  `toml:"upstreams"`
	Log        LogConfig        `toml:"log"`
}

type CertPair struct {
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

type ManagementConfig struct {
	Bind                string `toml:"bind"`
	AuthRateLimitPerMin int    `toml:"auth_rate_limit_per_min"`
}

type PortPoolConfig struct {
	Start int `toml:"start"`
	End   int `toml:"end"`
}

type UpstreamsConfig struct {
	Allow []string `toml:"allow"`
}

type LogConfig struct {
	Level  string `toml:"level"`
	Access bool   `toml:"access"`
}

// Default returns the built-in defaults (§9).
func Default() *Config {
	return &Config{
		HTTPSPort:          443,
		HTTPPort:           80,
		BindAddr:           "0.0.0.0",
		CADir:              "/etc/rgdevenv/cas",
		TokenFile:          "/etc/rgdevenv/token",
		StateFile:          "/var/lib/rgdevenv/state.json",
		Management:         ManagementConfig{AuthRateLimitPerMin: 10},
		PortPool:           PortPoolConfig{Start: 9000, End: 9999},
		Log:                LogConfig{Level: "info", Access: true},
	}
}

// Load builds a Config from defaults, then the file (if present), then env, and
// validates it. A missing file is not an error (defaults+env are used).
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		}
	}
	applyEnv(cfg)
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("RGDEVENV_HTTPS_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.HTTPSPort = n
		}
	}
	if v := os.Getenv("RGDEVENV_HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.HTTPPort = n
		}
	}
	if v := os.Getenv("RGDEVENV_BIND_ADDR"); v != "" {
		c.BindAddr = v
	}
	if v := os.Getenv("RGDEVENV_CERT_FILE"); v != "" {
		c.CertFile = v
	}
	if v := os.Getenv("RGDEVENV_KEY_FILE"); v != "" {
		c.KeyFile = v
	}
	if v := os.Getenv("RGDEVENV_CA_DIR"); v != "" {
		c.CADir = v
	}
	if v := os.Getenv("RGDEVENV_MANAGEMENT_HOSTNAME"); v != "" {
		c.ManagementHostname = v
	}
	if v := os.Getenv("RGDEVENV_MANAGEMENT_BIND"); v != "" {
		c.Management.Bind = v
	}
	if v := os.Getenv("RGDEVENV_TOKEN_FILE"); v != "" {
		c.TokenFile = v
	}
	if v := os.Getenv("RGDEVENV_STATE_FILE"); v != "" {
		c.StateFile = v
	}
	if v := os.Getenv("RGDEVENV_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
}

func (c *Config) normalize() error {
	if c.HTTPSPort < 1 || c.HTTPSPort > 65535 {
		return fmt.Errorf("config: https_port %d out of range", c.HTTPSPort)
	}
	if c.HTTPPort < 0 || c.HTTPPort > 65535 {
		return fmt.Errorf("config: http_port %d out of range", c.HTTPPort)
	}
	if c.PortPool.Start < 1 || c.PortPool.End > 65535 || c.PortPool.Start > c.PortPool.End {
		return fmt.Errorf("config: invalid port_pool [%d,%d]", c.PortPool.Start, c.PortPool.End)
	}
	// AIDEV-NOTE: pool disjointness (§6). The pool must not contain any always-on
	// or management bind port; per-mapping listen_port disjointness is enforced
	// later (store.Validate / Phase-2 txn).
	for _, p := range c.alwaysOnPorts() {
		if p >= c.PortPool.Start && p <= c.PortPool.End {
			return fmt.Errorf("config: port %d is inside port_pool [%d,%d]", p, c.PortPool.Start, c.PortPool.End)
		}
	}
	if c.ManagementHostname != "" {
		h, err := canon.Host(c.ManagementHostname)
		if err != nil {
			return fmt.Errorf("config: management_hostname: %w", err)
		}
		c.ManagementHostname = h
	}
	return c.validateMgmtBind()
}

// validateMgmtBind enforces the loopback/unix-only plaintext rule (§15): a
// non-loopback TCP management bind is refused at startup.
func (c *Config) validateMgmtBind() error {
	b := strings.TrimSpace(c.Management.Bind)
	if b == "" || isUnixBind(b) {
		return nil
	}
	host, _, err := net.SplitHostPort(b)
	if err != nil {
		return fmt.Errorf("config: management.bind %q: %w", b, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("config: management.bind %q must be a loopback address or unix socket (plaintext only)", b)
	}
	return nil
}

func (c *Config) alwaysOnPorts() []int {
	ports := []int{c.HTTPSPort}
	if c.HTTPPort > 0 {
		ports = append(ports, c.HTTPPort)
	}
	if p, ok := c.mgmtBindPort(); ok {
		ports = append(ports, p)
	}
	return ports
}

func (c *Config) mgmtBindPort() (int, bool) {
	b := strings.TrimSpace(c.Management.Bind)
	if b == "" || isUnixBind(b) {
		return 0, false
	}
	_, ps, err := net.SplitHostPort(b)
	if err != nil {
		return 0, false
	}
	p, err := strconv.Atoi(ps)
	if err != nil {
		return 0, false
	}
	return p, true
}

// AllCertPairs returns the primary (top-level) pair first, then any extras.
func (c *Config) AllCertPairs() []CertPair {
	var pairs []CertPair
	if c.CertFile != "" && c.KeyFile != "" {
		pairs = append(pairs, CertPair{CertFile: c.CertFile, KeyFile: c.KeyFile})
	}
	return append(pairs, c.Certs...)
}

func isUnixBind(b string) bool {
	return strings.HasPrefix(b, "/") || strings.HasPrefix(b, "@") || strings.HasPrefix(b, "unix:")
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: `gofmt` + vet + commit**

```bash
gofmt -w internal/config/
go vet ./internal/config/
git add internal/config/
git commit -m "feat(config): static config load with precedence and validation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: State model (`internal/store/model.go`)

The persisted + in-memory model (*§10*). Same struct is loaded from disk and
published in memory.

**Files:**
- Create: `internal/store/model.go`
- Test: `internal/store/model_test.go`

- [ ] **Step 1: Write the failing test** — `internal/store/model_test.go`

```go
package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStateJSONRoundTrip(t *testing.T) {
	in := &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name:      "rg-1.sean.realgo.com",
			Label:     "demo",
			CreatedAt: time.Date(2026, 6, 13, 9, 30, 0, 0, time.UTC),
			Mappings: []Mapping{
				{
					ListenPort: 443,
					ListenTLS:  true,
					Upstream:   Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}},
				},
				{
					ListenPort: 8080,
					ListenTLS:  false, // must survive round-trip
					Upstream:   Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: UpstreamTLS{Mode: "verify"}},
				},
			},
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	// AIDEV-NOTE: listen_tls must NOT be omitempty; false has to persist.
	if !strings.Contains(string(b), `"listen_tls":false`) {
		t.Fatalf("listen_tls=false not serialized: %s", b)
	}
	var out State
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.LoadBalancers[0].Mappings[1].ListenTLS {
		t.Fatal("ListenTLS false not preserved")
	}
	if out.LoadBalancers[0].Mappings[0].Upstream.Port != 9011 {
		t.Fatal("upstream port lost")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestStateJSONRoundTrip -v`
Expected: FAIL — `undefined: State`.

- [ ] **Step 3: Write the implementation** — `internal/store/model.go`

```go
// Package store holds rgdevenv's dynamic state: the model, atomic+durable JSON
// persistence, a single-instance lock, and the atomically published snapshot.
package store

import "time"

// CurrentVersion is the on-disk schema version. A file with a newer version is
// refused (§10); older versions would be migrated forward.
const CurrentVersion = 1

type State struct {
	Version         int              `json:"version"`
	LoadBalancers   []LoadBalancer   `json:"load_balancers"`
	PortAllocations []PortAllocation `json:"port_allocations"`
}

type LoadBalancer struct {
	Name      string    `json:"name"` // canonical FQDN; unique key
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Mappings  []Mapping `json:"mappings"`
}

type Mapping struct {
	ListenPort    int      `json:"listen_port"`
	ListenTLS     bool     `json:"listen_tls"` // NOT omitempty: false must persist
	Upstream      Upstream `json:"upstream"`
	AllocationID  string   `json:"allocation_id,omitempty"`
	AutoAllocated bool     `json:"auto_allocated,omitempty"`
}

type Upstream struct {
	Scheme string      `json:"scheme"` // http | https
	Host   string      `json:"host"`
	Port   int         `json:"port"`
	TLS    UpstreamTLS `json:"tls"`
}

type UpstreamTLS struct {
	Mode   string `json:"mode"` // verify | ca | skip
	CAName string `json:"ca_name,omitempty"`
}

type PortAllocation struct {
	ID          string    `json:"id"`
	Port        int       `json:"port"`
	Owner       string    `json:"owner,omitempty"`
	Label       string    `json:"label,omitempty"`
	Auto        bool      `json:"auto,omitempty"`
	AllocatedAt time.Time `json:"allocated_at"`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestStateJSONRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/model.go internal/store/model_test.go
git commit -m "feat(store): state model with JSON round-trip

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Persistence, instance lock, and snapshot (`internal/store/store.go`)

Atomic+durable writes (temp→fsync→rename→parent-dir fsync), `0600`, a flock
single-instance lock, and an atomically published snapshot pointer (*§10*).

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test** — `internal/store/store_test.go`

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStatePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

func TestOpenMissingStartsEmpty(t *testing.T) {
	s, err := Open(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Snapshot(); got.Version != CurrentVersion || len(got.LoadBalancers) != 0 {
		t.Fatalf("expected empty v%d state, got %+v", CurrentVersion, got)
	}
}

func TestSaveRoundTripAndMode(t *testing.T) {
	path := tempStatePath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Snapshot()
	st.LoadBalancers = []LoadBalancer{{Name: "a.example.com"}}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
	s.Close()

	// Reopen and confirm persistence.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Snapshot(); len(got.LoadBalancers) != 1 || got.LoadBalancers[0].Name != "a.example.com" {
		t.Fatalf("reopen lost data: %+v", got)
	}
}

func TestInstanceLock(t *testing.T) {
	path := tempStatePath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected second Open to fail on instance lock")
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for newer version")
	}
}

func TestLoadRejectsMalformed(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{ not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for malformed state")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestOpen|TestSave|TestInstance|TestLoad' -v`
Expected: FAIL — `undefined: Open`.

- [ ] **Step 3: Write the implementation** — `internal/store/store.go`

```go
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// Store owns the persisted state file, a single-instance lock, and the
// atomically published in-memory snapshot.
type Store struct {
	path     string
	lockFile *os.File
	snap     atomic.Pointer[State]
	mu       sync.Mutex // serializes Save
}

// Open acquires the instance lock, loads (or initializes) state, and publishes
// the first snapshot. Callers run store.Validate on the snapshot before serving.
func Open(path string) (*Store, error) {
	lf, err := acquireLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	st, err := loadState(path)
	if err != nil {
		lf.Close()
		return nil, err
	}
	s := &Store{path: path, lockFile: lf}
	s.snap.Store(st)
	return s, nil
}

// Snapshot returns the current immutable snapshot (lock-free read).
func (s *Store) Snapshot() *State { return s.snap.Load() }

// Publish atomically swaps in a new snapshot pointer.
func (s *Store) Publish(st *State) { s.snap.Store(st) }

// Close releases the instance lock.
func (s *Store) Close() error {
	if s.lockFile == nil {
		return nil
	}
	err := s.lockFile.Close()
	s.lockFile = nil
	return err
}

// Save writes st atomically and durably and is safe for concurrent callers.
//
// AIDEV-NOTE: Durability ordering (§10): write temp → fsync temp → rename →
// fsync parent dir. CreateTemp yields 0600. Do not reorder; the parent-dir
// fsync is what makes the rename durable across a crash.
func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("store: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		tmp.Close()
		return fmt.Errorf("store: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("store: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("store: open dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("store: fsync dir: %w", err)
	}
	return nil
}

// acquireLock takes an exclusive, non-blocking flock. flock associates the lock
// with the open file description, so a second Open (even in-process) fails.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: another rgdevenv instance holds %s: %w", path, err)
	}
	return f, nil
}

func loadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: CurrentVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var st State
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("store: parse %s: %w", path, err)
	}
	if st.Version > CurrentVersion {
		return nil, fmt.Errorf("store: state version %d is newer than supported %d", st.Version, CurrentVersion)
	}
	if st.Version == 0 {
		st.Version = CurrentVersion
	}
	return &st, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): atomic durable persistence, instance lock, snapshot

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Structural validation (`store.Validate`)

Invariants checked before a snapshot is served/published (*§10*): unique LB
names, unique `listen_port` per LB, **one TLS mode per `listen_port`** across all
LBs, unique allocation ids/ports, and no dangling `allocation_id` references.

**Files:**
- Modify: `internal/store/model.go` (append `Validate`)
- Modify: `internal/store/model_test.go` (append tests)

- [ ] **Step 1: Write the failing tests** — append to `internal/store/model_test.go`

```go
func validState() *State {
	return &State{
		Version: CurrentVersion,
		LoadBalancers: []LoadBalancer{{
			Name: "a.example.com",
			Mappings: []Mapping{
				{ListenPort: 443, ListenTLS: true, AllocationID: "alloc-1",
					Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLS{Mode: "verify"}}},
			},
		}},
		PortAllocations: []PortAllocation{{ID: "alloc-1", Port: 9011}},
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(validState()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
}

func TestValidateDuplicateLB(t *testing.T) {
	st := validState()
	st.LoadBalancers = append(st.LoadBalancers, LoadBalancer{Name: "a.example.com"})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate LB error")
	}
}

func TestValidateTLSModeConflictAcrossLBs(t *testing.T) {
	st := validState()
	// Another LB reuses port 443 but as plaintext -> conflict.
	st.LoadBalancers = append(st.LoadBalancers, LoadBalancer{
		Name: "b.example.com",
		Mappings: []Mapping{{ListenPort: 443, ListenTLS: false,
			Upstream: Upstream{Scheme: "http", Host: "localhost", Port: 9012, TLS: UpstreamTLS{Mode: "verify"}}}},
	})
	if err := Validate(st); err == nil {
		t.Fatal("expected TLS-mode conflict on port 443")
	}
}

func TestValidateDanglingAllocation(t *testing.T) {
	st := validState()
	st.LoadBalancers[0].Mappings[0].AllocationID = "ghost"
	if err := Validate(st); err == nil {
		t.Fatal("expected dangling allocation error")
	}
}

func TestValidateDuplicateAllocationPort(t *testing.T) {
	st := validState()
	st.PortAllocations = append(st.PortAllocations, PortAllocation{ID: "alloc-2", Port: 9011})
	if err := Validate(st); err == nil {
		t.Fatal("expected duplicate allocation port error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run TestValidate -v`
Expected: FAIL — `undefined: Validate`.

- [ ] **Step 3: Append the implementation** — to `internal/store/model.go`

```go
import "fmt" // add to the existing import block (alongside "time")

// Validate checks structural invariants required before a State is served or
// published (§10).
//
// AIDEV-NOTE: "one TLS mode per listen_port" is GLOBAL across all LBs (§6), not
// per-LB. Config-dependent checks (pool disjointness, upstream policy, cert
// coverage) are enforced by config.normalize, the proxy router, and the Phase-2
// transaction — not here.
func Validate(st *State) error {
	seenLB := make(map[string]bool)
	portTLS := make(map[int]bool)
	portSeen := make(map[int]bool)

	for _, lb := range st.LoadBalancers {
		if lb.Name == "" {
			return fmt.Errorf("store: load balancer with empty name")
		}
		if seenLB[lb.Name] {
			return fmt.Errorf("store: duplicate load balancer %q", lb.Name)
		}
		seenLB[lb.Name] = true

		seenPort := make(map[int]bool)
		for _, m := range lb.Mappings {
			if seenPort[m.ListenPort] {
				return fmt.Errorf("store: lb %q has duplicate listen_port %d", lb.Name, m.ListenPort)
			}
			seenPort[m.ListenPort] = true

			if portSeen[m.ListenPort] && portTLS[m.ListenPort] != m.ListenTLS {
				return fmt.Errorf("store: listen_port %d has conflicting TLS modes", m.ListenPort)
			}
			portSeen[m.ListenPort] = true
			portTLS[m.ListenPort] = m.ListenTLS
		}
	}

	allocByID := make(map[string]bool)
	allocByPort := make(map[int]bool)
	for _, a := range st.PortAllocations {
		if a.ID == "" {
			return fmt.Errorf("store: allocation with empty id")
		}
		if allocByID[a.ID] {
			return fmt.Errorf("store: duplicate allocation id %q", a.ID)
		}
		allocByID[a.ID] = true
		if allocByPort[a.Port] {
			return fmt.Errorf("store: duplicate allocation for port %d", a.Port)
		}
		allocByPort[a.Port] = true
	}
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID != "" && !allocByID[m.AllocationID] {
				return fmt.Errorf("store: lb %q mapping :%d references missing allocation %q",
					lb.Name, m.ListenPort, m.AllocationID)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (all store tests).

- [ ] **Step 5: Commit**

```bash
git add internal/store/model.go internal/store/model_test.go
git commit -m "feat(store): structural state validation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Port-allocation reconcile (`internal/registry`)

The daemon reconciles allocations against mappings at startup (*§10, §11*):
free orphaned `auto=true` allocations. (Conflicts are already rejected by
`store.Validate`; `allocate`/`return` mutation is Phase 2.)

**Files:**
- Create: `internal/registry/registry.go`
- Test: `internal/registry/registry_test.go`

- [ ] **Step 1: Write the failing test** — `internal/registry/registry_test.go`

```go
package registry

import (
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func TestReconcileFreesOrphanAuto(t *testing.T) {
	st := &store.State{
		PortAllocations: []store.PortAllocation{
			{ID: "a", Port: 9000, Auto: true},  // orphan auto -> freed
			{ID: "b", Port: 9001, Auto: false}, // manual orphan -> kept
		},
	}
	got, changed := Reconcile(st)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only manual alloc kept, got %+v", got)
	}
}

func TestReconcileKeepsReferencedAuto(t *testing.T) {
	st := &store.State{
		LoadBalancers: []store.LoadBalancer{{
			Name:     "x",
			Mappings: []store.Mapping{{ListenPort: 443, AllocationID: "a"}},
		}},
		PortAllocations: []store.PortAllocation{{ID: "a", Port: 9000, Auto: true}},
	}
	got, changed := Reconcile(st)
	if changed {
		t.Fatal("expected changed=false")
	}
	if len(got) != 1 {
		t.Fatalf("referenced auto alloc dropped: %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -v`
Expected: FAIL — `undefined: Reconcile`.

- [ ] **Step 3: Write the implementation** — `internal/registry/registry.go`

```go
// Package registry implements port-pool reservation lifecycle. Phase 1 uses only
// startup Reconcile; allocate/return mutation arrives with the Phase-2 API.
package registry

import "github.com/realgo/rgdevenv/internal/store"

// Reconcile frees orphaned auto-allocated ports — auto=true allocations not
// referenced by any mapping (§10, §11). It returns the cleaned allocation slice
// and whether anything changed. Manually allocated ports are always retained.
func Reconcile(st *store.State) (allocs []store.PortAllocation, changed bool) {
	referenced := make(map[string]bool)
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if m.AllocationID != "" {
				referenced[m.AllocationID] = true
			}
		}
	}
	kept := make([]store.PortAllocation, 0, len(st.PortAllocations))
	for _, a := range st.PortAllocations {
		if a.Auto && !referenced[a.ID] {
			changed = true
			continue
		}
		kept = append(kept, a)
	}
	return kept, changed
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/registry/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/
git commit -m "feat(registry): reconcile orphaned auto port allocations

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Upstream policy (`internal/upstream/policy.go`)

The SSRF/loop allow + deny rules (*§15*): localhost always allowed; other hosts
need the allowlist; link-local/cloud-metadata/self-listener always denied.

**Files:**
- Create: `internal/upstream/policy.go`
- Test: `internal/upstream/policy_test.go`

- [ ] **Step 1: Write the failing test** — `internal/upstream/policy_test.go`

```go
package upstream

import (
	"net"
	"testing"
)

func TestAllowedHost(t *testing.T) {
	p := NewPolicy([]string{"build-box", "10.0.0.0/8"})
	cases := []struct {
		host    string
		wantErr bool
	}{
		{"localhost", false},
		{"127.0.0.1", false},
		{"build-box", false},
		{"BUILD-BOX", false},
		{"10.1.2.3", false},
		{"evil.example.com", true},
		{"8.8.8.8", true},
	}
	for _, c := range cases {
		if err := p.AllowedHost(c.host); (err != nil) != c.wantErr {
			t.Errorf("AllowedHost(%q) err=%v wantErr=%v", c.host, err, c.wantErr)
		}
	}
}

func TestCheckDialIPDeniesMetadataAndLinkLocal(t *testing.T) {
	p := NewPolicy(nil)
	var self SelfGuard
	for _, ip := range []string{"169.254.169.254", "169.254.0.1", "fe80::1"} {
		if p.CheckDialIP(net.ParseIP(ip), 80, self) == "" {
			t.Errorf("expected %s to be denied", ip)
		}
	}
	if p.CheckDialIP(net.ParseIP("93.184.216.34"), 80, self) != "" {
		t.Error("public IP must not be denied by deny rules")
	}
}

func TestCheckDialIPSelfLoop(t *testing.T) {
	p := NewPolicy(nil)
	self := SelfGuard{ListenPorts: map[int]bool{443: true}}
	if p.CheckDialIP(net.ParseIP("127.0.0.1"), 443, self) == "" {
		t.Error("loopback on a listener port must be denied (loop)")
	}
	if p.CheckDialIP(net.ParseIP("127.0.0.1"), 9011, self) != "" {
		t.Error("loopback on a non-listener port (dev server) must be allowed")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/upstream/ -run 'TestAllowedHost|TestCheckDialIP' -v`
Expected: FAIL — `undefined: NewPolicy`.

- [ ] **Step 3: Write the implementation** — `internal/upstream/policy.go`

```go
// Package upstream enforces the SSRF/loop policy and loads named upstream CAs.
package upstream

import (
	"fmt"
	"net"
	"strings"
)

// denyNets are always denied regardless of the allowlist (§15): link-local IPv4
// (which includes cloud-metadata 169.254.169.254) and link-local IPv6.
var denyNets = mustCIDRs("169.254.0.0/16", "fe80::/10")

// Policy decides whether an upstream host/IP may be dialed.
type Policy struct {
	allowHosts map[string]bool
	allowNets  []*net.IPNet
}

// NewPolicy builds a Policy from allow entries (hostnames and/or CIDRs). An entry
// that parses as a CIDR is a network; otherwise it is a hostname.
func NewPolicy(allow []string) *Policy {
	p := &Policy{allowHosts: make(map[string]bool)}
	for _, e := range allow {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			p.allowNets = append(p.allowNets, n)
			continue
		}
		p.allowHosts[strings.ToLower(e)] = true
	}
	return p
}

// SelfGuard identifies rgdevenv's own listener endpoints for loop protection.
type SelfGuard struct {
	LocalIPs    []net.IP
	ListenPorts map[int]bool
}

func (g SelfGuard) isSelf(ip net.IP, port int) bool {
	if !g.ListenPorts[port] {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, lip := range g.LocalIPs {
		if lip.Equal(ip) {
			return true
		}
	}
	return false
}

// AllowedHost reports whether a host NAME (or IP literal) is permitted, without
// resolving DNS. localhost/loopback are always allowed; a name must be in the
// allowlist; an IP literal must fall in an allow CIDR.
//
// AIDEV-NOTE: name-level gate for create/validate time. The dial-time gate
// (Dialer) additionally validates EVERY resolved IP and pins it (§8, §15).
func (p *Policy) AllowedHost(host string) error {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		for _, n := range p.allowNets {
			if n.Contains(ip) {
				return nil
			}
		}
		return fmt.Errorf("upstream: IP %s not in allowlist", host)
	}
	if p.allowHosts[h] {
		return nil
	}
	return fmt.Errorf("upstream: host %q not in allowlist", host)
}

// CheckDialIP returns a non-empty reason if dialing ip:port must be denied
// regardless of the allowlist (§15).
func (p *Policy) CheckDialIP(ip net.IP, port int, self SelfGuard) string {
	for _, n := range denyNets {
		if n.Contains(ip) {
			return "link-local/cloud-metadata address denied"
		}
	}
	if self.isSelf(ip, port) {
		return "self/listener address denied (loop protection)"
	}
	return ""
}

func mustCIDRs(cidrs ...string) []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("upstream: bad built-in CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/upstream/ -run 'TestAllowedHost|TestCheckDialIP' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/upstream/policy.go internal/upstream/policy_test.go
git commit -m "feat(upstream): SSRF allow/deny policy with self-loop guard

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Named private CA loading (`internal/upstream/ca.go`)

Path-safe `ca_name` → `<ca_dir>/<ca_name>.pem`, loaded into a pool that trusts
**only** that CA (*§7*).

**Files:**
- Create: `internal/upstream/ca.go`
- Test: `internal/upstream/ca_test.go`

- [ ] **Step 1: Write the failing test** — `internal/upstream/ca_test.go`

```go
package upstream

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidCAName(t *testing.T) {
	ok := []string{"build-box", "corp_ca", "ca.internal", "CA-1"}
	bad := []string{"", ".", "..", "../etc/passwd", "a/b", `a\b`, "a..b", "with space"}
	for _, n := range ok {
		if !ValidCAName(n) {
			t.Errorf("ValidCAName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if ValidCAName(n) {
			t.Errorf("ValidCAName(%q) = true, want false", n)
		}
	}
}

// writeTestCAPEM writes a self-signed CA as <dir>/<name>.pem and returns dir.
func writeTestCAPEM(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, name+".pem"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadCA(t *testing.T) {
	dir := writeTestCAPEM(t, "corp")
	pool, err := LoadCA(dir, "corp")
	if err != nil || pool == nil {
		t.Fatalf("LoadCA: pool=%v err=%v", pool, err)
	}
	if _, err := LoadCA(dir, "missing"); err == nil {
		t.Fatal("expected error for missing CA")
	}
	if _, err := LoadCA(dir, "../corp"); err == nil {
		t.Fatal("expected error for path traversal")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/upstream/ -run 'TestValidCAName|TestLoadCA' -v`
Expected: FAIL — `undefined: ValidCAName`.

- [ ] **Step 3: Write the implementation** — `internal/upstream/ca.go`

```go
package upstream

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var caNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidCAName reports whether name is a path-safe CA identifier (§7): no path
// separators, no "..", conservative charset. The on-disk file is
// <ca_dir>/<name>.pem.
func ValidCAName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return caNameRe.MatchString(name)
}

// LoadCA loads the named private CA into a fresh pool that trusts ONLY this CA
// (system roots are not included) — used for upstream tls mode "ca" (§7).
func LoadCA(caDir, name string) (*x509.CertPool, error) {
	if !ValidCAName(name) {
		return nil, fmt.Errorf("upstream: invalid ca_name %q", name)
	}
	path := filepath.Join(caDir, name+".pem")
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("upstream: read CA %q: %w", name, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("upstream: no certificates in CA %q", name)
	}
	return pool, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/upstream/ -run 'TestValidCAName|TestLoadCA' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/upstream/ca.go internal/upstream/ca_test.go
git commit -m "feat(upstream): path-safe named private CA loading

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Shared safe dialer (`internal/upstream/dialer.go`)

The single chokepoint for **all** upstream connections (proxy + Phase-2 health
checks). Resolve → validate every IP → dial a **pinned** validated IP with no
re-resolution (*§8, §15*). This is the DNS-rebinding defense.

**Files:**
- Create: `internal/upstream/dialer.go`
- Test: `internal/upstream/dialer_test.go`

- [ ] **Step 1: Write the failing test** — `internal/upstream/dialer_test.go`

```go
package upstream

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeConn is a stand-in net.Conn; its methods are never called in these tests.
type fakeConn struct{ net.Conn }

func TestDialerPinsValidatedIP(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	var dialed string
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5")}, nil
	}
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "build-box:8443"); err != nil {
		t.Fatal(err)
	}
	// AIDEV-NOTE: dialing the IP (not the host) proves no OS re-resolution.
	if dialed != "203.0.113.5:8443" {
		t.Fatalf("expected to dial the pinned IP, got %q", dialed)
	}
}

func TestDialerDeniesIfAnyAddressDenied(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5"), net.ParseIP("169.254.169.254")}, nil
	}
	called := false
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return fakeConn{}, nil
	}
	_, err := d.DialContext(context.Background(), "tcp", "build-box:8443")
	if err == nil {
		t.Fatal("expected deny-if-any error")
	}
	if called {
		t.Fatal("must not dial when any address is denied")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PolicyError, got %T", err)
	}
}

func TestDialerRejectsNonAllowlistedBeforeResolve(t *testing.T) {
	d := New(NewPolicy(nil), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		t.Fatal("resolve must not be called for a non-allowlisted host")
		return nil, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "evil.example.com:80"); err == nil {
		t.Fatal("expected policy rejection")
	}
}

func TestDialerAllowsLocalhostDevServer(t *testing.T) {
	self := SelfGuard{ListenPorts: map[int]bool{443: true}} // 443 is ours; 9011 is not
	d := New(NewPolicy(nil), self, 0)
	var dialed string
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "localhost:9011"); err != nil {
		t.Fatal(err)
	}
	if dialed != "127.0.0.1:9011" {
		t.Fatalf("dialed %q", dialed)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/upstream/ -run TestDialer -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation** — `internal/upstream/dialer.go`

```go
package upstream

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

// PolicyError indicates an upstream dial was refused by policy. The reverse proxy
// logs it but returns a generic 502 to clients (§8).
type PolicyError struct {
	Host   string
	IP     net.IP
	Reason string
}

func (e *PolicyError) Error() string {
	if e.IP != nil {
		return fmt.Sprintf("upstream policy: %s (%s): %s", e.Host, e.IP, e.Reason)
	}
	return fmt.Sprintf("upstream policy: %s: %s", e.Host, e.Reason)
}

// Dialer is the single shared SAFE dialer used by both the reverse proxy and the
// (Phase-2) health checker. It resolves the upstream name, validates EVERY
// returned address against the allowlist + deny rules, then dials a PINNED
// validated IP with no OS re-resolution — closing the DNS-rebinding window.
type Dialer struct {
	policy *Policy
	self   SelfGuard

	// Resolve and Dial are injectable for tests; defaults use the system
	// resolver and a net.Dialer.
	Resolve func(ctx context.Context, host string) ([]net.IP, error)
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
}

// New builds a safe dialer with the given policy, self-guard, and dial timeout.
func New(policy *Policy, self SelfGuard, timeout time.Duration) *Dialer {
	base := &net.Dialer{Timeout: timeout}
	d := &Dialer{policy: policy, self: self}
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	d.Dial = base.DialContext
	return d
}

// DialContext matches http.Transport.DialContext.
//
// AIDEV-NOTE: SECURITY-CRITICAL — the ONLY place upstream connections are made.
// Order is deliberate: allow-check (name) -> resolve -> deny-check (every IP) ->
// dial a PINNED IP. Never pass `host` to the OS dialer (that re-resolves and
// reopens the rebinding window). Deny-if-ANY: one bad address fails the whole dial.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("upstream: bad address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("upstream: bad port %q", portStr)
	}

	if err := d.policy.AllowedHost(host); err != nil {
		return nil, &PolicyError{Host: host, Reason: err.Error()}
	}

	ips, err := d.Resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("upstream: resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("upstream: no addresses for %q", host)
	}
	for _, ip := range ips {
		if reason := d.policy.CheckDialIP(ip, port, d.self); reason != "" {
			return nil, &PolicyError{Host: host, IP: ip, Reason: reason}
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := d.Dial(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/upstream/ -v`
Expected: PASS (all upstream tests, including `-race`).

- [ ] **Step 5: Commit**

```bash
git add internal/upstream/dialer.go internal/upstream/dialer_test.go
git commit -m "feat(upstream): shared safe dialer with validated-IP pinning

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Generic data-plane error pages (`internal/proxy/errors.go`)

Generic `404`/`502` that never leak upstream identity (*§8, §16*).

**Files:**
- Create: `internal/proxy/errors.go`
- Test: `internal/proxy/errors_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/errors_test.go`

```go
package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	writeNotFound(w)
	if w.Code != 404 {
		t.Fatalf("code = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "404") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestWriteBadGatewayNoLeak(t *testing.T) {
	w := httptest.NewRecorder()
	writeBadGateway(w)
	if w.Code != 502 {
		t.Fatalf("code = %d", w.Code)
	}
	body := strings.ToLower(w.Body.String())
	if strings.Contains(body, "localhost") || strings.Contains(body, "upstream") {
		t.Fatalf("502 body leaks detail: %q", body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run 'TestWrite' -v`
Expected: FAIL — `undefined: writeNotFound`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/errors.go`

```go
// Package proxy implements the data plane: TLS termination, routing, reverse
// proxying, the :80 redirect, listeners, and generic error pages.
package proxy

import (
	"io"
	"net/http"
)

// writeNotFound and writeBadGateway emit generic data-plane errors. They never
// reveal upstream identity (§8, §16); detail goes to logs only.
func writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, "404 Not Found\n")
}

func writeBadGateway(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	io.WriteString(w, "502 Bad Gateway\n")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run 'TestWrite' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/errors.go internal/proxy/errors_test.go
git commit -m "feat(proxy): generic non-leaking 404/502 pages

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: SNI certificate resolver (`internal/proxy/tls.go`)

Loads supplied cert/key pairs, selects by SNI, answers cert-coverage queries, and
reloads with **validate-before-swap** (*§7*).

**Files:**
- Create: `internal/proxy/tls.go`
- Test: `internal/proxy/tls_test.go` (also defines `writeWildcardCert`, reused by later proxy tests)

- [ ] **Step 1: Write the failing test** — `internal/proxy/tls_test.go`

```go
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

// writeWildcardCert writes a self-signed cert+key (default SAN *.sean.realgo.com)
// and returns their file paths. Reused across proxy tests.
func writeWildcardCert(t *testing.T, dnsNames ...string) (certFile, keyFile string) {
	t.Helper()
	if len(dnsNames) == 0 {
		dnsNames = []string{"*.sean.realgo.com"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestCertResolverCovers(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Covers("rg-1.sean.realgo.com") {
		t.Error("wildcard should cover subdomain")
	}
	if r.Covers("example.com") {
		t.Error("must not cover unrelated host")
	}
}

func TestCertResolverGetCertificate(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := r.GetCertificate(&tls.ClientHelloInfo{ServerName: "rg-1.sean.realgo.com"})
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate: cert=%v err=%v", cert, err)
	}
}

func TestCertResolverReloadValidateBeforeSwap(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload([]config.CertPair{{CertFile: "/nonexistent", KeyFile: "/nonexistent"}}); err == nil {
		t.Fatal("expected reload error")
	}
	if !r.Covers("rg-1.sean.realgo.com") {
		t.Error("old cert must be retained after a failed reload")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestCertResolver -v`
Expected: FAIL — `undefined: NewCertResolver`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/tls.go`

```go
package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/realgo/rgdevenv/internal/config"
)

// CertResolver holds the supplied certificate(s), selects one by SNI (§7), and
// supports validate-before-swap reload.
type CertResolver struct {
	mu    sync.RWMutex
	certs []tls.Certificate
}

// NewCertResolver loads and parses the given cert/key pairs (primary first).
func NewCertResolver(pairs []config.CertPair) (*CertResolver, error) {
	certs, err := loadPairs(pairs)
	if err != nil {
		return nil, err
	}
	return &CertResolver{certs: certs}, nil
}

func loadPairs(pairs []config.CertPair) ([]tls.Certificate, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("proxy: no certificate pairs configured")
	}
	out := make([]tls.Certificate, 0, len(pairs))
	for _, p := range pairs {
		cert, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("proxy: load cert %s: %w", p.CertFile, err)
		}
		if cert.Leaf == nil && len(cert.Certificate) > 0 {
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err != nil {
				return nil, fmt.Errorf("proxy: parse leaf %s: %w", p.CertFile, err)
			}
			cert.Leaf = leaf
		}
		out = append(out, cert)
	}
	return out, nil
}

// GetCertificate is the tls.Config.GetCertificate callback. It prefers a cert
// matching the SNI name and falls back to the primary pair.
func (r *CertResolver) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.certs {
		if err := hello.SupportsCertificate(&r.certs[i]); err == nil {
			return &r.certs[i], nil
		}
	}
	if len(r.certs) > 0 {
		return &r.certs[0], nil
	}
	return nil, fmt.Errorf("proxy: no certificate available")
}

// Covers reports whether some loaded certificate is valid for the canonical host.
func (r *CertResolver) Covers(host string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.certs {
		if r.certs[i].Leaf != nil && r.certs[i].Leaf.VerifyHostname(host) == nil {
			return true
		}
	}
	return false
}

// Reload validates new pairs and swaps them in only on success (§7). On any
// error the previously loaded certs are retained; verification is never silently
// downgraded.
//
// AIDEV-NOTE: validate-before-swap — parse everything first, mutate only after
// all pairs load cleanly.
func (r *CertResolver) Reload(pairs []config.CertPair) error {
	certs, err := loadPairs(pairs)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.certs = certs
	r.mu.Unlock()
	return nil
}

// TLSConfig builds a server tls.Config backed by this resolver.
func (r *CertResolver) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: r.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run TestCertResolver -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/tls.go internal/proxy/tls_test.go
git commit -m "feat(proxy): SNI cert resolver with validate-before-swap reload

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 13: Reverse-proxy builder (`internal/proxy/reverseproxy.go`)

Build an `httputil.ReverseProxy` per upstream: shared safe dialer, the three
upstream-TLS modes, transport timeouts, and authoritative forwarding-header
rewrite (*§7, §8*). (The three TLS modes are asserted end-to-end in Task 19.)

**Files:**
- Create: `internal/proxy/reverseproxy.go`
- Test: `internal/proxy/reverseproxy_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/reverseproxy_test.go`

```go
package proxy

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func backendHostPort(t *testing.T, url string) (string, int) {
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

func TestReverseProxyOverwritesForwardedHeaders(t *testing.T) {
	var got http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, 5*time.Second)
	up := store.Upstream{Scheme: "http", Host: host, Port: port, TLS: store.UpstreamTLS{Mode: "verify"}}
	rp, err := BuildReverseProxy(up, true /*listenTLS*/, dialer, "", DefaultLimits(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	front := httptest.NewServer(rp)
	defer front.Close()
	req, _ := http.NewRequest("GET", front.URL, nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6") // spoof attempt
	req.Header.Set("X-Real-IP", "6.6.6.6")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if xff := got.Get("X-Forwarded-For"); xff == "6.6.6.6" || xff == "" {
		t.Fatalf("X-Forwarded-For not overwritten by rgdevenv: %q", xff)
	}
	if got.Get("X-Real-IP") == "6.6.6.6" {
		t.Fatal("X-Real-IP spoof not stripped")
	}
	if got.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("X-Forwarded-Proto = %q, want https", got.Get("X-Forwarded-Proto"))
	}
}

func TestReverseProxy502OnPolicyDenial(t *testing.T) {
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	up := store.Upstream{Scheme: "http", Host: "blocked.example.com", Port: 80, TLS: store.UpstreamTLS{Mode: "verify"}}
	rp, err := BuildReverseProxy(up, true, dialer, "", DefaultLimits(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(rp)
	defer front.Close()
	resp, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "blocked.example.com") {
		t.Fatal("502 body leaked upstream host")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestReverseProxy -v`
Expected: FAIL — `undefined: BuildReverseProxy`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/reverseproxy.go`

```go
package proxy

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// Limits holds tunable server/transport timeouts and bounds (§8).
type Limits struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int

	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxRequestBody    int64 // 0 = unlimited (dev default)
}

// DefaultLimits returns safe defaults.
func DefaultLimits() Limits {
	return Limits{
		DialTimeout:           10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ReadHeaderTimeout:     10 * time.Second,
		IdleTimeout:           120 * time.Second,
		MaxHeaderBytes:        1 << 20,
		MaxRequestBody:        0,
	}
}

// BuildReverseProxy builds an httputil.ReverseProxy for one upstream using the
// shared safe dialer and the upstream's TLS mode (§7, §8). listenTLS is the
// front-end mapping's TLS state (used for X-Forwarded-Proto).
func BuildReverseProxy(up store.Upstream, listenTLS bool, dialer *upstream.Dialer, caDir string, limits Limits, logger *slog.Logger) (*httputil.ReverseProxy, error) {
	if up.Scheme != "http" && up.Scheme != "https" {
		return nil, fmt.Errorf("proxy: invalid upstream scheme %q", up.Scheme)
	}
	target := &url.URL{Scheme: up.Scheme, Host: net.JoinHostPort(up.Host, strconv.Itoa(up.Port))}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          limits.MaxIdleConns,
		MaxIdleConnsPerHost:   limits.MaxIdleConnsPerHost,
		IdleConnTimeout:       limits.IdleConnTimeout,
		TLSHandshakeTimeout:   limits.TLSHandshakeTimeout,
		ResponseHeaderTimeout: limits.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if up.Scheme == "https" {
		tlsCfg, err := upstreamTLSConfig(up, caDir)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			setForwardedHeaders(pr, listenTLS)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// AIDEV-NOTE: full detail to logs; client gets a generic 502 (§8, §16).
			logger.Warn("upstream error", "host", r.Host, "upstream", target.String(), "error", err)
			writeBadGateway(w)
		},
	}
	return rp, nil
}

// setForwardedHeaders strips client-supplied forwarding/identity headers and sets
// rgdevenv's own (§8). With ReverseProxy.Rewrite these are NOT added
// automatically, so rgdevenv is the sole authority and spoofing is impossible.
func setForwardedHeaders(pr *httputil.ProxyRequest, listenTLS bool) {
	out, in := pr.Out, pr.In
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP", "Forwarded"} {
		out.Header.Del(h)
	}
	clientIP, _, _ := net.SplitHostPort(in.RemoteAddr)
	out.Header.Set("X-Forwarded-For", clientIP)
	out.Header.Set("X-Real-IP", clientIP)
	proto := "http"
	if listenTLS {
		proto = "https"
	}
	out.Header.Set("X-Forwarded-Proto", proto)
	out.Header.Set("X-Forwarded-Host", in.Host)
}

func upstreamTLSConfig(up store.Upstream, caDir string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: up.Host}
	switch up.TLS.Mode {
	case "verify", "":
		// verify against system roots
	case "skip":
		cfg.InsecureSkipVerify = true // dev-only (§7)
	case "ca":
		pool, err := upstream.LoadCA(caDir, up.TLS.CAName)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool // trusts ONLY the named private CA
	default:
		return nil, fmt.Errorf("proxy: unknown upstream tls mode %q", up.TLS.Mode)
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run TestReverseProxy -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/reverseproxy.go internal/proxy/reverseproxy_test.go
git commit -m "feat(proxy): per-upstream reverse proxy with TLS modes and header rewrite

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 14: Routing table (`internal/proxy/router.go`)

Build an immutable `(listen_port, canonical host) → route` table from a snapshot,
tracking **degraded** mappings (cert-not-covered, unbuildable upstream) that must
not serve (*§6, §7, §10*).

**Files:**
- Create: `internal/proxy/router.go`
- Test: `internal/proxy/router_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/router_test.go`

```go
package proxy

import (
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func testDeps(t *testing.T, certFile, keyFile, caDir string) RouteDeps {
	t.Helper()
	r, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	return RouteDeps{
		Dialer:   upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second),
		Resolver: r,
		CADir:    caDir,
		Limits:   DefaultLimits(),
		Logger:   discardLogger(),
	}
}

func TestBuildRoutingTableLiveRoute(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 0 {
		t.Fatalf("unexpected degraded: %+v", degraded)
	}
	if _, ok := tbl.Lookup(443, "rg-1.sean.realgo.com"); !ok {
		t.Fatal("expected route for rg-1 on 443")
	}
	if !tbl.HasHost("rg-1.sean.realgo.com") {
		t.Fatal("HasHost should be true")
	}
}

func TestBuildRoutingTableDegradedOnCertNotCovered(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t, "*.other.com")
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com", // not covered by *.other.com
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded, got %+v", degraded)
	}
	if _, ok := tbl.Lookup(443, "rg-1.sean.realgo.com"); ok {
		t.Fatal("degraded mapping must not be routable")
	}
}

func TestBuildRoutingTableDegradedOnMissingCA(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, t.TempDir()) // empty ca dir
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "https", Host: "build-box", Port: 8443, TLS: store.UpstreamTLS{Mode: "ca", CAName: "missing"}}}},
	}}}
	_, degraded := BuildRoutingTable(st, deps)
	if len(degraded) != 1 {
		t.Fatalf("expected 1 degraded for missing CA, got %+v", degraded)
	}
}

func TestDesiredListeners(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 8443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	tbl, _ := BuildRoutingTable(st, deps)
	desired := tbl.DesiredListeners(443, 80)
	if !desired[443] {
		t.Fatal("expected https 443 TLS listener")
	}
	if v, ok := desired[80]; !ok || v {
		t.Fatal("expected http 80 plaintext redirect listener")
	}
	if v, ok := desired[8443]; !ok || !v {
		t.Fatalf("expected 8443 TLS listener: %+v", desired)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run 'TestBuildRoutingTable|TestDesiredListeners' -v`
Expected: FAIL — `undefined: BuildRoutingTable`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/router.go`

```go
package proxy

import (
	"log/slog"
	"net/http"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// route is a resolved mapping ready to serve.
type route struct {
	lbName  string
	mapping store.Mapping
	handler http.Handler
}

// Degraded records a mapping that is configured but not served, with a reason
// (cert-not-covered, unbuildable upstream, listener bind failure) (§7, §10).
type Degraded struct {
	LB         string
	ListenPort int
	Reason     string
}

// RoutingTable maps (listen_port, canonical host) -> route. It is immutable once
// built and swapped in atomically (§16).
type RoutingTable struct {
	routes map[int]map[string]*route
	hosts  map[string]bool // hosts with >=1 live route (for the :80 redirect)
}

// RouteDeps are the dependencies for building a routing table.
type RouteDeps struct {
	Dialer   *upstream.Dialer
	Resolver *CertResolver
	CADir    string
	Limits   Limits
	Logger   *slog.Logger
}

// BuildRoutingTable builds routes for all servable mappings and returns the
// degraded ones. A mapping is degraded if its host is invalid, a TLS mapping's
// host is not certificate-covered (§7), or its reverse proxy cannot be built
// (bad scheme/mode/CA).
//
// AIDEV-NOTE: upstream POLICY (allowlist/deny) is enforced at dial time by the
// safe dialer, not here — build does no DNS, so degraded reasons are
// deterministic and offline.
func BuildRoutingTable(st *store.State, deps RouteDeps) (*RoutingTable, []Degraded) {
	t := &RoutingTable{routes: make(map[int]map[string]*route), hosts: make(map[string]bool)}
	var degraded []Degraded

	for _, lb := range st.LoadBalancers {
		name, err := canon.Host(lb.Name)
		if err != nil {
			for _, m := range lb.Mappings {
				degraded = append(degraded, Degraded{lb.Name, m.ListenPort, "invalid hostname: " + err.Error()})
			}
			continue
		}
		for _, m := range lb.Mappings {
			if m.ListenTLS && !deps.Resolver.Covers(name) {
				degraded = append(degraded, Degraded{name, m.ListenPort, "host not covered by certificate"})
				continue
			}
			h, err := BuildReverseProxy(m.Upstream, m.ListenTLS, deps.Dialer, deps.CADir, deps.Limits, deps.Logger)
			if err != nil {
				degraded = append(degraded, Degraded{name, m.ListenPort, "upstream not servable: " + err.Error()})
				continue
			}
			if t.routes[m.ListenPort] == nil {
				t.routes[m.ListenPort] = make(map[string]*route)
			}
			t.routes[m.ListenPort][name] = &route{lbName: name, mapping: m, handler: h}
			t.hosts[name] = true
		}
	}
	return t, degraded
}

// Lookup finds the route for (port, canonical host).
func (t *RoutingTable) Lookup(port int, host string) (*route, bool) {
	byHost, ok := t.routes[port]
	if !ok {
		return nil, false
	}
	r, ok := byHost[host]
	return r, ok
}

// HasHost reports whether any live route serves this host (used by the :80 redirect).
func (t *RoutingTable) HasHost(host string) bool { return t.hosts[host] }

// DesiredListeners returns the ports the table needs and whether each is TLS,
// including always-on https (TLS) and http (plaintext redirect, when >0).
func (t *RoutingTable) DesiredListeners(httpsPort, httpPort int) map[int]bool {
	desired := make(map[int]bool)
	for port, byHost := range t.routes {
		for _, r := range byHost {
			desired[port] = r.mapping.ListenTLS
			break
		}
	}
	desired[httpsPort] = true
	if httpPort > 0 {
		desired[httpPort] = false
	}
	return desired
}

// WithoutPorts returns a copy of the table with the given ports removed (used
// when a listener fails to bind).
func (t *RoutingTable) WithoutPorts(ports map[int]bool) *RoutingTable {
	nt := &RoutingTable{routes: make(map[int]map[string]*route), hosts: make(map[string]bool)}
	for port, byHost := range t.routes {
		if ports[port] {
			continue
		}
		nt.routes[port] = byHost
		for host := range byHost {
			nt.hosts[host] = true
		}
	}
	return nt
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run 'TestBuildRoutingTable|TestDesiredListeners' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/router.go internal/proxy/router_test.go
git commit -m "feat(proxy): immutable routing table with degraded tracking

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 15: HTTP→HTTPS redirect (`internal/proxy/redirect.go`)

The `:80` handler issues a `308` to the canonical HTTPS URL **only** for known,
certificate-covered hosts; everything else gets a generic `404` (no open
redirect, no Host echo) (*§6*).

**Files:**
- Create: `internal/proxy/redirect.go`
- Test: `internal/proxy/redirect_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/redirect_test.go`

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func rg1State() *store.State {
	return &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
}

func TestRedirectKnownHost(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(rg1State(), deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 443)

	req := httptest.NewRequest("GET", "http://rg-1.sean.realgo.com/foo?x=1", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://rg-1.sean.realgo.com/foo?x=1" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestRedirectUnknownHostNoOpenRedirect(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(&store.State{}, deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 443)

	req := httptest.NewRequest("GET", "http://evil.example.com/", nil)
	req.Host = "evil.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (no open redirect)", w.Code)
	}
	if w.Header().Get("Location") != "" {
		t.Fatal("must not emit a Location for an unknown host")
	}
}

func TestRedirectNonDefaultHTTPSPort(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	deps := testDeps(t, certFile, keyFile, "")
	tbl, _ := BuildRoutingTable(rg1State(), deps)
	h := newRedirectHandler(func() *RoutingTable { return tbl }, deps.Resolver, 8443)

	req := httptest.NewRequest("GET", "http://rg-1.sean.realgo.com/", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if loc := w.Header().Get("Location"); loc != "https://rg-1.sean.realgo.com:8443/" {
		t.Fatalf("Location = %q, want port 8443", loc)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestRedirect -v`
Expected: FAIL — `undefined: newRedirectHandler`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/redirect.go`

```go
package proxy

import (
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/canon"
)

// newRedirectHandler returns the :80 handler. It issues a 308 to the canonical
// https URL ONLY for known, certificate-covered hosts; anything else gets a
// generic 404 (§6).
//
// AIDEV-NOTE: never echo an arbitrary Host into Location (open-redirect guard).
// Both certificate coverage AND a live route are required.
func newRedirectHandler(current func() *RoutingTable, resolver *CertResolver, httpsPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := canon.Host(r.Host)
		if err != nil || !resolver.Covers(host) || !current().HasHost(host) {
			writeNotFound(w)
			return
		}
		target := "https://" + host
		if httpsPort != 443 {
			target += ":" + strconv.Itoa(httpsPort)
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run TestRedirect -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/redirect.go internal/proxy/redirect_test.go
git commit -m "feat(proxy): safe :80 -> :443 redirect (no open redirect)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 16: Listener manager (`internal/proxy/listeners.go`)

Owns the set of per-port `http.Server`s: open on demand, close when unreferenced,
report bind failures as non-fatal (*§6, §10*). Always-on ports are never closed.

**Files:**
- Create: `internal/proxy/listeners.go`
- Test: `internal/proxy/listeners_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/listeners_test.go`

```go
package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestListenersOpenAndClose(t *testing.T) {
	port := freePort(t)
	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{})
	mk := func(p int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	}
	if failed := m.Reconcile(map[int]bool{port: false}, mk); len(failed) != 0 {
		t.Fatalf("unexpected bind failures: %v", failed)
	}
	defer m.Shutdown(context.Background())

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	// Reconcile to empty -> the (non-always-on) port closes.
	m.Reconcile(map[int]bool{}, mk)
	if len(m.ActivePorts()) != 0 {
		t.Fatalf("expected no active ports, got %v", m.ActivePorts())
	}
}

func TestListenersBindFailureReported(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{})
	failed := m.Reconcile(map[int]bool{port: false}, func(p int) http.Handler { return http.NotFoundHandler() })
	if _, ok := failed[port]; !ok {
		t.Fatalf("expected bind failure for occupied port %d", port)
	}
}

func TestListenersAlwaysOnNotClosed(t *testing.T) {
	port := freePort(t)
	m := NewListeners("127.0.0.1", nil, DefaultLimits(), discardLogger(), map[int]bool{port: false})
	mk := func(p int) http.Handler { return http.NotFoundHandler() }
	m.Reconcile(map[int]bool{port: false}, mk)
	defer m.Shutdown(context.Background())
	// Reconcile to empty must NOT close an always-on port.
	m.Reconcile(map[int]bool{}, mk)
	if len(m.ActivePorts()) != 1 {
		t.Fatalf("always-on port must stay open, active = %v", m.ActivePorts())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestListeners -v`
Expected: FAIL — `undefined: NewListeners`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/listeners.go`

```go
package proxy

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type listenerEntry struct {
	port int
	tls  bool
	srv  *http.Server
	ln   net.Listener
}

// Listeners manages the set of per-port HTTP servers. Always-on ports (https,
// http) are protected from closure by Reconcile.
type Listeners struct {
	bindAddr  string
	tlsConfig *tls.Config
	limits    Limits
	logger    *slog.Logger
	alwaysOn  map[int]bool

	mu     sync.Mutex
	active map[int]*listenerEntry
}

// NewListeners builds the manager. alwaysOn ports are never closed by Reconcile.
func NewListeners(bindAddr string, tlsConfig *tls.Config, limits Limits, logger *slog.Logger, alwaysOn map[int]bool) *Listeners {
	return &Listeners{
		bindAddr:  bindAddr,
		tlsConfig: tlsConfig,
		limits:    limits,
		logger:    logger,
		alwaysOn:  alwaysOn,
		active:    make(map[int]*listenerEntry),
	}
}

// Reconcile opens listeners for ports in desired that are not active and closes
// active non-always-on ports not in desired. desired maps port -> isTLS. Ports
// that fail to bind are returned so the caller can degrade their mappings (§10).
//
// AIDEV-NOTE: bind failures are non-fatal (§10) — the daemon starts anyway.
func (m *Listeners) Reconcile(desired map[int]bool, makeHandler func(port int) http.Handler) map[int]error {
	m.mu.Lock()
	defer m.mu.Unlock()

	failed := make(map[int]error)
	for port, isTLS := range desired {
		if _, ok := m.active[port]; ok {
			continue
		}
		entry, err := m.open(port, isTLS, makeHandler(port))
		if err != nil {
			m.logger.Error("listener bind failed", "port", port, "error", err)
			failed[port] = err
			continue
		}
		m.active[port] = entry
	}
	for port, entry := range m.active {
		if m.alwaysOn[port] || desired[port] {
			continue
		}
		m.shutdown(entry)
		delete(m.active, port)
	}
	return failed
}

func (m *Listeners) open(port int, isTLS bool, h http.Handler) (*listenerEntry, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(m.bindAddr, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	if isTLS {
		ln = tls.NewListener(ln, m.tlsConfig)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: m.limits.ReadHeaderTimeout,
		IdleTimeout:       m.limits.IdleTimeout,
		MaxHeaderBytes:    m.limits.MaxHeaderBytes,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			m.logger.Error("listener stopped", "port", port, "error", err)
		}
	}()
	return &listenerEntry{port: port, tls: isTLS, srv: srv, ln: ln}, nil
}

func (m *Listeners) shutdown(e *listenerEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.srv.Shutdown(ctx)
}

// Shutdown gracefully stops all listeners.
func (m *Listeners) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for port, e := range m.active {
		if err := e.srv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.active, port)
	}
	return firstErr
}

// ActivePorts returns the currently bound ports.
func (m *Listeners) ActivePorts() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	ports := make([]int, 0, len(m.active))
	for p := range m.active {
		ports = append(ports, p)
	}
	return ports
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run TestListeners -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/listeners.go internal/proxy/listeners_test.go
git commit -m "feat(proxy): per-port listener manager with non-fatal bind failures

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 17: Server — Apply + dispatch (`internal/proxy/server.go`)

Ties routing + listeners together. `Apply` rebuilds the table, reconciles
listeners, demotes mappings on ports that failed to bind, and publishes the table
atomically (*§16*). `dispatch` routes by `(port, canonical Host)` and leaves a
Phase-2 seam for the management host.

**Files:**
- Create: `internal/proxy/server.go`
- Test: `internal/proxy/server_test.go`

- [ ] **Step 1: Write the failing test** — `internal/proxy/server_test.go`

```go
package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newTestServer(t *testing.T, certFile, keyFile, mgmtHost string) *Server {
	t.Helper()
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{BindAddr: "127.0.0.1", HTTPSPort: 443, HTTPPort: 80, MgmtHost: mgmtHost, DialTimeout: time.Second}
	return NewServer(cfg, upstream.NewPolicy(nil), resolver, DefaultLimits(), discardLogger())
}

func TestDispatchUnknownHost404(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	req := httptest.NewRequest("GET", "https://nope.sean.realgo.com/", nil)
	req.Host = "nope.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", w.Code)
	}
}

func TestDispatchManagementHost404InPhase1(t *testing.T) {
	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	req := httptest.NewRequest("GET", "https://rgdevenv.sean.realgo.com/", nil)
	req.Host = "rgdevenv.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("management host should 404 in phase 1, got %d", w.Code)
	}
}

func TestDispatchRoutesToUpstream(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "from-upstream")
	}))
	defer backend.Close()
	host, port := backendHostPort(t, backend.URL)

	certFile, keyFile := writeWildcardCert(t)
	s := newTestServer(t, certFile, keyFile, "rgdevenv.sean.realgo.com")
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: host, Port: port, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	dialer := upstream.New(upstream.NewPolicy(nil), upstream.SelfGuard{}, time.Second)
	tbl, degraded := BuildRoutingTable(st, RouteDeps{Dialer: dialer, Resolver: s.resolver, Limits: DefaultLimits(), Logger: discardLogger()})
	if len(degraded) != 0 {
		t.Fatalf("degraded: %+v", degraded)
	}
	s.routes.Store(tbl)

	req := httptest.NewRequest("GET", "https://rg-1.sean.realgo.com/", nil)
	req.Host = "rg-1.sean.realgo.com"
	w := httptest.NewRecorder()
	s.dispatch(443).ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "from-upstream" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/proxy/ -run TestDispatch -v`
Expected: FAIL — `undefined: NewServer`.

- [ ] **Step 3: Write the implementation** — `internal/proxy/server.go`

```go
package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// ServerConfig is the static configuration the Server needs.
type ServerConfig struct {
	BindAddr    string
	HTTPSPort   int
	HTTPPort    int
	CADir       string
	MgmtHost    string // canonical management hostname (reserved; 404 in Phase 1)
	DialTimeout time.Duration
}

// Server owns the routing table, listeners, and per-port dispatch.
type Server struct {
	cfg      ServerConfig
	policy   *upstream.Policy
	resolver *CertResolver
	limits   Limits
	logger   *slog.Logger
	localIPs []net.IP

	routes    atomic.Pointer[RoutingTable]
	listeners *Listeners
}

// NewServer constructs a Server; the HTTPS/on-demand TLS listeners use
// resolver.TLSConfig().
func NewServer(cfg ServerConfig, policy *upstream.Policy, resolver *CertResolver, limits Limits, logger *slog.Logger) *Server {
	alwaysOn := map[int]bool{cfg.HTTPSPort: true}
	if cfg.HTTPPort > 0 {
		alwaysOn[cfg.HTTPPort] = true
	}
	s := &Server{
		cfg:       cfg,
		policy:    policy,
		resolver:  resolver,
		limits:    limits,
		logger:    logger,
		localIPs:  localInterfaceIPs(),
		listeners: NewListeners(cfg.BindAddr, resolver.TLSConfig(), limits, logger, alwaysOn),
	}
	s.routes.Store(&RoutingTable{routes: map[int]map[string]*route{}, hosts: map[string]bool{}})
	return s
}

// Apply rebuilds routing + listeners from st and publishes atomically. Returns
// degraded mappings. Used at startup and (Phase 2) after each committed
// transaction.
//
// AIDEV-NOTE: the dialer self-guard uses ALL declared listen ports (a superset),
// independent of degraded status, so loop protection is stable across rebuilds.
func (s *Server) Apply(st *store.State) []Degraded {
	selfPorts := allListenPorts(st, s.cfg.HTTPSPort, s.cfg.HTTPPort)
	dialer := upstream.New(s.policy, upstream.SelfGuard{LocalIPs: s.localIPs, ListenPorts: selfPorts}, s.cfg.DialTimeout)

	table, degraded := BuildRoutingTable(st, RouteDeps{
		Dialer:   dialer,
		Resolver: s.resolver,
		CADir:    s.cfg.CADir,
		Limits:   s.limits,
		Logger:   s.logger,
	})

	desired := table.DesiredListeners(s.cfg.HTTPSPort, s.cfg.HTTPPort)
	failed := s.listeners.Reconcile(desired, s.makeHandler)
	if len(failed) > 0 {
		fp := make(map[int]bool, len(failed))
		for p := range failed {
			fp[p] = true
		}
		table = table.WithoutPorts(fp)
		degraded = append(degraded, listenerFailures(st, failed)...)
	}
	s.routes.Store(table)
	return degraded
}

func (s *Server) makeHandler(port int) http.Handler {
	if port == s.cfg.HTTPPort {
		return newRedirectHandler(s.routes.Load, s.resolver, s.cfg.HTTPSPort)
	}
	return s.dispatch(port)
}

func (s *Server) dispatch(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := canon.Host(r.Host)
		if err != nil {
			writeNotFound(w)
			return
		}
		// AIDEV-TODO(phase2): when host == s.cfg.MgmtHost, serve the management
		// plane here (auth + REST API + web UI). For now it 404s like any host.
		if s.cfg.MgmtHost != "" && host == s.cfg.MgmtHost {
			writeNotFound(w)
			return
		}
		rte, ok := s.routes.Load().Lookup(port, host)
		if !ok {
			writeNotFound(w)
			return
		}
		if s.limits.MaxRequestBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.limits.MaxRequestBody)
		}
		rte.handler.ServeHTTP(w, r)
	})
}

// Shutdown gracefully stops all listeners.
func (s *Server) Shutdown(ctx context.Context) error { return s.listeners.Shutdown(ctx) }

// Resolver exposes the cert resolver (for SIGHUP reload wiring).
func (s *Server) Resolver() *CertResolver { return s.resolver }

func allListenPorts(st *store.State, httpsPort, httpPort int) map[int]bool {
	ports := map[int]bool{httpsPort: true}
	if httpPort > 0 {
		ports[httpPort] = true
	}
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			ports[m.ListenPort] = true
		}
	}
	return ports
}

func listenerFailures(st *store.State, failed map[int]error) []Degraded {
	var out []Degraded
	for _, lb := range st.LoadBalancers {
		for _, m := range lb.Mappings {
			if err, ok := failed[m.ListenPort]; ok {
				out = append(out, Degraded{LB: lb.Name, ListenPort: m.ListenPort, Reason: "listener bind failed: " + err.Error()})
			}
		}
	}
	return out
}

func localInterfaceIPs() []net.IP {
	var ips []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -v -race`
Expected: PASS (all proxy tests).

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/server.go internal/proxy/server_test.go
git commit -m "feat(proxy): server Apply (build/reconcile/publish) and dispatch

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 18: `serve` wiring (`cmd/rgdevenv/serve.go`)

Wire it together: load config, open + validate + reconcile the store, build the
cert resolver/policy/server, `Apply` the snapshot, then handle `SIGHUP`
(validate-before-swap reload) and `SIGTERM`/`SIGINT` (graceful shutdown)
(*§7, §10, §16, §20*).

**Files:**
- Replace: `cmd/rgdevenv/serve.go` (the Task 1 stub)
- Test: `cmd/rgdevenv/serve_test.go`

- [ ] **Step 1: Write the failing test** — `cmd/rgdevenv/serve_test.go`

```go
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s never came up", addr)
}

func writeMainTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "*.sean.realgo.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"*.sean.realgo.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestSetupServerWiring(t *testing.T) {
	certFile, keyFile := writeMainTestCert(t)
	httpsPort := freeTCPPort(t)
	cfg := &config.Config{
		BindAddr:           "127.0.0.1",
		HTTPSPort:          httpsPort,
		HTTPPort:           0, // disable :80 in this test
		CertFile:           certFile,
		KeyFile:            keyFile,
		ManagementHostname: "rgdevenv.sean.realgo.com",
		StateFile:          filepath.Join(t.TempDir(), "state.json"),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, st, err := setupServer(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv.Apply(st.Snapshot())
	defer srv.Shutdown(context.Background())

	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "rgdevenv.sean.realgo.com"},
	}}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d/", httpsPort), nil)
	req.Host = "rgdevenv.sean.realgo.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// TLS handshake proves the cert loaded + listener bound; mgmt host 404s in phase 1.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/rgdevenv/ -run TestSetupServerWiring -v`
Expected: FAIL — `undefined: setupServer`.

- [ ] **Step 3: Replace the stub** — `cmd/rgdevenv/serve.go` (full file)

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/proxy"
	"github.com/realgo/rgdevenv/internal/registry"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func newServeCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the rgdevenv proxy daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "/etc/rgdevenv/config.toml", "path to config file")
	return cmd
}

func runServe(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, levelVar := newLogger(cfg.Log.Level)

	srv, st, err := setupServer(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	for _, d := range srv.Apply(st.Snapshot()) {
		logger.Warn("mapping degraded", "lb", d.LB, "listen_port", d.ListenPort, "reason", d.Reason)
	}
	logger.Info("rgdevenv listening", "https_port", cfg.HTTPSPort, "http_port", cfg.HTTPPort, "bind", cfg.BindAddr)

	return runSignals(configPath, srv, st, logger, levelVar)
}

// setupServer opens + validates + reconciles the store and builds the proxy
// server. It does NOT bind sockets (call srv.Apply for that), so it is testable.
func setupServer(cfg *config.Config, logger *slog.Logger) (*proxy.Server, *store.Store, error) {
	st, err := store.Open(cfg.StateFile)
	if err != nil {
		return nil, nil, err
	}
	snap := st.Snapshot()
	if err := store.Validate(snap); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("state invalid: %w", err)
	}
	if allocs, changed := registry.Reconcile(snap); changed {
		snap.PortAllocations = allocs
		if err := st.Save(snap); err != nil {
			st.Close()
			return nil, nil, fmt.Errorf("persist reconciled state: %w", err)
		}
		st.Publish(snap)
		logger.Info("reconciled orphaned port allocations")
	}

	resolver, err := proxy.NewCertResolver(cfg.AllCertPairs())
	if err != nil {
		st.Close()
		return nil, nil, err
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
	return srv, st, nil
}

// runSignals blocks until SIGTERM/SIGINT, handling SIGHUP reloads in between.
func runSignals(configPath string, srv *proxy.Server, st *store.Store, logger *slog.Logger, levelVar *slog.LevelVar) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	for sig := range sigs {
		switch sig {
		case syscall.SIGHUP:
			// Runtime-safe reload only: log level, certs, CA file contents.
			// Ports/bind changes require a restart (§9).
			newCfg, err := config.Load(configPath)
			if err != nil {
				logger.Error("config reload failed; keeping previous config", "error", err)
				break
			}
			levelVar.Set(parseLevel(newCfg.Log.Level))
			// AIDEV-NOTE: validate-before-swap (§7) — on failure the old certs
			// are retained and verification is never downgraded.
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
			defer cancel()
			return srv.Shutdown(ctx)
		}
	}
	return nil
}

func newLogger(level string) (*slog.Logger, *slog.LevelVar) {
	lv := new(slog.LevelVar)
	lv.Set(parseLevel(level))
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})), lv
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/rgdevenv/ -v -race`
Expected: PASS.

- [ ] **Step 5: Manual smoke test (optional but recommended)**

```bash
# Generate a throwaway wildcard cert and a minimal config + state, then curl through it.
# (Use high ports so no privileges are needed.)
go run ./cmd/rgdevenv serve --config /tmp/rgdevenv-smoke/config.toml
# In another shell: curl -k --resolve rg-1.sean.realgo.com:8443:127.0.0.1 \
#   https://rg-1.sean.realgo.com:8443/   (expects 404 until you add a mapping)
```

- [ ] **Step 6: Commit**

```bash
git add cmd/rgdevenv/serve.go cmd/rgdevenv/serve_test.go
git commit -m "feat(serve): wire config/store/proxy with SIGHUP reload and graceful shutdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 19: End-to-end proxy integration (`internal/proxy/integration_test.go`)

Exercises the full `Server` on real ephemeral sockets against real backends,
covering the spec §19 matrix: HTTP routing + generic `404`, the three upstream
TLS modes (`ca`/`skip` succeed, `verify` against self-signed → generic `502` with
no leak), WebSocket upgrade, self-proxy-loop refusal, and the `:80` `308`
redirect. (Forwarding-header overwrite and open-redirect 404 are unit-tested in
Tasks 13 and 15.)

**Files:**
- Create: `internal/proxy/integration_test.go`

- [ ] **Step 1: Write the integration test** — `internal/proxy/integration_test.go`

```go
package proxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/config"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s never came up", addr)
}

// rgClient returns an HTTP client that always connects to 127.0.0.1:httpsPort but
// keeps the request URL's Host/SNI, so dispatch routes by Host. Our self-signed
// front cert is skipped (we are testing the proxy, not cert trust).
func rgClient(httpsPort int) *http.Client {
	d := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func startProxyOn(t *testing.T, st *store.State, allow []string, caDir string, httpsPort, httpPort int) func() {
	t.Helper()
	certFile, keyFile := writeWildcardCert(t)
	resolver, err := NewCertResolver([]config.CertPair{{CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{
		BindAddr: "127.0.0.1", HTTPSPort: httpsPort, HTTPPort: httpPort,
		CADir: caDir, MgmtHost: "rgdevenv.sean.realgo.com", DialTimeout: 3 * time.Second,
	}
	srv := NewServer(cfg, upstream.NewPolicy(allow), resolver, DefaultLimits(), discardLogger())
	srv.Apply(st)
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if httpPort > 0 {
		waitListening(t, fmt.Sprintf("127.0.0.1:%d", httpPort))
	}
	return func() { srv.Shutdown(context.Background()) }
}

// newTLSBackend starts an HTTPS backend (cert for 127.0.0.1/localhost signed by a
// fresh CA) and returns its host, port, the CA PEM, and a stop func.
func newTLSBackend(t *testing.T, body string) (host string, port int, caPEM []byte, stop func()) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "integration ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(101), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})}
	go srv.Serve(tlsLn)
	h, ps, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(ps)
	return h, p, caPEM, func() { srv.Close() }
}

func TestIntegrationHTTPRouting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello")
	}))
	defer backend.Close()
	bh, bp := backendHostPort(t, backend.URL)

	httpsPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	client := rgClient(httpsPort)

	resp, err := client.Get("https://rg-1.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("known host: code=%d body=%q", resp.StatusCode, body)
	}

	resp2, err := client.Get("https://nope.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("unknown host: code=%d, want 404", resp2.StatusCode)
	}
}

func TestIntegrationUpstreamTLSModes(t *testing.T) {
	bh, bp, caPEM, stop := newTLSBackend(t, "secure-ok")
	defer stop()

	caDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(caDir, "corp.pem"), caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	httpsPort := freePort(t)
	mk := func(name, mode, caName string) store.LoadBalancer {
		return store.LoadBalancer{Name: name, Mappings: []store.Mapping{{
			ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "https", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: mode, CAName: caName}},
		}}}
	}
	st := &store.State{LoadBalancers: []store.LoadBalancer{
		mk("rg-ca.sean.realgo.com", "ca", "corp"),
		mk("rg-skip.sean.realgo.com", "skip", ""),
		mk("rg-verify.sean.realgo.com", "verify", ""),
	}}
	defer startProxyOn(t, st, nil, caDir, httpsPort, 0)()
	client := rgClient(httpsPort)

	for _, host := range []string{"rg-ca.sean.realgo.com", "rg-skip.sean.realgo.com"} {
		resp, err := client.Get("https://" + host + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "secure-ok" {
			t.Fatalf("%s: code=%d body=%q (want 200/secure-ok)", host, resp.StatusCode, body)
		}
	}

	// verify mode: self-signed CA not in system roots -> generic 502, no leak.
	resp, err := client.Get("https://rg-verify.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("verify mode against self-signed: code=%d, want 502", resp.StatusCode)
	}
	if strings.Contains(string(body), bh) || strings.Contains(strings.ToLower(string(body)), "certificate") {
		t.Fatalf("502 body leaked detail: %q", body)
	}
}

func TestIntegrationWebSocketUpgrade(t *testing.T) {
	// Backend that completes a minimal upgrade and echoes one line.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
		line, _ := buf.ReadString('\n')
		buf.WriteString("echo:" + line)
		buf.Flush()
	})}
	go backend.Serve(ln)
	defer backend.Close()
	bh, ps, _ := net.SplitHostPort(ln.Addr().String())
	bp, _ := strconv.Atoi(ps)

	httpsPort := freePort(t) // always-on TLS port (unused here)
	wsPort := freePort(t)    // plaintext on-demand listener
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-ws.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: wsPort, ListenTLS: false,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	waitListening(t, fmt.Sprintf("127.0.0.1:%d", wsPort))

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", wsPort))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: rg-ws.sean.realgo.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("expected 101, got %q (err=%v)", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	fmt.Fprintf(conn, "ping\n")
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(echo) != "echo:ping" {
		t.Fatalf("websocket echo = %q, want %q", strings.TrimSpace(echo), "echo:ping")
	}
}

func TestIntegrationSelfLoopRefused(t *testing.T) {
	httpsPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-loop.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			// Upstream points at OUR OWN https listener -> loop -> denied.
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: httpsPort, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, 0)()
	client := rgClient(httpsPort)

	resp, err := client.Get("https://rg-loop.sean.realgo.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("self-loop must be refused with 502, got %d", resp.StatusCode)
	}
}

func TestIntegration80Redirect(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()
	bh, bp := backendHostPort(t, backend.URL)

	httpsPort := freePort(t)
	httpPort := freePort(t)
	st := &store.State{LoadBalancers: []store.LoadBalancer{{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{ListenPort: httpsPort, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: bh, Port: bp, TLS: store.UpstreamTLS{Mode: "verify"}}}},
	}}}
	defer startProxyOn(t, st, nil, "", httpsPort, httpPort)()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Known host -> 308 to canonical https URL on the real https port.
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/path?q=1", httpPort), nil)
	req.Host = "rg-1.sean.realgo.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", resp.StatusCode)
	}
	want := fmt.Sprintf("https://rg-1.sean.realgo.com:%d/path?q=1", httpsPort)
	if got := resp.Header.Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}

	// Unknown host -> 404, no Location.
	req2, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", httpPort), nil)
	req2.Host = "evil.example.com"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 404 || resp2.Header.Get("Location") != "" {
		t.Fatalf("unknown host on :80 = %d loc=%q, want 404/no-loc", resp2.StatusCode, resp2.Header.Get("Location"))
	}
}
```

- [ ] **Step 2: Run the integration tests**

Run: `go test ./internal/proxy/ -run TestIntegration -v -race`
Expected: PASS (all integration subtests).

- [ ] **Step 3: Run the entire suite**

Run: `go test ./... -race && go vet ./...`
Expected: all packages PASS, vet clean.

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/integration_test.go
git commit -m "test(proxy): end-to-end integration matrix (routing, TLS modes, ws, loop, redirect)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Definition of done (Phase 1)

- [ ] `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass.
- [ ] `rgdevenv serve --config <file>` starts, binds `:443`/`:80` (or configured
  ports), and proxies a hand-written `state.json` to its upstreams.
- [ ] SSRF/loop protection verified: link-local/metadata/self-loop denied;
  validated-IP pinning defeats rebinding; deny-if-any holds (Tasks 8, 10).
- [ ] The three upstream TLS modes behave correctly; `verify` against an
  untrusted cert yields a generic `502` (Task 19).
- [ ] `:80` issues safe `308`s only for known, cert-covered hosts; everything
  else is a generic `404` (Tasks 15, 19).
- [ ] Atomic+durable persistence, instance lock, and startup reconcile work
  (Tasks 5, 7).

## What Phase 2 adds (not in this plan)

`internal/auth` (bearer + rate limit), `internal/txn` (staged build → validate →
pre-bind → persist → publish → cleanup, reusing `Server.Apply`), `internal/api`
(REST CRUD), `internal/health` (protocol-aware checks via the shared safe
dialer), `internal/ui` (static login shell + dashboard), `internal/client` +
`lb`/`map`/`port`/`ca`/`status` CLI subcommands, and serving the management plane
at the `MgmtHost` seam in `proxy.Server.dispatch` (plus the optional
loopback/unix management bind). Phase 2 gets its own spec-derived plan.











