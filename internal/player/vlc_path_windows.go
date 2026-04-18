//go:build windows

package player

import (
	"os"
	"os/exec"
	"path/filepath"
)

// vlcBinaryName returns the VLC binary name for the current platform.
// On Windows, VLC is often not in PATH, so check common install locations.
func vlcBinaryName() string {
	// If vlc is in PATH, use it directly
	if _, err := exec.LookPath("vlc"); err == nil {
		return "vlc"
	}

	// Check common Windows install paths
	programFiles := os.Getenv("ProgramFiles")
	if programFiles != "" {
		vlcPath := filepath.Join(programFiles, "VideoLAN", "VLC", "vlc.exe")
		if _, err := os.Stat(vlcPath); err == nil {
			return vlcPath
		}
	}

	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	if programFilesX86 != "" {
		vlcPath := filepath.Join(programFilesX86, "VideoLAN", "VLC", "vlc.exe")
		if _, err := os.Stat(vlcPath); err == nil {
			return vlcPath
		}
	}

	return "vlc"
}
