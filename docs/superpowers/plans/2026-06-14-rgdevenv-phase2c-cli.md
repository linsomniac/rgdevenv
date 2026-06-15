# rgdevenv Phase 2c — CLI Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `rgdevenv` CLI subcommands (`lb`, `map`, `port`, `ca`, `status`) — thin clients over the Phase 2a/2b REST API — backed by a reusable `internal/client` package, with human-readable tables by default and `--json` for scripting.

**Architecture:** A new `internal/client` package wraps the REST API: it loads CLI config (`RGDEVENV_API`/`RGDEVENV_TOKEN`/`~/.config/rgdevenv/cli.toml`), builds a bearer-authenticated `*http.Client`, and exposes one typed method per endpoint that decodes the `{error,code}` body into a typed `APIError`. The `cmd/rgdevenv` cobra tree gains sibling subcommands to `serve`, each constructing a client from merged config+flags, calling one method, and rendering the result as a table or (with `--json`) the raw JSON. No new third-party dependencies.

**Tech Stack:** Go 1.22 stdlib (`net/http`, `encoding/json`, `net/url`, `text/tabwriter`), `spf13/cobra` (already a dependency), `BurntSushi/toml` (already a dependency).

**Spec sections:** §12 (REST API — the contract the client consumes), §13 (CLI), §19 (testing: "CLI: subcommands against an in-process test API; human and `--json` output").

> **Commit convention:** every commit message must additionally end with the trailer
> `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
> (omitted from the per-task `-m` examples for brevity). Work on a branch, not `master`.

---

## Design decisions (review these first)

1. **Client-owned DTOs.** `internal/client` defines its own response structs (`LoadBalancer`, `Mapping`, `Upstream`, `PortPool`, `Allocation`, `Status`, `UpstreamHealth`) mirroring the API's JSON wire shape, rather than importing `internal/store`/`internal/api`/`internal/health`. The wire contract is the boundary; this keeps the client decoupled from server internals and usable as a standalone package. Field tags must match the API exactly (verified against Phase 2b handlers).
2. **Config precedence: flags > env > file > defaults.** `client.Load()` reads `~/.config/rgdevenv/cli.toml` then overlays `RGDEVENV_API`/`RGDEVENV_TOKEN`/`RGDEVENV_INSECURE`; the cobra layer overlays explicit `--api`/`--token`/`--insecure` flags. `api` and `token` are required (clear error if missing). `cli.toml` is read best-effort (missing file is fine); if present it should be `0600` (we warn, not fail, on looser perms — out of scope to hard-enforce).
3. **`--insecure` for dev.** rgdevenv serves a supplied (often private-CA) wildcard cert; a developer may not have installed that CA. A global `--insecure` flag (and `insecure` config key / `RGDEVENV_INSECURE=1`) sets `InsecureSkipVerify` for the CLI's HTTPS calls. Default is secure (verify against system roots). This only affects the CLI→management-plane connection, never the data plane.
4. **`--upstream URL` parsing.** `http://host:port` / `https://host:port`; scheme must be `http` or `https`; host required; port required (an explicit port avoids ambiguity, matching every spec example). A missing/extra path, query, or userinfo is rejected. Returns `(scheme, host, port)`.
5. **`map set` is a PUT replace (full mapping).** Per §12 `PUT` replaces a mapping; `map set` requires `--listen-port` and sends the full new mapping body (with the body's `listen_port` equal to the path port). `--no-tls` toggles `listen_tls=false`; default `listen_tls=true`. For `--allocate`, omit `upstream` and set `allocate=true`.
6. **Output: table by default, `--json` raw.** `--json` prints the exact decoded response re-encoded as indented JSON (stable, scriptable). Tables use `text/tabwriter`. Mutations that return 204 print a short confirmation line (or nothing in `--json` mode beyond an empty success — we print `{}`); commands that return a body print it.
7. **No `serve` changes.** `serve` is untouched; the new subcommands are siblings added to the cobra root in `main.go`. Health (Phase 2b) is already surfaced by the API; the CLI just displays the `health` field where present (e.g., `map ls`, `status`).

---

## File structure

**New files**
```
internal/client/config.go        Config + Load (file → env; flags overlaid by caller)
internal/client/config_test.go
internal/client/client.go         Client, New, do(), APIError
internal/client/client_test.go
internal/client/types.go          wire DTOs (LoadBalancer/Mapping/Upstream/PortPool/Allocation/Status/...)
internal/client/lbs.go            ListLBs/CreateLB/GetLB/SetLBLabel/DeleteLB
internal/client/lbs_test.go
internal/client/mappings.go       PutMapping/DeleteMapping + MappingRequest
internal/client/mappings_test.go
internal/client/ports.go          ListPorts/AllocatePort/ReturnPort
internal/client/misc.go           ListCAs/Status
internal/client/ports_misc_test.go
internal/client/upstream.go       ParseUpstreamURL
internal/client/upstream_test.go
cmd/rgdevenv/cli.go               global CLI flags, newClient(), output helpers (renderJSON/table)
cmd/rgdevenv/cli_test.go
cmd/rgdevenv/lb.go                lb add/set/rm/ls
cmd/rgdevenv/map.go               map add/set/rm/ls
cmd/rgdevenv/port.go              port get/return/ls
cmd/rgdevenv/status.go            ca ls; status
cmd/rgdevenv/cli_integration_test.go   subcommands against an in-process API (§19)
```

**Modified files**
```
cmd/rgdevenv/main.go              register lb/map/port/ca/status commands + persistent flags
```

---

## Task 1: CLI client configuration

**Files:**
- Create: `internal/client/config.go`
- Test: `internal/client/config_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/client/config_test.go`:

```go
package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.toml")
	if err := os.WriteFile(path, []byte("api = \"https://rgdevenv.example.com\"\ntoken = \"tok-from-file\"\ninsecure = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// env empty for this test
	t.Setenv("RGDEVENV_API", "")
	t.Setenv("RGDEVENV_TOKEN", "")
	t.Setenv("RGDEVENV_INSECURE", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API != "https://rgdevenv.example.com" || cfg.Token != "tok-from-file" || !cfg.Insecure {
		t.Fatalf("file config wrong: %+v", cfg)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.toml")
	_ = os.WriteFile(path, []byte("api = \"https://file\"\ntoken = \"file-tok\"\n"), 0o600)
	t.Setenv("RGDEVENV_API", "https://env")
	t.Setenv("RGDEVENV_TOKEN", "env-tok")
	t.Setenv("RGDEVENV_INSECURE", "1")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API != "https://env" || cfg.Token != "env-tok" || !cfg.Insecure {
		t.Fatalf("env should override file: %+v", cfg)
	}
}

func TestLoadMissingFileIsOK(t *testing.T) {
	t.Setenv("RGDEVENV_API", "https://x")
	t.Setenv("RGDEVENV_TOKEN", "y")
	t.Setenv("RGDEVENV_INSECURE", "")
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if cfg.API != "https://x" || cfg.Token != "y" || cfg.Insecure {
		t.Fatalf("env-only config wrong: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run TestLoad -v`
Expected: FAIL — no Go files / undefined `Load`.

- [ ] **Step 3: Implement the config loader**

`internal/client/config.go`:

```go
// Package client is a thin REST client for the rgdevenv management API, used by
// the CLI subcommands (§12, §13).
package client

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the CLI client configuration (§13). Precedence is flags > env > file;
// flags are overlaid by the cobra layer after Load.
type Config struct {
	API      string `toml:"api"`      // base URL, e.g. https://rgdevenv.example.com (or http://127.0.0.1:8443)
	Token    string `toml:"token"`    // bearer token
	Insecure bool   `toml:"insecure"` // skip TLS verification (dev; private CA not installed)
}

// Load reads cli.toml (if present) then overlays RGDEVENV_* environment variables.
// A missing file is not an error. It does NOT enforce that api/token are set —
// the caller validates after overlaying flags (see Config.Validate).
func Load(path string) (Config, error) {
	var cfg Config
	if path != "" {
		if _, err := toml.DecodeFile(path, &cfg); err != nil && !os.IsNotExist(err) {
			return cfg, fmt.Errorf("client: parse %s: %w", path, err)
		}
	}
	if v := strings.TrimSpace(os.Getenv("RGDEVENV_API")); v != "" {
		cfg.API = v
	}
	if v := strings.TrimSpace(os.Getenv("RGDEVENV_TOKEN")); v != "" {
		cfg.Token = v
	}
	if v := strings.TrimSpace(os.Getenv("RGDEVENV_INSECURE")); v != "" {
		// any non-empty value other than 0/false enables insecure
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Insecure = b
		} else {
			cfg.Insecure = true
		}
	}
	return cfg, nil
}

// Validate ensures the required fields are present.
func (c Config) Validate() error {
	if strings.TrimSpace(c.API) == "" {
		return fmt.Errorf("client: no API endpoint (set --api, RGDEVENV_API, or api in cli.toml)")
	}
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("client: no token (set --token, RGDEVENV_TOKEN, or token in cli.toml)")
	}
	return nil
}

// DefaultConfigPath returns ~/.config/rgdevenv/cli.toml ("" if HOME is unknown).
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir + "/rgdevenv/cli.toml"
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/config.go internal/client/config_test.go
git commit -m "feat(client): CLI config loader (file + env)"
```

---

## Task 2: Client core (transport, bearer, error decoding)

**Files:**
- Create: `internal/client/client.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/client/client_test.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds a Client pointed at srv with a fixed token.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{API: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDoSendsBearerAndDecodes(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	var out struct{ Hello string }
	if err := c.do(context.Background(), http.MethodGet, "/api/v1/thing", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if out.Hello != "world" {
		t.Fatalf("decoded = %+v", out)
	}
}

func TestDoMapsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "already exists", "code": "duplicate_lb"})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	err := c.do(context.Background(), http.MethodPost, "/api/v1/lbs", map[string]string{"name": "x"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if ae.Status != http.StatusConflict || ae.Code != "duplicate_lb" || !strings.Contains(ae.Message, "already exists") {
		t.Fatalf("APIError wrong: %+v", ae)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := New(Config{API: "://bad", Token: "t"}); err == nil {
		t.Fatal("expected error for bad base URL")
	}
}
```

(The test uses `errors.As(err, &ae)` from the stdlib — `"errors"` is imported above.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run 'TestDo|TestNew' -v`
Expected: FAIL — undefined `New`, `Client.do`, `APIError`.

- [ ] **Step 3: Implement the client core**

`internal/client/client.go`:

```go
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIError is a non-2xx response decoded from the API's {error,code} body (§12).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s, http %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("%s (http %d)", e.Message, e.Status)
}

// Client calls the rgdevenv management REST API with a bearer token.
type Client struct {
	base  string // base URL without trailing slash
	token string
	hc    *http.Client
}

// New builds a Client. The base URL must be absolute (http/https). Insecure skips
// TLS verification (dev only).
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.API))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("client: invalid API base URL %q", cfg.API)
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if cfg.Insecure {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // dev only
	}
	return &Client{base: strings.TrimRight(cfg.API, "/"), token: cfg.Token, hc: hc}, nil
}

