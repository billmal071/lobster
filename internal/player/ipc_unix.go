//go:build !windows

package player

import (
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"lobster/internal/userdir"
)

// ipcPrefix names mpv IPC directories within the shared parent.
const ipcPrefix = "mpv"

// socketName is the socket's name inside its own private directory. It is kept
// short because the whole path has to fit in sun_path.
const socketName = "socket"

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
	dir, visible, err := userdir.Make(ipcPrefix, ipcStaleAfter, socketPathFits)
	if err != nil {
		return nil, fmt.Errorf("creating directory for mpv socket: %w", err)
	}
	return &ipcSocket{
		path:           filepath.Join(dir, socketName),
		cleanup:        func() { userdir.Remove(dir) },
		sandboxVisible: visible,
	}, nil
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

// ipcSandboxHint explains a dial failure that a confined mpv would cause, and
// says nothing when the socket was somewhere mpv could have reached.
func ipcSandboxHint(s *ipcSocket) string {
	if s.sandboxVisible {
		return ""
	}
	return " (no directory under $HOME was usable, so the socket is in the system" +
		" temp dir; an mpv packaged as a snap or flatpak has its own private /tmp" +
		" and cannot see it there)"
}
