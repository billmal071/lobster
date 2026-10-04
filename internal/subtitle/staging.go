package subtitle

import (
	"time"

	"lobster/internal/userdir"
)

// staleAfter is how long a staging directory may go without a sign of life
// before a later run sweeps it.
//
// The sign of life is the ownership marker, refreshed every stagingTouchEvery
// for as long as a run holds the directory — not the directory's own mtime,
// which is set when the subtitle files are written at the start of playback and
// does not move again. Without the refresh, a session outlasting staleAfter read
// as abandoned to a second lobster starting in the meantime, which swept the
// staging directory out from under the running player; a film paused overnight
// and resumed the next evening is long enough.
//
// Whether that was *observable* rested on claims about mpv and VLC holding no
// descriptor on files handed to them as launch arguments. Nothing here has
// measured those claims, and they are claims about programs lobster does not
// package and does not pin a version of. The refresh makes being right about
// them unnecessary.
const staleAfter = 24 * time.Hour

// stagingTouchEvery is how often a run refreshes its staging directory's marker.
// A quarter of the stale window leaves three missed refreshes of slack before a
// second lobster could consider this run abandoned, at a cost of one timestamp
// update every six hours.
//
// The ratio is the same one the torrent data directory uses, and deliberately so
// — what the interval has to outpace is the stale window, which each package
// chooses for itself, not the size of what is in the directory. The constant
// stays per-package for that reason: a shared one would silently stop leaving
// three refreshes of slack the moment one package shortened its window. It is a
// var so a test can drive the loop rather than wait out a real interval.
var stagingTouchEvery = staleAfter / 4

// stagingPrefix names staging directories within the shared parent.
const stagingPrefix = "subs"

// newStagingDir creates a fresh staging directory a confined player can read,
// falling back to the system temp dir when nothing under $HOME is usable (a
// container with no writable home, say) — an unreadable subtitle is better
// than no playback at all.
//
// stopAlive ends the liveness refresh and must be called once the directory is
// released, which removeStagingDir does.
func newStagingDir() (dir string, stopAlive func(), err error) {
	dir, _, err = userdir.Make(stagingPrefix, staleAfter, nil)
	if err != nil {
		return "", nil, err
	}
	return dir, userdir.Keepalive(dir, stagingTouchEvery), nil
}

// removeStagingDir stops the liveness refresh, then deletes the staging
// directory and, if that leaves the shared parent empty, the parent too.
//
// stopAlive may be nil — a TempDir built around a directory this package did not
// create has no refresh to stop — and userdir.Keepalive's stop is idempotent, so
// a repeated Cleanup is harmless.
func removeStagingDir(dir string, stopAlive func()) {
	if stopAlive != nil {
		stopAlive()
	}
	userdir.Remove(dir)
}
