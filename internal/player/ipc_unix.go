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

// ipcStaleAfter is how long an abandoned IPC directory is kept before a later
// run sweeps it. Deleting one under a running mpv is not observable: lobster
// dials once at startup and an established Unix connection survives the
// socket file being unlinked, so only a dial that has not happened yet could
// fail — and by then mpv has been playing for over a day.
const ipcStaleAfter = 24 * time.Hour

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
		return socketIn(dir, visible), nil
	} else if !errors.Is(err, userdir.ErrUnusable) {
		return nil, fmt.Errorf("creating directory for mpv socket: %w", err)
	}

	// Every candidate, the system temp dir included, produced a path too long
	// for sun_path — $TMPDIR can be arbitrarily deep, and on macOS it already
	// starts at /var/folders/xx/yy. Try the one root a Unix is guaranteed to
	// have and that is short by construction.
	if dir, err := os.MkdirTemp(shortTempRoot, "lobster-"+ipcPrefix+"-*"); err == nil {
		if socketPathFits(dir) {
			return socketIn(dir, false), nil
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
	return socketIn(dir, false), nil
}

// socketIn describes the socket lobster will ask mpv to create in dir.
func socketIn(dir string, visible bool) *ipcSocket {
	return &ipcSocket{
		path:           filepath.Join(dir, socketName),
		cleanup:        func() { userdir.Remove(dir) },
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
