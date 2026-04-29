// Package player provides a secure interface for launching media players.
// All player invocations use exec.Command with explicit argument slices,
// preventing shell injection that was possible with the original eval-based approach.
package player

import (
	"fmt"
	"runtime"

	"lobster/internal/media"
)

// Player is the interface for media player implementations.
type Player interface {
	// Play starts playback of a stream. Returns the last playback position.
	Play(stream *media.Stream, title string, startPos float64, subFile string) (float64, error)

	// Name returns the player name.
	Name() string

	// Available checks if the player binary exists in PATH.
	Available() bool
}

// NotFoundError returns a helpful error message when a player binary is missing.
func NotFoundError(name string) error {
	if runtime.GOOS == "windows" {
		switch name {
		case "mpv":
			return fmt.Errorf("mpv not found — install with one of:\n  winget install mpv\n  scoop install mpv\n  choco install mpv\nMake sure mpv is in your PATH, or installed in a standard location")
		case "vlc":
			return fmt.Errorf("vlc not found — install with one of:\n  winget install VideoLAN.VLC\n  scoop install vlc\n  choco install vlc")
		default:
			return fmt.Errorf("player %q not found in PATH", name)
		}
	}
	return fmt.Errorf("player %q not found in PATH", name)
}

// New creates a player by name.
func New(name string) Player {
	switch name {
	case "mpv":
		return &MPV{}
	case "vlc":
		return &VLC{}
	case "iina", "celluloid":
		return &Generic{name: name}
	default:
		return &MPV{} // Default to mpv
	}
}
