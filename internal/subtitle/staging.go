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
//
// Being under $HOME lexically is not enough — see withinHome.
func stagingBases() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	resolvedHome, ok := resolveHome()
	if !ok {
		return nil
	}

	var existing []string
	for _, name := range []string{"Videos", "Movies", "Downloads"} {
		dir := filepath.Join(home, name)
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		if !withinHome(resolvedHome, dir) {
			continue
		}
		existing = append(existing, dir)
	}
	// The last resort needs no check here: newStagingDir creates it directly
	// under $HOME and verifies the directory it actually reached.
	return append(existing, filepath.Join(home, "lobster"))
}

// resolveHome returns $HOME with symlinks resolved, and false when there is no
// usable home directory.
func resolveHome() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// withinHome reports whether path, once every symlink in it is resolved, is
// still inside resolvedHome.
//
// A media directory symlinked to another drive is a common arrangement, and
// os.Stat and os.MkdirAll both follow symlinks without complaint — so
// "$HOME/Videos/.lobster/subs-x" can name a file on /mnt. snapd's home
// interface is enforced by AppArmor, which decides on the resolved path, so
// such a directory is denied to a confined player exactly as /tmp is: staging
// there would look like a fix and silently be none. A path that cannot be
// resolved is treated as outside, since the point is to hand out only paths
// known to be reachable.
//
// This rejects escapes, not symlinks. One that resolves back inside $HOME is
// perfectly readable, and refusing it would push those users onto the
// broken-for-snaps temp-dir fallback for no reason.
func withinHome(resolvedHome, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedHome, resolved)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// newStagingDir creates a fresh staging directory a confined player can read,
// falling back to os.MkdirTemp when nothing under $HOME is usable (a container
// with no writable home, say) — an unreadable subtitle is better than no
// playback at all.
func newStagingDir() (string, error) {
	resolvedHome, homeOK := resolveHome()
	for _, base := range stagingBases() {
		parent := filepath.Join(base, stagingParent)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			continue
		}
		// The base passed stagingBases, but stagingParent itself can be a
		// symlink out of $HOME, and the last-resort base was never checked.
		// Verify the path actually reached, not the one asked for.
		if !homeOK || !withinHome(resolvedHome, parent) {
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
// before they could clean up.
//
// A staging directory's mtime is set when its subtitle files are written, at
// the start of playback, and reading those files afterwards never advances it.
// So a session that runs longer than staleAfter can have its own live staging
// directory swept by a second lobster process starting in the meantime. That
// window is left open deliberately rather than guarded with a lock: staged
// files are handed to the player only as launch arguments, and nothing reopens
// one mid-playback. mpv is given every track at launch and then holds no
// descriptor on the files at all — selecting a track whose file has since been
// deleted still renders it, because the file was read in full at load time —
// and VLC is given a single file at launch. Losing the directory under a
// running player is therefore not observable; the process's own Cleanup on a
// pruned directory is a no-op.
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
