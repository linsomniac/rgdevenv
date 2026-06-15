// Package client is a thin REST client for the rgdevenv management API, used by
// the CLI subcommands (§12, §13).
package client

import (
	"fmt"
	"os"
	"path/filepath"
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
		// AIDEV-NOTE: any non-empty value other than 0/false enables insecure mode;
		// strconv.ParseBool handles "true"/"1"/"yes"/etc.; non-bool strings default to true.
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
	return filepath.Join(dir, "rgdevenv", "cli.toml")
}