// do performs an authenticated request. body (if non-nil) is JSON-encoded; out
// (if non-nil) is JSON-decoded from a 2xx response. A non-2xx response is mapped
// to *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("client: decode response: %w", err)
	}
	return nil
}

func decodeAPIError(resp *http.Response) error {
	ae := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &body) == nil && body.Error != "" {
		ae.Message = body.Error
		ae.Code = body.Code
	}
	return ae
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/client.go internal/client/client_test.go
git commit -m "feat(client): core transport with bearer auth + APIError decoding"
```

---

## Task 3: Wire DTOs + load-balancer methods

**Files:**
- Create: `internal/client/types.go`, `internal/client/lbs.go`
- Test: `internal/client/lbs_test.go`

- [ ] **Step 1: Write the failing test**

`internal/client/lbs_test.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLBMethods(t *testing.T) {
	var lastMethod, lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/lbs":
			_ = json.NewEncoder(w).Encode([]LoadBalancer{{Name: "a.example.com", Mappings: []Mapping{}}})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(LoadBalancer{Name: "a.example.com", Label: "demo"})
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	lbs, err := c.ListLBs(ctx)
	if err != nil || len(lbs) != 1 || lbs[0].Name != "a.example.com" {
		t.Fatalf("list: %v %+v", err, lbs)
	}
	lb, err := c.CreateLB(ctx, "a.example.com", "demo")
	if err != nil || lb.Label != "demo" {
		t.Fatalf("create: %v %+v", err, lb)
	}
	if lastMethod != http.MethodPost || lastPath != "/api/v1/lbs" || lastBody == "" {
		t.Fatalf("create request wrong: %s %s %s", lastMethod, lastPath, lastBody)
	}
	if _, err := c.SetLBLabel(ctx, "a.example.com", "renamed"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if lastMethod != http.MethodPatch {
		t.Fatalf("set method = %s", lastMethod)
	}
	if err := c.DeleteLB(ctx, "a.example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if lastMethod != http.MethodDelete || lastPath != "/api/v1/lbs/a.example.com" {
		t.Fatalf("delete request wrong: %s %s", lastMethod, lastPath)
	}
}
```

Add `"io"` to the test imports.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run TestLBMethods -v`
Expected: FAIL — undefined `LoadBalancer`, `Mapping`, `ListLBs`, etc.

- [ ] **Step 3: Implement DTOs and LB methods**

`internal/client/types.go` (JSON tags MUST match the Phase 2b API exactly):

```go
package client

import "time"

type LoadBalancer struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Mappings  []Mapping `json:"mappings"`
}

type Mapping struct {
	ListenPort    int      `json:"listen_port"`
	ListenTLS     bool     `json:"listen_tls"`
	Upstream      Upstream `json:"upstream"`
	AllocationID  string   `json:"allocation_id,omitempty"`
	AutoAllocated bool     `json:"auto_allocated,omitempty"`
	Health        string   `json:"health,omitempty"`
}

type Upstream struct {
	Scheme string      `json:"scheme"`
	Host   string      `json:"host"`
	Port   int         `json:"port"`
	TLS    UpstreamTLS `json:"tls"`
}

type UpstreamTLS struct {
	Mode   string `json:"mode"`
	CAName string `json:"ca_name,omitempty"`
}

type PortPool struct {
	Start       int          `json:"start"`
	End         int          `json:"end"`
	Used        int          `json:"used"`
	Free        int          `json:"free"`
	Allocations []Allocation `json:"allocations"`
}

type Allocation struct {
	ID          string    `json:"id"`
	Port        int       `json:"port"`
	Owner       string    `json:"owner,omitempty"`
	Label       string    `json:"label,omitempty"`
	Auto        bool      `json:"auto,omitempty"`
	AllocatedAt time.Time `json:"allocated_at"`
}

type AllocateResult struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

type Status struct {
	Version         string           `json:"version"`
	HTTPSPort       int              `json:"https_port"`
	HTTPPort        int              `json:"http_port"`
	ActiveListeners []int            `json:"active_listeners"`
	LoadBalancers   int              `json:"load_balancers"`
	Mappings        int              `json:"mappings"`
	Allocations     int              `json:"allocations"`
	Upstreams       []UpstreamHealth `json:"upstreams"`
}

type UpstreamHealth struct {
	Scheme  string `json:"scheme"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSMode string `json:"tls_mode,omitempty"`
	Health  string `json:"health"`
}
```

`internal/client/lbs.go`:

```go
package client

