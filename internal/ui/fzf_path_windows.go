//go:build windows

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fzfBinary returns the path to the fzf binary, checking common Windows
// install locations when fzf is not found in PATH.
func fzfBinary() (string, error) {
	// Try PATH first
	if p, err := exec.LookPath("fzf"); err == nil {
		return p, nil
	}

	// Common install locations on Windows
	candidates := []string{}

	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		// winget shim
		candidates = append(candidates, filepath.Join(local, "Microsoft", "WinGet", "Links", "fzf.exe"))
	}

	if home := os.Getenv("USERPROFILE"); home != "" {
		// scoop
		candidates = append(candidates, filepath.Join(home, "scoop", "shims", "fzf.exe"))
	}

	if choco := os.Getenv("ChocolateyInstall"); choco != "" {
		candidates = append(candidates, filepath.Join(choco, "bin", "fzf.exe"))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("fzf not found — install with: winget install junegunn.fzf")
}
