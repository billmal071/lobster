// Package player provides a secure interface for launching media players.
// All player invocations use exec.Command with explicit argument slices,
// preventing shell injection that was possible with the original eval-based approach.
package player

import (
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
