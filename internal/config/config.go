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
	"time"

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
	Health     HealthConfig     `toml:"health"`
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

type HealthConfig struct {
	Enabled         bool   `toml:"enabled"`
	IntervalSeconds int    `toml:"interval_seconds"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
	Path            string `toml:"path"` // "" → TCP-connect probe; else HTTP(S) GET of this path
	Threshold       int    `toml:"threshold"`
}

// Default returns the built-in defaults (§9).
func Default() *Config {
	return &Config{
		HTTPSPort:  443,
		HTTPPort:   80,
		BindAddr:   "0.0.0.0",
		CADir:      "/etc/rgdevenv/cas",
		TokenFile:  "/etc/rgdevenv/token",
		StateFile:  "/var/lib/rgdevenv/state.json",
		Management: ManagementConfig{AuthRateLimitPerMin: 10},
		PortPool:   PortPoolConfig{Start: 9000, End: 9999},
		Log:        LogConfig{Level: "info", Access: true},
		Health:     HealthConfig{Enabled: true, IntervalSeconds: 15, TimeoutSeconds: 5, Path: "/", Threshold: 2},
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
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(c *Config) error {
	if v := os.Getenv("RGDEVENV_HTTPS_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: RGDEVENV_HTTPS_PORT %q: %w", v, err)
		}
		c.HTTPSPort = n
	}
	if v := os.Getenv("RGDEVENV_HTTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: RGDEVENV_HTTP_PORT %q: %w", v, err)
		}
		c.HTTPPort = n
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
	return nil
}

func (c *Config) normalize() error {
	if c.HTTPSPort < 1 || c.HTTPSPort > 65535 {
		return fmt.Errorf("config: https_port %d out of range", c.HTTPSPort)
	}
	// http_port may be 0 to disable the HTTP->HTTPS redirect listener (§6);
	// https_port must always be a real port.
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
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}

// MgmtBindPort returns the TCP port of management.bind, or 0 for none/unix.
func (c *Config) MgmtBindPort() int {
	p, ok := c.mgmtBindPort()
	if !ok {
		return 0
	}
	return p
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

// HealthInterval is the probe interval as a duration.
func (c *Config) HealthInterval() time.Duration {
	return time.Duration(c.Health.IntervalSeconds) * time.Second
}

// HealthTimeout is the per-probe timeout as a duration.
func (c *Config) HealthTimeout() time.Duration {
	return time.Duration(c.Health.TimeoutSeconds) * time.Second
}
