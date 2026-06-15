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

func TestLoadMalformedTOMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.toml")
	if err := os.WriteFile(path, []byte("api = [not valid toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RGDEVENV_API", "")
	t.Setenv("RGDEVENV_TOKEN", "")
	t.Setenv("RGDEVENV_INSECURE", "")
	if _, err := Load(path); err == nil {
		t.Fatal("malformed TOML must return an error")
	}
}

func TestLoadInsecureFalseFromEnv(t *testing.T) {
	t.Setenv("RGDEVENV_API", "https://x")
	t.Setenv("RGDEVENV_TOKEN", "y")
	t.Setenv("RGDEVENV_INSECURE", "false")
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Insecure {
		t.Fatal("RGDEVENV_INSECURE=false must disable insecure")
	}
}

func TestValidate(t *testing.T) {
	if err := (Config{API: "https://x", Token: "y"}).Validate(); err != nil {
		t.Fatalf("valid config must pass: %v", err)
	}
	if err := (Config{Token: "y"}).Validate(); err == nil {
		t.Fatal("missing API must fail validation")
	}
	if err := (Config{API: "https://x"}).Validate(); err == nil {
		t.Fatal("missing token must fail validation")
	}
}
