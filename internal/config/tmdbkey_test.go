package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setConfigDir(t *testing.T, contents string) {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", dir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	if contents == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "lobster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lobster", "config.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTMDBKeyFromFile(t *testing.T) {
	t.Setenv("LOBSTER_TMDB_KEY", "")
	setConfigDir(t, `tmdb_api_key = "filekey"`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDBAPIKey != "filekey" {
		t.Errorf("TMDBAPIKey = %q, want filekey", cfg.TMDBAPIKey)
	}
}

func TestTMDBKeyEnvOverridesFile(t *testing.T) {
	t.Setenv("LOBSTER_TMDB_KEY", "  envkey \n")
	setConfigDir(t, `tmdb_api_key = "filekey"`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDBAPIKey != "envkey" {
		t.Errorf("TMDBAPIKey = %q, want envkey (trimmed, env wins)", cfg.TMDBAPIKey)
	}
}

func TestTMDBKeyEnvWithoutConfigFile(t *testing.T) {
	t.Setenv("LOBSTER_TMDB_KEY", "envkey")
	setConfigDir(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDBAPIKey != "envkey" {
		t.Errorf("TMDBAPIKey = %q, want envkey", cfg.TMDBAPIKey)
	}
}

func TestTMDBKeyDefaultsEmpty(t *testing.T) {
	t.Setenv("LOBSTER_TMDB_KEY", "")
	setConfigDir(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDBAPIKey != "" {
		t.Errorf("TMDBAPIKey = %q, want empty", cfg.TMDBAPIKey)
	}
}

func TestTMDBKeyNeverFormatted(t *testing.T) {
	cfg := Default()
	cfg.TMDBAPIKey = "supersecretkey"
	for _, s := range []string{
		fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%v", *cfg), fmt.Sprintf("%+v", *cfg), fmt.Sprintf("%#v", *cfg),
	} {
		if strings.Contains(s, "supersecretkey") {
			t.Errorf("formatted config leaks the key: %s", s)
		}
	}
}
