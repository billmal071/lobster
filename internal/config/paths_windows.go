//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// configDir returns the Windows config directory (%APPDATA%\lobster).
func configDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData != "" {
		return filepath.Join(appData, "lobster"), nil
	}
	// Fallback to os.UserConfigDir which returns %APPDATA% on Windows
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("getting config directory: %w", err)
	}
	return filepath.Join(dir, "lobster"), nil
}

// dataDir returns the Windows data directory (%LOCALAPPDATA%\lobster).
func dataDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		return filepath.Join(localAppData, "lobster"), nil
	}
	// Fallback
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, "AppData", "Local", "lobster"), nil
}
