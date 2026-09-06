package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stagingParent is the directory created inside the chosen base directory. It
// is dot-prefixed so it stays out of the user's way in a file manager; that is
// safe because snapd's home interface only hides paths whose *first* component
// below $HOME is dot-prefixed, not nested ones.
const stagingParent = ".lobster"

// staleAfter is how long an abandoned staging directory is kept before a later
// run sweeps it. A hard kill skips Cleanup, and unlike /tmp a directory under
// $HOME is never reclaimed by the system.
const staleAfter = 24 * time.Hour

// stagingBases returns candidate base directories for subtitle staging, most
// preferred first.
//
// Players packaged as snaps (the VLC snap on this machine, VLC 3.0.20) run with
// a private /tmp, so a file the host wrote to os.TempDir() does not exist as far
// as the player is concerned. Their view of the user's files comes from snapd's
// home interface, which exposes $HOME but denies any path whose first component
// below $HOME is dot-prefixed. Both halves were verified empirically against
// this VLC snap: a subtitle in /tmp fails to open with "No such file or
// directory", one under ~/.cache with "Permission denied", and one under
// ~/Videos/.lobster/ loads.
//
// So the base has to be a non-hidden directory under $HOME. A media directory
// is the least surprising home for subtitle files; the last resort is a plain
// "lobster" directory, created only when no media directory exists.
func stagingBases() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}

	var existing []string
	for _, name := range []string{"Videos", "Movies", "Downloads"} {
		dir := filepath.Join(home, name)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			existing = append(existing, dir)
		}
	}
	return append(existing, filepath.Join(home, "lobster"))
}

// newStagingDir creates a fresh staging directory a confined player can read,
// falling back to os.MkdirTemp when nothing under $HOME is usable (a container
// with no writable home, say) — an unreadable subtitle is better than no
// playback at all.
func newStagingDir() (string, error) {
	for _, base := range stagingBases() {
		parent := filepath.Join(base, stagingParent)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			continue
		}
		pruneStale(parent)
		dir, err := os.MkdirTemp(parent, "subs-*")
		if err != nil {
			continue
		}
		return dir, nil
	}
	return os.MkdirTemp("", "lobster-subs-*")
}

// pruneStale removes staging directories left behind by runs that were killed
// before they could clean up. Best effort: anything still in use by a
// concurrent run is younger than staleAfter and is left alone.
func pruneStale(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleAfter)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "subs-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, e.Name()))
	}
}

// removeStagingDir deletes a staging directory and, if that leaves the shared
// parent empty, the parent too. os.Remove refuses a non-empty directory, so a
// concurrent run's staging directory is never disturbed.
func removeStagingDir(dir string) {
	if dir == "" {
		return
	}
	_ = os.RemoveAll(dir)
	parent := filepath.Dir(dir)
	if filepath.Base(parent) == stagingParent {
		_ = os.Remove(parent)
	}
}
