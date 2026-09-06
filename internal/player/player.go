// Package player provides a secure interface for launching media players.
// All player invocations use exec.Command with explicit argument slices,
// preventing shell injection that was possible with the original eval-based approach.
package player

import (
	"fmt"
	"runtime"

	"lobster/internal/media"
)

// PlayResult holds the final playback state after a player exits.
type PlayResult struct {
	Position float64 // last playback position in seconds
	Duration float64 // total media duration in seconds (0 if unknown)

	// PositionUnknown reports that this session tracked the playback position
	// and never observed one — mpv's IPC socket never came up within the dial
	// bound, or mpv never reported a time-pos over it. Position is then a
	// default rather than a measurement, and persisting it would overwrite a
	// real resume point recorded by an earlier watch of the same title.
	//
	// It is deliberately false for a session that genuinely sat at position 0,
	// so the two stay distinguishable, and false for players that do not
	// report positions at all (vlc, generic): those make no claim either way
	// and history goes on recording their watches exactly as before.
	PositionUnknown bool
}

// Player is the interface for media player implementations.
type Player interface {
	// Play starts playback of a stream. Returns the final playback state.
	Play(stream *media.Stream, title string, startPos float64, subFiles []string) (PlayResult, error)

	// Name returns the player name.
	Name() string

	// Available checks if the player binary exists in PATH.
	Available() bool
}

// Checkpointer is implemented by players that can report the playback
// position periodically while playback is still running (currently only mpv,
// the one player tracked over IPC). Callers type-assert for it and install a
// callback so a hard shutdown mid-watch loses at most one checkpoint interval
// of resume position instead of the whole watch. The callback is never
// invoked after Play returns.
type Checkpointer interface {
	SetCheckpoint(fn func(position, duration float64))
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

// New creates a player by name. audioLang is the preferred audio-track
// language; it is passed to the player so a multi-dub release does not default
// to whichever track the muxer happened to list first.
func New(name, audioLang string) Player {
	switch name {
	case "mpv":
		return &MPV{audioLang: audioLang}
	case "vlc":
		return &VLC{audioLang: audioLang}
	case "iina", "celluloid":
		return &Generic{name: name, audioLang: audioLang}
	default:
		return &MPV{audioLang: audioLang} // Default to mpv
	}
}
