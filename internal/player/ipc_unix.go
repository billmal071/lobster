//go:build !windows

package player

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"lobster/internal/userdir"
)

// ipcPrefix names mpv IPC directories within the shared parent.
const ipcPrefix = "mpv"

// socketName is the socket's name inside its own private directory. It is kept
// short because the whole path has to fit in sun_path.
const socketName = "socket"

// shortTempRoot is the last-resort location for the socket: a Unix always has
// /tmp, and a path beneath it is short enough to fit in sun_path whatever
// $HOME and $TMPDIR happen to be.
const shortTempRoot = "/tmp"

// maxSocketPath is the longest usable Unix socket path: sun_path is 104 bytes
// on macOS and the BSDs and 108 on Linux, less one for the terminating NUL.
// Using the smaller bound everywhere costs four characters of headroom on
// Linux and avoids a platform table that would go stale.
const maxSocketPath = 104 - 1

// ipcStaleAfter is how long an IPC directory may go without a sign of life
// before a later run sweeps it.
//
// The sign of life is the ownership marker, refreshed every ipcTouchEvery while
// mpv is running — not the directory's own mtime, which is set when mpv binds
// the socket and does not move again. Without the refresh a playback outlasting
// ipcStaleAfter read as abandoned to a second lobster starting in the meantime,
// which deleted the socket directory under the running player; mpv left paused
// overnight and resumed the next evening is long enough.
//
// Unlinking the socket is in fact survivable — an established Unix connection is
// unaffected, and dialWithRetry has long since given up or succeeded — but that
// argument only covers the one thing lobster happens to do with the path today.
// Keeping the directory alive for as long as it is in use costs one timestamp
// update every six hours and does not have to be re-argued when something else
// needs the path.
const ipcStaleAfter = 24 * time.Hour

// ipcTouchEvery is how often a run refreshes its IPC directory's marker. A
// quarter of the stale window leaves three missed refreshes of slack before a
// second lobster could consider this run abandoned.
//
// It is the same ratio the torrent data directory and subtitle staging use, and
// the constant still belongs to this package: the interval has to outpace
// ipcStaleAfter, which this package chooses, and a shared constant would stop
// leaving that slack the moment one package shortened its own window. A var so a
// test can drive the loop rather than wait out a real interval.
var ipcTouchEvery = ipcStaleAfter / 4

// ipcSocket holds the IPC socket path and cleanup function.
type ipcSocket struct {
	path    string
	cleanup func()
	// sandboxVisible is false when the socket had to be placed in the system
	// temp dir, where a confined mpv cannot reach it.
	sandboxVisible bool
}

// newIPCSocket creates a randomized Unix socket path for mpv IPC.
//
// The path has to be visible to mpv, which is not a given: an mpv packaged as
// a snap runs with a private /tmp, so a --input-ipc-server path under
// os.TempDir() resolves inside mpv's own namespace and the socket it creates
// there does not exist for lobster. The dial then fails for the whole retry
// bound and the watch is recorded as untracked — no error, resume just never
// works. Staging the socket under $HOME instead, exactly as subtitle files
// are, puts it somewhere both processes name the same file.
func newIPCSocket() (*ipcSocket, error) {
	if dir, visible, err := userdir.Make(ipcPrefix, ipcStaleAfter, socketPathFits); err == nil {
		return socketIn(dir, visible, userdir.Keepalive(dir, ipcTouchEvery)), nil
	} else if !errors.Is(err, userdir.ErrUnusable) {
		return nil, fmt.Errorf("creating directory for mpv socket: %w", err)
	}

	// Every candidate, the system temp dir included, produced a path too long
	// for sun_path — $TMPDIR can be arbitrarily deep, and on macOS it already
	// starts at /var/folders/xx/yy. Try the one root a Unix is guaranteed to
	// have and that is short by construction.
	if dir, err := os.MkdirTemp(shortTempRoot, "lobster-"+ipcPrefix+"-*"); err == nil {
		if socketPathFits(dir) {
			return socketIn(dir, false, nil), nil
		}
		_ = os.RemoveAll(dir)
	}

	// Nothing fits anywhere. Hand over an unbindable path rather than refuse:
	// a socket mpv cannot create costs position tracking, which is what this
	// code did before it started choosing directories at all, whereas an error
	// here propagates out of Play and costs the user their playback.
	dir, err := os.MkdirTemp("", "lobster-"+ipcPrefix+"-*")
	if err != nil {
		return nil, fmt.Errorf("creating directory for mpv socket: %w", err)
	}
	return socketIn(dir, false, nil), nil
}

// socketIn describes the socket lobster will ask mpv to create in dir.
//
// stopAlive ends the liveness refresh and is nil for the two last-resort
// directories above, which are created directly in the system temp dir: nothing
// lobster runs sweeps those, and the system reclaims them on its own, so there
// is no sweep to stay ahead of.
func socketIn(dir string, visible bool, stopAlive func()) *ipcSocket {
	return &ipcSocket{
		path: filepath.Join(dir, socketName),
		cleanup: func() {
			if stopAlive != nil {
				stopAlive()
			}
			userdir.Remove(dir)
		},
		sandboxVisible: visible,
	}
}

// socketPathFits reports whether a socket inside dir would still address:
// bind and connect both fail outright on a path too long for sun_path, so a
// deeply nested home has to fall back rather than produce a socket nobody can
// reach.
func socketPathFits(dir string) bool {
	return len(filepath.Join(dir, socketName)) <= maxSocketPath
}

// dial connects to the mpv IPC socket.
func (s *ipcSocket) dial() (io.ReadWriteCloser, error) {
	return net.Dial("unix", s.path)
}

// ipcSandboxHint explains a dial failure that the socket's location would
// cause, and says nothing when the socket was somewhere mpv could have reached.
func ipcSandboxHint(s *ipcSocket) string {
	if !socketPathFits(filepath.Dir(s.path)) {
		return " (no directory short enough for the socket path limit was available," +
			" so mpv could not create the socket at all; position tracking needs a" +
			" shorter $HOME or $TMPDIR)"
	}
	if s.sandboxVisible {
		return ""
	}
	return " (no directory under $HOME was usable, so the socket is in the system" +
		" temp dir; an mpv packaged as a snap or flatpak has its own private /tmp" +
		" and cannot see it there)"
}
