//go:build windows

package player

import (
	"os"
	"os/exec"
	"path/filepath"
)

// mpvBinaryName returns the mpv binary path, checking common Windows
// install locations when mpv is not found in PATH.
func mpvBinaryName() string {
	if p, err := exec.LookPath("mpv"); err == nil {
		return p
	}

	candidates := []string{}

	if home := os.Getenv("USERPROFILE"); home != "" {
		// scoop
		candidates = append(candidates, filepath.Join(home, "scoop", "shims", "mpv.exe"))
		candidates = append(candidates, filepath.Join(home, "scoop", "apps", "mpv", "current", "mpv.exe"))
	}

	if choco := os.Getenv("ChocolateyInstall"); choco != "" {
		candidates = append(candidates, filepath.Join(choco, "bin", "mpv.exe"))
	}

	programFiles := os.Getenv("ProgramFiles")
	if programFiles != "" {
		candidates = append(candidates, filepath.Join(programFiles, "mpv", "mpv.exe"))
	}

	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	if programFilesX86 != "" {
		candidates = append(candidates, filepath.Join(programFilesX86, "mpv", "mpv.exe"))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "mpv"
}
