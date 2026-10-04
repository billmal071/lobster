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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Parent is the directory created inside the chosen base. It is dot-prefixed
// so it stays out of the user's way in a file manager; that is safe because
// snapd's home interface only hides paths whose first component below $HOME is
// dot-prefixed, not nested ones.
const Parent = ".lobster"

// Marker is the file lobster writes inside every directory it creates here. It
// is proof of ownership, and PruneStale deletes nothing without it.
//
// A prefix match is not proof. PruneStale's parent is a directory the user
// chose, so it may already hold directories of their own, and "starts with
// torrent-" is a name a user is entitled to use — a ~/Downloads/torrent-backups
// older than the stale window would be removed recursively by a sweep that
// trusted the name alone. The marker distinguishes "lobster made this and lost
// track of it" from "this was already here".
const Marker = ".lobster-dir"

// baseNames are the candidate base directories, relative to $HOME and most
// preferred first.
//
// A media directory is the least surprising place for lobster's scratch files.
// The last entry is a plain "lobster" directory, used only when no media
// directory exists, and created rather than required to exist.
var baseNames = []string{"Videos", "Movies", "Downloads", "lobster"}

// afterBaseResolved runs between resolving a base and creating anything in it.
// It is the TOCTOU window, and is a no-op outside the test that widens it on
// purpose to prove the window is not exploitable.
var afterBaseResolved = func(base string) {}

// ErrUnusable reports that every candidate directory, the temp-dir fallback
// included, was rejected by the caller's usable check.
var ErrUnusable = errors.New("userdir: no usable scratch directory")

// Make creates a fresh directory named "<prefix>-<random>" inside
// <base>/Parent under $HOME, sweeping abandoned siblings older than staleAfter
// as it goes.
//
// Every directory below $HOME is created through an os.Root pinned to the
// resolved home, so nothing can be created outside it — not even by swapping a
// component for an outward symlink between the check and the creation, which a
// pathname-based check cannot rule out. A media directory symlinked to another
// drive is a common arrangement and would otherwise be accepted silently:
// os.Stat and os.MkdirAll both follow symlinks, while snapd's home interface
// is enforced by AppArmor on the resolved path, so staging there looks like a
// fix and is none.
//
// Symlinks that stay inside $HOME are still used. They are perfectly
// reachable, and refusing them would push those users onto the fallback for no
// reason — which is why the root cannot simply be opened at $HOME and the base
// names walked through it: os.Root refuses an absolute symlink even when its
// target is inside the root, and absolute is the usual way such a link is
// written. Instead each base is resolved to a path relative to the resolved
// home, and that relative path is what the root operations act on. The
// resolution only proposes a candidate; the root operations are the authority,
// so a swap afterwards makes them fail rather than escape.
//
// usable, when non-nil, vetoes a candidate directory the caller cannot work
// with — a Unix socket path too long for sun_path, say. A vetoed directory is
// removed and the next base tried.
//
// visible reports whether the result is under $HOME, and so readable by a
// confined helper. When no base works Make falls back to os.MkdirTemp and
// returns visible=false: a private-/tmp handoff is broken, but refusing to run
// at all is worse. If usable rejects even that, Make returns ErrUnusable, and
// a caller that can degrade further should handle it rather than fail.
func Make(prefix string, staleAfter time.Duration, usable func(dir string) bool) (dir string, visible bool, err error) {
	if home, root, ok := openHome(); ok {
		defer root.Close()
		for i, base := range baseNames {
			// The media directories must already exist — lobster does not
			// invent a Videos folder. Only the last-resort base is created.
			mustExist := i < len(baseNames)-1
			rel, ok := relativeBase(root.Name(), home, base, mustExist)
			if !ok {
				continue
			}
			afterBaseResolved(base)
			parent := path.Join(rel, Parent)
			// Root-relative, so this fails rather than escapes when a
			// component is or becomes a symlink out of the home directory.
			if err := root.MkdirAll(parent, 0o700); err != nil {
				continue
			}
			pruneStaleIn(root, parent, prefix, staleAfter, false)
			name, err := mkdirRandomIn(root, parent, prefix)
			if err != nil {
				continue
			}
			if err := markOwnedIn(root, name); err != nil {
				_ = root.RemoveAll(name)
				continue
			}
			candidate := filepath.Join(root.Name(), filepath.FromSlash(name))
			if usable != nil && !usable(candidate) {
				_ = root.RemoveAll(name)
				continue
			}
			return candidate, true, nil
		}
	}

	candidate, err := os.MkdirTemp("", "lobster-"+prefix+"-*")
	if err != nil {
		return "", false, err
	}
	if err := MarkOwned(candidate); err != nil {
		Remove(candidate)
		return "", false, err
	}
	if usable != nil && !usable(candidate) {
		Remove(candidate)
		return "", false, ErrUnusable
	}
	return candidate, false, nil
}

