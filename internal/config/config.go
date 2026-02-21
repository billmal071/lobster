// Package config handles TOML-based configuration loading and validation.
// Unlike the original shell script which executed config as shell code,
// TOML is parsed as data only — no code execution is possible.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all application configuration.
type Config struct {
	Base         string `toml:"base"`
	Player       string `toml:"player"`
	Provider     string `toml:"provider"`
	SubsLanguage string `toml:"subs_language"`
	Quality      string `toml:"quality"`
	History      bool   `toml:"history"`
	DownloadDir  string `toml:"download_dir"`
	Debug        bool   `toml:"debug"`
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Base:         "flixhq.to",
		Player:       "mpv",
		Provider:     "Vidcloud",
		SubsLanguage: "english",
		Quality:      "1080",
		History:      true,
		DownloadDir:  "~/Videos/lobster",
		Debug:        false,
	}
}

// configDir returns the XDG-compliant config directory.
func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "lobster"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".config", "lobster"), nil
}

// ConfigPath returns the path to the config file.
func ConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads the config file and merges with defaults.
// If the config file doesn't exist, defaults are returned.
func Load() (*Config, error) {
	cfg := Default()

	path, err := ConfigPath()
	if err != nil {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// Validate checks config values are within acceptable bounds.
func (c *Config) Validate() error {
	validPlayers := map[string]bool{
		"mpv": true, "vlc": true, "iina": true, "celluloid": true,
	}
	if !validPlayers[strings.ToLower(c.Player)] {
		return fmt.Errorf("unsupported player %q (valid: mpv, vlc, iina, celluloid)", c.Player)
	}

	validProviders := map[string]bool{
		"vidcloud": true, "upcloud": true,
	}
	if !validProviders[strings.ToLower(c.Provider)] {
		return fmt.Errorf("unsupported provider %q (valid: Vidcloud, UpCloud)", c.Provider)
	}

	validQualities := map[string]bool{
		"360": true, "480": true, "720": true, "1080": true,
	}
	if !validQualities[c.Quality] {
		return fmt.Errorf("unsupported quality %q (valid: 360, 480, 720, 1080)", c.Quality)
	}

	if c.Base == "" {
		return fmt.Errorf("base URL cannot be empty")
	}

	return nil
}

// ExpandDownloadDir resolves ~ in the download directory path.
func (c *Config) ExpandDownloadDir() (string, error) {
	dir := c.DownloadDir
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding home dir: %w", err)
		}
		dir = filepath.Join(home, dir[2:])
	}
	return filepath.Abs(dir)
}

// HistoryPath returns the path to the history file.
func HistoryPath() (string, error) {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataDir, "lobster", "history.tsv"), nil
}
