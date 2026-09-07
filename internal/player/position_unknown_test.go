package player

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"lobster/internal/media"
)

// stubRunPlayerCmd replaces the process runner so a Play call exercises the
// full argument-building and return path without ever launching a real media
// player. It restores the production runner afterwards.
func stubRunPlayerCmd(t *testing.T, err error) {
	t.Helper()
	prev := runPlayerCmd
	runPlayerCmd = func(*exec.Cmd) error { return err }
	t.Cleanup(func() { runPlayerCmd = prev })
}

// exitErr is the *exec.ExitError the players special-case: VLC and the mpv
// frontends exit non-zero when the user closes the window, which is a normal
// end of playback rather than a failure. It is built by hand rather than by
// running a command, so no test here spawns a process; ProcessState is
// non-nil because the error formats itself through it.
func exitErr() error {
	return &exec.ExitError{ProcessState: &os.ProcessState{}}
}

// checkUntracked asserts the pair of flags a player with no position tracking
// owes its caller. PositionUnknown says the zero Position is a default rather
// than a measurement, so it must not reach history; PositionUntracked says
// why — the player never had tracking — which is what tells the save paths to
// record the watch while keeping the stored position, instead of skipping it
// the way a tracked-but-blind session is skipped.
func checkUntracked(t *testing.T, name string, result PlayResult, when string) {
	t.Helper()
	if !result.PositionUnknown {
		t.Fatalf("%s Play %s returned PositionUnknown = false with Position %g; %s cannot observe a position, so 0 here is a default and must not be persisted",
			name, when, result.Position, name)
	}
	if !result.PositionUntracked {
		t.Fatalf("%s Play %s returned PositionUntracked = false; without it the save paths skip the entry entirely and a title watched only in %s never reaches history",
			name, when, name)
	}
}

// Neither VLC nor the mpv frontends observe a playback position, so every
// result they return carries a default 0 rather than a measurement. They must
// say so: a result with PositionUnknown false claims position 0 was really
// seen, and the history save paths then write that 0 over the resume point an
// earlier tracked watch recorded.
func TestNonTrackingPlayersReportPositionUnknown(t *testing.T) {
	stream := &media.Stream{URL: "https://example.invalid/stream.m3u8"}

	for _, tc := range []struct {
		name   string
		player Player
	}{
		{"vlc", &VLC{}},
		{"iina", &Generic{name: "iina"}},
		{"celluloid", &Generic{name: "celluloid"}},
	} {
		t.Run(tc.name+"/clean exit", func(t *testing.T) {
			stubRunPlayerCmd(t, nil)
			result, err := tc.player.Play(stream, "Title", 0, nil)
			if err != nil {
				t.Fatalf("Play returned error %v, want nil", err)
			}
			checkUntracked(t, tc.name, result, "on a clean exit")
		})

		t.Run(tc.name+"/user closed the window", func(t *testing.T) {
			stubRunPlayerCmd(t, exitErr())
			result, err := tc.player.Play(stream, "Title", 0, nil)
			if err != nil {
				t.Fatalf("Play returned error %v, want nil for a non-zero exit", err)
			}
			checkUntracked(t, tc.name, result, "after a non-zero exit")
		})

		t.Run(tc.name+"/launch failed", func(t *testing.T) {
			stubRunPlayerCmd(t, errors.New("boom"))
			result, err := tc.player.Play(stream, "Title", 0, nil)
			if err == nil {
				t.Fatalf("Play returned nil error for a failed launch")
			}
			checkUntracked(t, tc.name, result, "alongside a launch error")
		})
	}
}
