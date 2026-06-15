package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 8443 {
		t.Fatalf("file precedence: got %d", cfg.HTTPSPort)
	}
	if cfg.ManagementHostname != "mgmt.example.com" {
		t.Fatalf("mgmt host not canonicalized: %q", cfg.ManagementHostname)
	}

	// AIDEV-NOTE: 9443 would be inside the default port_pool [9000,9999]; use
	// 19443 to test env precedence without triggering pool-overlap validation.
	t.Setenv("RGDEVENV_HTTPS_PORT", "19443")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPSPort != 19443 {
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

func TestNormalizeAcceptsUnixMgmtBind(t *testing.T) {
	path := writeTOML(t, "[management]\nbind = \"/run/rgdevenv/mgmt.sock\"\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("unix socket mgmt bind should be allowed: %v", err)
	}
}

func TestLoadRejectsBadEnvPort(t *testing.T) {
	t.Setenv("RGDEVENV_HTTPS_PORT", "notanumber")
	if _, err := Load(""); err == nil {
		t.Fatal("expected error for non-integer RGDEVENV_HTTPS_PORT")
	}
}

func TestNormalizeRejectsMgmtBindPortInPool(t *testing.T) {
	path := writeTOML(t, "[management]\nbind = \"127.0.0.1:9500\"\n[port_pool]\nstart = 9000\nend = 9999\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error: management bind port inside pool")
	}
}

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
	cases := map[string]struct {
		mut  func(*Config)
		want string // expected substring of the normalize error
	}{
		"interval":  {func(c *Config) { c.Health.IntervalSeconds = 0 }, "health.interval_seconds"},
		"timeout":   {func(c *Config) { c.Health.TimeoutSeconds = -1 }, "health.timeout_seconds"},
		"threshold": {func(c *Config) { c.Health.Threshold = 0 }, "health.threshold"},
	}
	for name, tc := range cases {
		c := Default() // Enabled: true, so the numeric checks fire
		tc.mut(c)
		err := c.normalize()
		if err == nil {
			t.Fatalf("%s: expected normalize error", name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q does not contain %q", name, err, tc.want)
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
