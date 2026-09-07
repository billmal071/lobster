// Package userdir picks scratch directories that a sandboxed helper program
// can actually see.
//
// lobster hands file paths to programs it does not package: mpv, VLC, and
// whatever a user configures. When one of those is packaged as a snap it runs
// with a private /tmp, so a path lobster wrote under os.TempDir() does not
// exist as far as the helper is concerned — and, in the other direction, a
// socket the helper creates there is invisible to lobster. Nothing errors; the
// handoff just silently does not happen.
//
// The confinement matrix was measured against the VLC snap (3.0.20, rev 3777)
// when subtitle staging first hit this: inside the sandbox /tmp is an empty
// private mount and $HOME is remapped, absolute host paths under ~/.cache and
// ~/.local/share are denied, and only the FIRST path component below $HOME
// decides visibility — a dot-directory nested inside a visible one is fine.
// So a usable handoff directory is <visible dir under $HOME>/.lobster/<name>.
package userdir

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Parent is the directory created inside the chosen base. It is dot-prefixed
// so it stays out of the user's way in a file manager; that is safe because
// snapd's home interface only hides paths whose first component below $HOME is
// dot-prefixed, not nested ones.
const Parent = ".lobster"

// Bases returns candidate base directories, most preferred first.
//
// A media directory is the least surprising place for lobster's scratch files.
// The last entry is a plain "lobster" directory, offered only when no media
// directory exists, and created rather than required to exist.
func Bases() []string {
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

// Make creates a fresh directory named "<prefix>-<random>" inside
// <base>/Parent for the first base that works, sweeping abandoned siblings
// older than staleAfter as it goes.
//
// usable, when non-nil, vetoes a candidate directory the caller cannot work
// with — a Unix socket path too long for sun_path, say. A vetoed directory is
// removed and the next base tried.
//
// visible reports whether the result is under $HOME, and so readable by a
// confined helper. When no base works Make falls back to os.MkdirTemp and
// returns visible=false: a private-/tmp handoff is broken, but refusing to run
// at all is worse. Callers that can degrade gracefully should say so when
// visible is false rather than fail.
func Make(prefix string, staleAfter time.Duration, usable func(dir string) bool) (dir string, visible bool, err error) {
	for _, base := range Bases() {
		parent := filepath.Join(base, Parent)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			continue
		}
		PruneStale(parent, prefix, staleAfter)
		candidate, err := os.MkdirTemp(parent, prefix+"-*")
		if err != nil {
			continue
		}
		if usable != nil && !usable(candidate) {
			Remove(candidate)
			continue
		}
		return candidate, true, nil
	}

	candidate, err := os.MkdirTemp("", "lobster-"+prefix+"-*")
	if err != nil {
		return "", false, err
	}
	if usable != nil && !usable(candidate) {
		Remove(candidate)
		return "", false, errUnusable
	}
	return candidate, false, nil
}

// errUnusable reports that every candidate directory, the temp-dir fallback
// included, was vetoed by the caller's usable check.
var errUnusable = unusableError{}

type unusableError struct{}

func (unusableError) Error() string { return "no usable scratch directory" }

// PruneStale removes "<prefix>-*" directories under parent that have not been
// written to for staleAfter, i.e. those left behind by runs that were killed
// before they could clean up. Unlike /tmp, a directory under $HOME is never
// reclaimed by the system, so somebody has to.
//
// A directory's mtime is set when its files are written and does not advance
// while a helper reads from them, so a session running longer than staleAfter
// can have its own live directory swept by a second lobster process starting
// in the meantime. Whether that matters is the caller's to judge: pick a
// staleAfter comfortably longer than any handoff whose disappearance the
// helper would notice.
func PruneStale(parent, prefix string, staleAfter time.Duration) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleAfter)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix+"-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, e.Name()))
	}
}

// Remove deletes dir and, if that leaves the shared parent empty, the parent
// too. os.Remove refuses a non-empty directory, so a concurrent run's
// directory is never disturbed.
func Remove(dir string) {
	if dir == "" {
		return
	}
	_ = os.RemoveAll(dir)
	parent := filepath.Dir(dir)
	if filepath.Base(parent) == Parent {
		_ = os.Remove(parent)
	}
}