import (
	"context"
	"net/http"
)

func (c *Client) ListLBs(ctx context.Context) ([]LoadBalancer, error) {
	var out []LoadBalancer
	if err := c.do(ctx, http.MethodGet, "/api/v1/lbs", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) CreateLB(ctx context.Context, name, label string) (LoadBalancer, error) {
	var out LoadBalancer
	body := map[string]string{"name": name, "label": label}
	err := c.do(ctx, http.MethodPost, "/api/v1/lbs", body, &out)
	return out, err
}

func (c *Client) GetLB(ctx context.Context, name string) (LoadBalancer, error) {
	var out LoadBalancer
	err := c.do(ctx, http.MethodGet, "/api/v1/lbs/"+name, nil, &out)
	return out, err
}

func (c *Client) SetLBLabel(ctx context.Context, name, label string) (LoadBalancer, error) {
	var out LoadBalancer
	body := map[string]string{"label": label}
	err := c.do(ctx, http.MethodPatch, "/api/v1/lbs/"+name, body, &out)
	return out, err
}

func (c *Client) DeleteLB(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/lbs/"+name, nil, nil)
}
```

AIDEV-NOTE on `types.go` (add it above `LoadBalancer`): these mirror the Phase 2b API wire shape (internal/api/views.go, ports.go, misc.go); keep the json tags in sync if the API changes.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/types.go internal/client/lbs.go internal/client/lbs_test.go
git commit -m "feat(client): wire DTOs + load-balancer methods"
```

---

## Task 4: Mapping methods

**Files:**
- Create: `internal/client/mappings.go`
- Test: `internal/client/mappings_test.go`

- [ ] **Step 1: Write the failing test**

`internal/client/mappings_test.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMappingMethods(t *testing.T) {
	var lastMethod, lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Mapping{ListenPort: 443, ListenTLS: true})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	// Create with explicit upstream.
	port := 443
	tls := true
	req := MappingRequest{
		ListenPort: &port, ListenTLS: &tls,
		Upstream: &UpstreamRequest{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLSRequest{Mode: "verify"}},
	}
	if _, err := c.PutMapping(ctx, "a.example.com", req, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if lastMethod != http.MethodPost || lastPath != "/api/v1/lbs/a.example.com/mappings" {
		t.Fatalf("create request wrong: %s %s", lastMethod, lastPath)
	}
	if !strings.Contains(lastBody, `"host":"localhost"`) {
		t.Fatalf("create body missing upstream: %s", lastBody)
	}

	// Replace (PUT to /{port}).
	if _, err := c.PutMapping(ctx, "a.example.com", req, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if lastMethod != http.MethodPut || lastPath != "/api/v1/lbs/a.example.com/mappings/443" {
		t.Fatalf("replace request wrong: %s %s", lastMethod, lastPath)
	}

	// Delete.
	if err := c.DeleteMapping(ctx, "a.example.com", 443); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if lastMethod != http.MethodDelete || lastPath != "/api/v1/lbs/a.example.com/mappings/443" {
		t.Fatalf("delete request wrong: %s %s", lastMethod, lastPath)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run TestMappingMethods -v`
Expected: FAIL — undefined `MappingRequest`, `PutMapping`, etc.

- [ ] **Step 3: Implement mapping methods**

`internal/client/mappings.go` (request DTOs mirror the API's `mappingReq`: `listen_port`/`listen_tls` are pointers so omitted fields take server defaults):

```go
package client

import (
	"context"
	"fmt"
	"net/http"
)

// MappingRequest is the create/replace body (§12). ListenPort/ListenTLS are
// pointers so an omitted field uses the server default (port 443, tls true).
type MappingRequest struct {
	ListenPort *int             `json:"listen_port,omitempty"`
	ListenTLS  *bool            `json:"listen_tls,omitempty"`
	Upstream   *UpstreamRequest `json:"upstream,omitempty"`
	Allocate   bool             `json:"allocate,omitempty"`
	Label      string           `json:"label,omitempty"`
}

type UpstreamRequest struct {
	Scheme string             `json:"scheme"`
	Host   string             `json:"host"`
	Port   int                `json:"port"`
	TLS    UpstreamTLSRequest `json:"tls"`
}

type UpstreamTLSRequest struct {
	Mode   string `json:"mode,omitempty"`
	CAName string `json:"ca_name,omitempty"`
}

// PutMapping creates (replace=false → POST) or replaces (replace=true → PUT
// /{listen_port}) a mapping. When replace is true the request must carry a
// ListenPort (used for the path and validated by the server against the body).
func (c *Client) PutMapping(ctx context.Context, lb string, req MappingRequest, replace bool) (Mapping, error) {
	var out Mapping
	if !replace {
		err := c.do(ctx, http.MethodPost, "/api/v1/lbs/"+lb+"/mappings", req, &out)
		return out, err
	}
	if req.ListenPort == nil {
		return out, fmt.Errorf("client: replace requires a listen port")
	}
	path := fmt.Sprintf("/api/v1/lbs/%s/mappings/%d", lb, *req.ListenPort)
	err := c.do(ctx, http.MethodPut, path, req, &out)
	return out, err
}

func (c *Client) DeleteMapping(ctx context.Context, lb string, port int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/lbs/%s/mappings/%d", lb, port), nil, nil)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/mappings.go internal/client/mappings_test.go
git commit -m "feat(client): mapping create/replace/delete methods"
```

---

## Task 5: Ports, CAs, and status methods

**Files:**
- Create: `internal/client/ports.go`, `internal/client/misc.go`
- Test: `internal/client/ports_misc_test.go`

- [ ] **Step 1: Write the failing test**

`internal/client/ports_misc_test.go`:

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPortsAndMiscMethods(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/ports" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(PortPool{Start: 9000, End: 9999, Used: 1, Free: 999, Allocations: []Allocation{{ID: "a1", Port: 9000}}})
		case r.URL.Path == "/api/v1/ports/allocate":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(AllocateResult{ID: "a2", Port: 9001})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/cas":
			_ = json.NewEncoder(w).Encode([]string{"corp", "partner"})
		case r.URL.Path == "/api/v1/status":
			_ = json.NewEncoder(w).Encode(Status{Version: "1.0", Upstreams: []UpstreamHealth{}})
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	if pp, err := c.ListPorts(ctx); err != nil || pp.Used != 1 || len(pp.Allocations) != 1 {
		t.Fatalf("ports: %v %+v", err, pp)
	}
	if a, err := c.AllocatePort(ctx, "owner", "label"); err != nil || a.Port != 9001 {
		t.Fatalf("allocate: %v %+v", err, a)
	}
	if err := c.ReturnPort(ctx, 9000); err != nil {
		t.Fatalf("return: %v", err)
	}
	if cas, err := c.ListCAs(ctx); err != nil || len(cas) != 2 || cas[0] != "corp" {
		t.Fatalf("cas: %v %+v", err, cas)
	}
	if s, err := c.Status(ctx); err != nil || s.Version != "1.0" {
		t.Fatalf("status: %v %+v", err, s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run TestPortsAndMisc -v`
Expected: FAIL — undefined `ListPorts`, `AllocatePort`, etc.

- [ ] **Step 3: Implement the methods**

`internal/client/ports.go`:

```go
package client

import (
	"context"
	"fmt"
	"net/http"
)

func (c *Client) ListPorts(ctx context.Context) (PortPool, error) {
	var out PortPool
	err := c.do(ctx, http.MethodGet, "/api/v1/ports", nil, &out)
	return out, err
}

func (c *Client) AllocatePort(ctx context.Context, owner, label string) (AllocateResult, error) {
	var out AllocateResult
	body := map[string]string{"owner": owner, "label": label}
	err := c.do(ctx, http.MethodPost, "/api/v1/ports/allocate", body, &out)
	return out, err
}

func (c *Client) ReturnPort(ctx context.Context, port int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/ports/%d", port), nil, nil)
}
```

`internal/client/misc.go`:

```go
package client

import (
	"context"
	"net/http"
)

func (c *Client) ListCAs(ctx context.Context) ([]string, error) {
	var out []string
	err := c.do(ctx, http.MethodGet, "/api/v1/cas", nil, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, &out)
	return out, err
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/ports.go internal/client/misc.go internal/client/ports_misc_test.go
git commit -m "feat(client): ports, cas, and status methods"
```

---

## Task 6: `--upstream` URL parsing

**Files:**
- Create: `internal/client/upstream.go`
- Test: `internal/client/upstream_test.go`

- [ ] **Step 1: Write the failing test**

`internal/client/upstream_test.go`:

```go
package client

import "testing"

func TestParseUpstreamURL(t *testing.T) {
	ok := []struct {
		in     string
		scheme string
		host   string
		port   int
	}{
		{"http://localhost:9011", "http", "localhost", 9011},
		{"https://build-box:8443", "https", "build-box", 8443},
		{"http://10.0.0.5:80", "http", "10.0.0.5", 80},
	}
	for _, tc := range ok {
		s, h, p, err := ParseUpstreamURL(tc.in)
		if err != nil || s != tc.scheme || h != tc.host || p != tc.port {
			t.Fatalf("%s → (%s,%s,%d,%v), want (%s,%s,%d)", tc.in, s, h, p, err, tc.scheme, tc.host, tc.port)
		}
	}
	bad := []string{
		"localhost:9011",          // no scheme
		"ftp://host:21",           // bad scheme
		"http://host",             // no port
		"http://:9011",            // no host
		"http://host:9011/path",   // path not allowed
		"http://user@host:9011",   // userinfo not allowed
		"http://host:notaport",    // bad port
		"",                        // empty
	}
	for _, in := range bad {
		if _, _, _, err := ParseUpstreamURL(in); err == nil {
			t.Fatalf("%q should be rejected", in)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/client/ -run TestParseUpstreamURL -v`
Expected: FAIL — undefined `ParseUpstreamURL`.

- [ ] **Step 3: Implement the parser**

`internal/client/upstream.go`:

```go
package client

import (
	"fmt"
	"net/url"
	"strconv"
)

// ParseUpstreamURL parses an --upstream value like "http://localhost:9011" or
// "https://build-box:8443" into scheme/host/port (§13). Scheme must be http or
// https; host and an explicit port are required; path/query/userinfo are rejected.
func ParseUpstreamURL(raw string) (scheme, host string, port int, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", 0, fmt.Errorf("client: invalid upstream URL %q: %w", raw, perr)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", 0, fmt.Errorf("client: upstream scheme must be http or https: %q", raw)
	}
	if u.User != nil {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain userinfo: %q", raw)
	}
	if p := u.EscapedPath(); p != "" && p != "/" {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain a path: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain a query or fragment: %q", raw)
	}
	h, portStr := u.Hostname(), u.Port()
	if h == "" {
		return "", "", 0, fmt.Errorf("client: upstream URL missing host: %q", raw)
	}
	if portStr == "" {
		return "", "", 0, fmt.Errorf("client: upstream URL must include an explicit port: %q", raw)
	}
	pn, cerr := strconv.Atoi(portStr)
	if cerr != nil || pn < 1 || pn > 65535 {
		return "", "", 0, fmt.Errorf("client: invalid upstream port in %q", raw)
	}
	return u.Scheme, h, pn, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/client/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/upstream.go internal/client/upstream_test.go
git commit -m "feat(client): --upstream URL parser"
```

---

## Task 7: CLI scaffolding — global flags, client factory, output helpers

**Files:**
- Create: `cmd/rgdevenv/cli.go`
- Modify: `cmd/rgdevenv/main.go`
- Test: `cmd/rgdevenv/cli_test.go`

- [ ] **Step 1: Write the failing test**

`cmd/rgdevenv/cli_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := renderJSON(&buf, map[string]any{"a": 1, "b": "x"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"a": 1`) || !strings.Contains(out, `"b": "x"`) {
		t.Fatalf("json output = %q", out)
	}
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, []string{"NAME", "PORT"}, [][]string{{"a", "443"}, {"bb", "8443"}})
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "8443") {
		t.Fatalf("table output = %q", out)
	}
	// header and both rows present
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n"); lines != 2 {
		t.Fatalf("want 3 lines (header+2 rows), got %d in %q", lines+1, out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/rgdevenv/ -run 'TestRender' -v`
Expected: FAIL — undefined `renderJSON`, `renderTable`.

- [ ] **Step 3: Implement scaffolding**

`cmd/rgdevenv/cli.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

// cliFlags holds the persistent CLI client flags (shared by all REST subcommands).
type cliFlags struct {
	configPath string
	api        string
	token      string
	insecure   bool
	json       bool
}

var cli cliFlags

// addClientFlags registers the persistent flags on the root command.
func addClientFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringVar(&cli.configPath, "cli-config", client.DefaultConfigPath(), "path to CLI config (cli.toml)")
	pf.StringVar(&cli.api, "api", "", "management API base URL (overrides config/env)")
	pf.StringVar(&cli.token, "token", "", "bearer token (overrides config/env)")
	pf.BoolVar(&cli.insecure, "insecure", false, "skip TLS verification (dev only)")
	pf.BoolVar(&cli.json, "json", false, "output JSON instead of a table")
}

// newClient builds a client from cli.toml + env, overlaid with explicit flags.
func newClient(cmd *cobra.Command) (*client.Client, error) {
	cfg, err := client.Load(cli.configPath)
	if err != nil {
		return nil, err
	}
	if cmd.Flags().Changed("api") {
		cfg.API = cli.api
	}
	if cmd.Flags().Changed("token") {
		cfg.Token = cli.token
	}
	if cmd.Flags().Changed("insecure") {
		cfg.Insecure = cli.insecure
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return client.New(cfg)
}

// renderJSON writes v as indented JSON.
func renderJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderTable writes a simple aligned table.
func renderTable(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	writeRow(tw, header)
	for _, r := range rows {
		writeRow(tw, r)
	}
	_ = tw.Flush()
}

func writeRow(w io.Writer, cols []string) {
	for i, c := range cols {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, c)
	}
	fmt.Fprintln(w)
}
```

In `cmd/rgdevenv/main.go`, register the flags and the new commands. Modify `main()`:

```go
func main() {
	root := &cobra.Command{
		Use:           "rgdevenv",
		Short:         "HTTPS reverse proxy for managing dev environments",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addClientFlags(root)
	root.AddCommand(newServeCmd())
	root.AddCommand(newLBCmd(), newMapCmd(), newPortCmd(), newCACmd(), newStatusCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rgdevenv: error:", err)
		os.Exit(1)
	}
}
```

(The `newLBCmd`/`newMapCmd`/`newPortCmd`/`newCACmd`/`newStatusCmd` constructors are added in Tasks 8–10. To keep the build green at THIS commit, add temporary stubs returning `&cobra.Command{Use: "lb"}` etc. in cli.go, OR implement Tasks 8–10 before re-running main. RECOMMENDED: add the five command files' constructors now as minimal stubs that compile, then flesh them out in Tasks 8–10. The simplest green path: implement this task's main.go change together with Task 8–10 stubs. If you prefer strict task isolation, register only the commands that exist so far.)

> AIDEV-NOTE: to keep each task's commit compiling, register a command only once its constructor exists. The cleanest approach: in this task, add `newLBCmd`/`newMapCmd`/`newPortCmd`/`newCACmd`/`newStatusCmd` as one-line stubs in their target files (each returning a bare `&cobra.Command{Use: "..."}`), then Tasks 8–10 replace the stubs with real implementations. Update the stubs' files in the matching task.

For THIS task, create the five stub constructors so main.go compiles:

`cmd/rgdevenv/lb.go` (stub), `cmd/rgdevenv/map.go` (stub), `cmd/rgdevenv/port.go` (stub), `cmd/rgdevenv/status.go` (stub — holds both `newCACmd` and `newStatusCmd`):

```go
// lb.go
package main
import "github.com/spf13/cobra"
func newLBCmd() *cobra.Command { return &cobra.Command{Use: "lb", Short: "Manage load balancers"} }
```
```go
// map.go
package main
import "github.com/spf13/cobra"
func newMapCmd() *cobra.Command { return &cobra.Command{Use: "map", Short: "Manage mappings"} }
```
```go
// port.go
package main
import "github.com/spf13/cobra"
func newPortCmd() *cobra.Command { return &cobra.Command{Use: "port", Short: "Manage port reservations"} }
```
```go
// status.go
package main
import "github.com/spf13/cobra"
func newCACmd() *cobra.Command     { return &cobra.Command{Use: "ca", Short: "Custom CAs"} }
func newStatusCmd() *cobra.Command { return &cobra.Command{Use: "status", Short: "Show server status"} }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go build ./... && go test ./cmd/rgdevenv/ -run TestRender -v`
Expected: PASS; binary builds with the (stub) subcommands registered.

- [ ] **Step 5: Commit**

```bash
git add cmd/rgdevenv/cli.go cmd/rgdevenv/main.go cmd/rgdevenv/lb.go cmd/rgdevenv/map.go cmd/rgdevenv/port.go cmd/rgdevenv/status.go cmd/rgdevenv/cli_test.go
git commit -m "feat(cli): scaffolding — flags, client factory, output helpers, command stubs"
```

---

## Task 8: `lb` subcommands

**Files:**
- Modify: `cmd/rgdevenv/lb.go` (replace the stub)
- Test: covered by the Task 11 integration test (no separate unit test; cobra wiring is exercised end-to-end). Optionally add a focused test if desired.

- [ ] **Step 1: Implement `lb add/set/rm/ls`**

Replace `cmd/rgdevenv/lb.go`:

```go
package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

func newLBCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "lb", Short: "Manage load balancers"}
	cmd.AddCommand(newLBAddCmd(), newLBSetCmd(), newLBRmCmd(), newLBLsCmd())
	return cmd
}

func newLBAddCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a load balancer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.CreateLB(cmd.Context(), args[0], label)
			if err != nil {
				return err
			}
			return printLB(cmd, lb)
		},
	}
	c.Flags().StringVar(&label, "label", "", "human-readable label")
	return c
}

func newLBSetCmd() *cobra.Command {
	var label string
	c := &cobra.Command{
		Use:   "set <name> --label TEXT",
		Short: "Update a load balancer's label",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.SetLBLabel(cmd.Context(), args[0], label)
			if err != nil {
				return err
			}
			return printLB(cmd, lb)
		},
	}
	c.Flags().StringVar(&label, "label", "", "new label")
	_ = c.MarkFlagRequired("label")
	return c
}

func newLBRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a load balancer (and its mappings)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.DeleteLB(cmd.Context(), args[0]); err != nil {
				return err
			}
			return printDeleted(cmd, "load balancer "+args[0])
		},
	}
}

func newLBLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List load balancers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lbs, err := cl.ListLBs(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), lbs)
			}
			sort.Slice(lbs, func(i, j int) bool { return lbs[i].Name < lbs[j].Name })
			rows := make([][]string, 0, len(lbs))
			for _, lb := range lbs {
				rows = append(rows, []string{lb.Name, lb.Label, fmt.Sprintf("%d", len(lb.Mappings))})
			}
			renderTable(cmd.OutOrStdout(), []string{"NAME", "LABEL", "MAPPINGS"}, rows)
			return nil
		},
	}
}

func printLB(cmd *cobra.Command, lb client.LoadBalancer) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), lb)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", lb.Name, lb.Label)
	return nil
}

func printDeleted(cmd *cobra.Command, what string) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), map[string]string{"deleted": what})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", what)
	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
git add cmd/rgdevenv/lb.go
git commit -m "feat(cli): lb add/set/rm/ls subcommands"
```

---

## Task 9: `map` subcommands

**Files:**
- Modify: `cmd/rgdevenv/map.go` (replace the stub)

- [ ] **Step 1: Implement `map add/set/rm/ls`**

Replace `cmd/rgdevenv/map.go`:

```go
package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/realgo/rgdevenv/internal/client"
)

func newMapCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "map", Short: "Manage mappings"}
	cmd.AddCommand(newMapAddCmd(), newMapSetCmd(), newMapRmCmd(), newMapLsCmd())
	return cmd
}

// mapFlags are shared by add and set.
type mapFlags struct {
	upstream    string
	listenPort  int
	noTLS       bool
	upstreamTLS string
	caName      string
	allocate    bool
	label       string
}

func (f mapFlags) request(cmd *cobra.Command) (client.MappingRequest, error) {
	port := f.listenPort
	tls := !f.noTLS
	req := client.MappingRequest{ListenPort: &port, ListenTLS: &tls, Allocate: f.allocate, Label: f.label}
	if f.allocate {
		if f.upstream != "" {
			return req, fmt.Errorf("--allocate and --upstream are mutually exclusive")
		}
		return req, nil
	}
	if f.upstream == "" {
		return req, fmt.Errorf("--upstream URL is required (or use --allocate)")
	}
	scheme, host, uport, err := client.ParseUpstreamURL(f.upstream)
	if err != nil {
		return req, err
	}
	mode := f.upstreamTLS
	if mode == "" {
		mode = "verify"
	}
	req.Upstream = &client.UpstreamRequest{
		Scheme: scheme, Host: host, Port: uport,
		TLS: client.UpstreamTLSRequest{Mode: mode, CAName: f.caName},
	}
	return req, nil
}

func addMapFlags(c *cobra.Command, f *mapFlags) {
	c.Flags().StringVar(&f.upstream, "upstream", "", "upstream URL, e.g. http://localhost:9011")
	c.Flags().IntVar(&f.listenPort, "listen-port", 443, "front-end listen port")
	c.Flags().BoolVar(&f.noTLS, "no-tls", false, "serve this listen port as plain HTTP")
	c.Flags().StringVar(&f.upstreamTLS, "upstream-tls", "", "upstream TLS mode: verify|skip|ca")
	c.Flags().StringVar(&f.caName, "ca-name", "", "custom CA name (for --upstream-tls ca)")
	c.Flags().StringVar(&f.label, "label", "", "label for an auto-allocated port")
}

func newMapAddCmd() *cobra.Command {
	var f mapFlags
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a mapping to a load balancer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.request(cmd)
			if err != nil {
				return err
			}
			m, err := cl.PutMapping(cmd.Context(), args[0], req, false)
			if err != nil {
				return err
			}
			return printMapping(cmd, m)
		},
	}
	addMapFlags(c, &f)
	c.Flags().BoolVar(&f.allocate, "allocate", false, "allocate a port and map :listen-port → http://localhost:<port>")
	return c
}

func newMapSetCmd() *cobra.Command {
	var f mapFlags
	c := &cobra.Command{
		Use:   "set <name> --listen-port N",
		Short: "Replace a mapping (by listen port)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			req, err := f.request(cmd)
			if err != nil {
				return err
			}
			m, err := cl.PutMapping(cmd.Context(), args[0], req, true)
			if err != nil {
				return err
			}
			return printMapping(cmd, m)
		},
	}
	addMapFlags(c, &f)
	c.Flags().BoolVar(&f.allocate, "allocate", false, "allocate a port for the replacement")
	_ = c.MarkFlagRequired("listen-port")
	return c
}

func newMapRmCmd() *cobra.Command {
	var listenPort int
	c := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a mapping (by listen port)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.DeleteMapping(cmd.Context(), args[0], listenPort); err != nil {
				return err
			}
			return printDeleted(cmd, fmt.Sprintf("mapping %s:%d", args[0], listenPort))
		},
	}
	c.Flags().IntVar(&listenPort, "listen-port", 443, "listen port of the mapping to remove")
	return c
}

func newMapLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <name>",
		Short: "List a load balancer's mappings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			lb, err := cl.GetLB(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), lb.Mappings)
			}
			ms := lb.Mappings
			sort.Slice(ms, func(i, j int) bool { return ms[i].ListenPort < ms[j].ListenPort })
			rows := make([][]string, 0, len(ms))
			for _, m := range ms {
				up := fmt.Sprintf("%s://%s:%d", m.Upstream.Scheme, m.Upstream.Host, m.Upstream.Port)
				rows = append(rows, []string{
					fmt.Sprintf("%d", m.ListenPort), fmt.Sprintf("%t", m.ListenTLS),
					up, m.Upstream.TLS.Mode, healthOrDash(m.Health),
				})
			}
			renderTable(cmd.OutOrStdout(), []string{"LISTEN", "TLS", "UPSTREAM", "UP-TLS", "HEALTH"}, rows)
			return nil
		},
	}
}

func printMapping(cmd *cobra.Command, m client.Mapping) error {
	if cli.json {
		return renderJSON(cmd.OutOrStdout(), m)
	}
	fmt.Fprintf(cmd.OutOrStdout(), ":%d → %s://%s:%d (%s)\n",
		m.ListenPort, m.Upstream.Scheme, m.Upstream.Host, m.Upstream.Port, healthOrDash(m.Health))
	return nil
}

func healthOrDash(h string) string {
	if h == "" {
		return "-"
	}
	return h
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
git add cmd/rgdevenv/map.go
git commit -m "feat(cli): map add/set/rm/ls subcommands"
```

---

## Task 10: `port`, `ca`, and `status` subcommands

**Files:**
- Modify: `cmd/rgdevenv/port.go` and `cmd/rgdevenv/status.go` (replace the stubs)

- [ ] **Step 1: Implement `port get/return/ls`**

Replace `cmd/rgdevenv/port.go`:

```go
package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
)

func newPortCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "port", Short: "Manage port reservations"}
	cmd.AddCommand(newPortGetCmd(), newPortReturnCmd(), newPortLsCmd())
	return cmd
}

func newPortGetCmd() *cobra.Command {
	var owner, label string
	c := &cobra.Command{
		Use:   "get",
		Short: "Allocate a port (prints id and port)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			a, err := cl.AllocatePort(cmd.Context(), owner, label)
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), a)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%d\n", a.ID, a.Port)
			return nil
		},
	}
	c.Flags().StringVar(&owner, "owner", "", "owner of the reservation")
	c.Flags().StringVar(&label, "label", "", "label for the reservation")
	return c
}

func newPortReturnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "return <port>",
		Short: "Return a reserved port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("port must be an integer: %q", args[0])
			}
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			if err := cl.ReturnPort(cmd.Context(), port); err != nil {
				return err
			}
			return printDeleted(cmd, fmt.Sprintf("port %d", port))
		},
	}
}

func newPortLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the port pool and allocations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			pp, err := cl.ListPorts(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), pp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pool %d-%d  used %d  free %d\n", pp.Start, pp.End, pp.Used, pp.Free)
			allocs := pp.Allocations
			sort.Slice(allocs, func(i, j int) bool { return allocs[i].Port < allocs[j].Port })
			rows := make([][]string, 0, len(allocs))
			for _, a := range allocs {
				rows = append(rows, []string{fmt.Sprintf("%d", a.Port), a.ID, a.Owner, a.Label, fmt.Sprintf("%t", a.Auto)})
			}
			renderTable(cmd.OutOrStdout(), []string{"PORT", "ID", "OWNER", "LABEL", "AUTO"}, rows)
			return nil
		},
	}
}
```

- [ ] **Step 2: Implement `ca ls` and `status`**

Replace `cmd/rgdevenv/status.go`:

```go
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newCACmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ca", Short: "Custom CAs"}
	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List available custom-CA names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			cas, err := cl.ListCAs(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), cas)
			}
			sort.Strings(cas)
			for _, name := range cas {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	})
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show server status and upstream health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient(cmd)
			if err != nil {
				return err
			}
			s, err := cl.Status(cmd.Context())
			if err != nil {
				return err
			}
			if cli.json {
				return renderJSON(cmd.OutOrStdout(), s)
			}
			out := cmd.OutOrStdout()
			ports := make([]string, 0, len(s.ActiveListeners))
			for _, p := range s.ActiveListeners {
				ports = append(ports, fmt.Sprintf("%d", p))
			}
			fmt.Fprintf(out, "version %s\n", s.Version)
			fmt.Fprintf(out, "listeners %s\n", strings.Join(ports, ","))
			fmt.Fprintf(out, "load_balancers %d  mappings %d  allocations %d\n", s.LoadBalancers, s.Mappings, s.Allocations)
			if len(s.Upstreams) > 0 {
				rows := make([][]string, 0, len(s.Upstreams))
				for _, u := range s.Upstreams {
					rows = append(rows, []string{fmt.Sprintf("%s://%s:%d", u.Scheme, u.Host, u.Port), u.TLSMode, u.Health})
				}
				renderTable(out, []string{"UPSTREAM", "TLS", "HEALTH"}, rows)
			}
			return nil
		},
	}
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: builds.

- [ ] **Step 4: Commit**

```bash
git add cmd/rgdevenv/port.go cmd/rgdevenv/status.go
git commit -m "feat(cli): port, ca, and status subcommands"
```

---

## Task 11: CLI integration test (subcommands against an in-process API)

**Files:**
- Create: `cmd/rgdevenv/cli_integration_test.go`

- [ ] **Step 1: Write the test**

This wires the real cobra commands against an in-process `api.Handler` over `httptest`, exercising the full CLI → client → API path for both table and `--json` output (§19).

`cmd/rgdevenv/cli_integration_test.go`:

```go
package main

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/api"
	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// inProcessAPI builds a real management Handler over a temp store and serves it.
func inProcessAPI(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := txn.New(st, func(*store.State) {}, func(string) bool { return true }, upstream.NewPolicy(nil),
		txn.Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"})
	h := api.New(api.Deps{
		Txn: mgr, Auth: auth.NewAuthenticator("0123456789abcdef0123456789abcdef"),
		Limiter: auth.NewRateLimiter(1000, time.Minute),
		CADir:   t.TempDir(), Version: "test", HTTPSPort: 443, HTTPPort: 80, PoolStart: 9000, PoolEnd: 9999,
		ActivePorts: func() []int { return []int{443} },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// run executes the root command with args and returns combined stdout.
func run(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	root := newTestRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	full := append([]string{
		"--api", srv.URL,
		"--token", "0123456789abcdef0123456789abcdef",
	}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

func TestCLIEndToEnd(t *testing.T) {
	srv := inProcessAPI(t)

	// Create an LB.
	if out, err := run(t, srv, "lb", "add", "rg-1.sean.realgo.com", "--label", "demo"); err != nil {
		t.Fatalf("lb add: %v (%s)", err, out)
	}
	// List shows it.
	out, err := run(t, srv, "lb", "ls")
	if err != nil || !strings.Contains(out, "rg-1.sean.realgo.com") || !strings.Contains(out, "demo") {
		t.Fatalf("lb ls: %v %q", err, out)
	}
	// Add an allocate mapping on :443.
	if out, err := run(t, srv, "map", "add", "rg-1.sean.realgo.com", "--allocate", "--label", "web"); err != nil {
		t.Fatalf("map add --allocate: %v (%s)", err, out)
	}
	// port ls shows one used port, JSON form.
	out, err = run(t, srv, "--json", "port", "ls")
	if err != nil || !strings.Contains(out, `"used": 1`) {
		t.Fatalf("port ls --json: %v %q", err, out)
	}
	// map ls shows the localhost upstream.
	out, err = run(t, srv, "map", "ls", "rg-1.sean.realgo.com")
	if err != nil || !strings.Contains(out, "localhost") {
		t.Fatalf("map ls: %v %q", err, out)
	}
	// status works.
	if out, err := run(t, srv, "status"); err != nil || !strings.Contains(out, "version test") {
		t.Fatalf("status: %v %q", err, out)
	}
	// A conflict surfaces as an error (duplicate LB).
	if _, err := run(t, srv, "lb", "add", "rg-1.sean.realgo.com"); err == nil {
		t.Fatal("duplicate lb add should error")
	}
	// Delete the LB.
	if _, err := run(t, srv, "lb", "rm", "rg-1.sean.realgo.com"); err != nil {
		t.Fatalf("lb rm: %v", err)
	}
}
```

- [ ] **Step 2: Add the `newTestRoot` helper**

The integration test needs a root command identical to production but constructible per-test (cobra flag state and the package-level `cli` var are global, so build a fresh root each run and reset `cli`). Add to `cli.go`:

```go
// newRoot builds the root command with all subcommands and client flags. main()
// and tests both use it so the wiring is identical.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "rgdevenv",
		Short:         "HTTPS reverse proxy for managing dev environments",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addClientFlags(root)
	root.AddCommand(newServeCmd())
	root.AddCommand(newLBCmd(), newMapCmd(), newPortCmd(), newCACmd(), newStatusCmd())
	return root
}
```

Refactor `main()` to use it:

```go
func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rgdevenv: error:", err)
		os.Exit(1)
	}
}
```

And add the test-only constructor in `cli_test.go` (resets the global flag state so runs don't leak into each other):

```go
func newTestRoot() *cobra.Command {
	cli = cliFlags{} // reset global flag state between runs
	return newRoot()
}
```

> AIDEV-NOTE: the CLI flag values live in the package-global `cli` (cobra binds flags to it). `newTestRoot` resets it per run so sequential in-test invocations don't inherit a previous `--json`/`--api`. Production runs the process once, so the global is fine there.

- [ ] **Step 3: Run to verify it passes**

Run: `go test ./cmd/rgdevenv/ -race -v -run TestCLIEndToEnd`
Expected: PASS. Then `go test ./... -race` for the whole module.

- [ ] **Step 4: Commit**

```bash
git add cmd/rgdevenv/cli_integration_test.go cmd/rgdevenv/cli.go cmd/rgdevenv/main.go
git commit -m "test(cli): end-to-end subcommands against an in-process API"
```

---

## Final verification

- [ ] **Whole-module gate**

Run:
```bash
go build ./... && go test ./... -race 2>&1 | grep -E '^(ok|FAIL)' && go vet ./... && (golangci-lint run >/dev/null 2>&1 && echo "lint 0 issues") && test -z "$(gofmt -l .)" && echo "ALL GREEN"
```
Expected: every package `ok`, `lint 0 issues`, `ALL GREEN`.

- [ ] **Manual smoke (optional, against a running daemon).** `RGDEVENV_API=https://rgdevenv.sean.realgo.com RGDEVENV_TOKEN=... ./rgdevenv lb ls` (add `--insecure` if the CA isn't installed locally).

---

## Spec coverage self-check

- §13 commands: `lb add/set/rm/ls` (Task 8), `map add/set/rm/ls` incl. `--upstream`/`--allocate`/`--no-tls`/`--upstream-tls`/`--ca-name` (Tasks 6, 9), `port get/return/ls` (Task 10), `ca ls` (Task 10), `status` (Task 10). ✅
- §13 client config `RGDEVENV_API`/`RGDEVENV_TOKEN`/`cli.toml` + precedence (Tasks 1, 7). ✅
- §13 `--json` vs table output (Tasks 7–10). ✅
- §13 `--upstream` URL parsing (Task 6). ✅
- §12 every endpoint has a client method (Tasks 3–5). ✅
- §19 CLI tested against an in-process API, human + `--json` (Task 11). ✅

---

## What Phase 2d adds (not in this plan)

- **Phase 2d — Web UI:** `internal/ui` embedded `html/template` + vanilla JS static login shell and dashboard mounted at `/`; consumes the same REST API (and Phase 2b's `health` field for the status dot). Gets its own plan.
