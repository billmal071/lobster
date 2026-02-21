package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Player != "mpv" {
		t.Errorf("default player = %q, want mpv", cfg.Player)
	}
	if cfg.Quality != "1080" {
		t.Errorf("default quality = %q, want 1080", cfg.Quality)
	}
	if cfg.Provider != "Vidcloud" {
		t.Errorf("default provider = %q, want Vidcloud", cfg.Provider)
	}
	if !cfg.History {
		t.Error("default history should be true")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{"valid defaults", func(c *Config) {}, false},
		{"invalid player", func(c *Config) { c.Player = "notepad" }, true},
		{"invalid provider", func(c *Config) { c.Provider = "BadServer" }, true},
		{"invalid quality", func(c *Config) { c.Quality = "4k" }, true},
		{"empty base", func(c *Config) { c.Base = "" }, true},
		{"valid vlc", func(c *Config) { c.Player = "vlc" }, false},
		{"valid upcloud", func(c *Config) { c.Provider = "UpCloud" }, false},
		{"valid 720", func(c *Config) { c.Quality = "720" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadFromTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
base = "example.com"
player = "vlc"
provider = "UpCloud"
quality = "720"
history = false
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Override config dir for testing
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create the lobster subdir and move the config
	lobsterDir := filepath.Join(tmpDir, "lobster")
	os.MkdirAll(lobsterDir, 0755)
	os.Rename(configPath, filepath.Join(lobsterDir, "config.toml"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Base != "example.com" {
		t.Errorf("base = %q, want example.com", cfg.Base)
	}
	if cfg.Player != "vlc" {
		t.Errorf("player = %q, want vlc", cfg.Player)
	}
	if cfg.Provider != "UpCloud" {
		t.Errorf("provider = %q, want UpCloud", cfg.Provider)
	}
	if cfg.Quality != "720" {
		t.Errorf("quality = %q, want 720", cfg.Quality)
	}
	if cfg.History {
		t.Error("history should be false")
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error on missing file: %v", err)
	}
	if cfg.Player != "mpv" {
		t.Errorf("missing file should return defaults, got player = %q", cfg.Player)
	}
}

func TestExpandDownloadDir(t *testing.T) {
	cfg := Default()
	cfg.DownloadDir = "/tmp/test-downloads"

	dir, err := cfg.ExpandDownloadDir()
	if err != nil {
		t.Fatalf("ExpandDownloadDir() error: %v", err)
	}
	if dir != "/tmp/test-downloads" {
		t.Errorf("got %q, want /tmp/test-downloads", dir)
	}
}