// openHome opens the resolved $HOME as an os.Root, through which every path
// below it is resolved without the possibility of escaping. root.Name() is the
// resolved home, so paths built from it carry no symlinks of their own.
func openHome() (home string, root *os.Root, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", nil, false
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", nil, false
	}
	root, err = os.OpenRoot(resolved)
	if err != nil {
		return "", nil, false
	}
	return home, root, true
}

// relativeBase proposes where base sits relative to the resolved home.
//
// A base that is a symlink is followed, so one pointing elsewhere inside $HOME
// yields its real location; one pointing out yields a path that escapes and is
// rejected. mustExist is false only for the last-resort base, which is created
// rather than required, and so has nothing to resolve when it is absent.
//
// The answer is a proposal, not a guarantee: it is derived from pathnames and
// could be stale by the time it is used. Every mutation is then performed
// root-relative, which turns a stale answer into a failure instead of an
// escape.
func relativeBase(resolvedHome, home, base string, mustExist bool) (string, bool) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(home, base))
	if err != nil {
		return base, !mustExist
	}
	rel, err := filepath.Rel(resolvedHome, resolved)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// mkdirRandomIn creates a uniquely named directory inside parent, which is
// relative to root. It is os.MkdirTemp's retry-on-collision loop expressed in
// root-relative operations, which os.MkdirTemp has no equivalent of.
func mkdirRandomIn(root *os.Root, parent, prefix string) (string, error) {
	for range 100 {
		name := path.Join(parent, prefix+"-"+randomSuffix())
		err := root.Mkdir(name, 0o700)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("userdir: no unused directory name after 100 attempts")
}

func randomSuffix() string {
	var b [8]byte
	// crypto/rand.Read does not fail on any supported platform; it terminates
	// the process instead, so there is no error case to fold into the name.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// MarkOwned writes the ownership marker into a directory lobster created, so a
// later PruneStale is allowed to sweep it. Callers that create their own
// directory inside a user-chosen parent — rather than going through Make — must
// call this, or their abandoned directories accumulate forever.
func MarkOwned(dir string) error {
	return os.WriteFile(filepath.Join(dir, Marker), nil, 0o600)
}

// markOwnedIn is MarkOwned performed root-relative, so it cannot be redirected
// outside the home directory by a component swapped for a symlink.
func markOwnedIn(root *os.Root, dir string) error {
	f, err := root.Create(path.Join(dir, Marker))
	if err != nil {
		return err
	}
	return f.Close()
}

// PruneStale removes "<prefix>-*" directories directly inside parent that carry
// the ownership Marker and have not been written to for staleAfter, i.e. those
// left behind by runs that were killed before they could clean up. A directory
// without the marker is never touched, however old it is and whatever it is
// called: parent here is a directory the user chose, and anything in it that
// lobster did not create is theirs. Unlike /tmp, a directory under $HOME is
// never reclaimed by the system, so somebody has to.
//
// A directory's mtime is set when its files are written and does not advance
// while a helper reads from them, so a session running longer than staleAfter
// can have its own live directory swept by a second lobster process starting
// in the meantime. Whether that matters is the caller's to judge: pick a
// staleAfter comfortably longer than any handoff whose disappearance the
// helper would notice.
func PruneStale(parent, prefix string, staleAfter time.Duration) {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return
	}
	defer root.Close()
	pruneStaleIn(root, ".", prefix, staleAfter, true)
}

// pruneStaleIn sweeps parent, relative to root. requireMarker is set for the
// exported PruneStale, whose parent is user-chosen; it is not set for the sweep
// Make does inside its own Parent directory, which lobster creates 0o700 and
// nothing else writes to. Requiring the marker there would permanently orphan
// the directories earlier versions left behind, with nothing left to sweep them.
func pruneStaleIn(root *os.Root, parent, prefix string, staleAfter time.Duration, requireMarker bool) {
	d, err := root.Open(parent)
	if err != nil {
		return
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
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
		name := path.Join(parent, e.Name())
		if requireMarker {
			if _, err := root.Stat(path.Join(name, Marker)); err != nil {
				continue
			}
		}
		_ = root.RemoveAll(name)
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
