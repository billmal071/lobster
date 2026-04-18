//go:build !windows

package player

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// ipcSocket holds the IPC socket path and cleanup function.
type ipcSocket struct {
	path    string
	cleanup func()
}

// newIPCSocket creates a randomized Unix socket path for mpv IPC.
func newIPCSocket() (*ipcSocket, error) {
	socketDir, err := os.MkdirTemp("", "lobster-mpv-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir for mpv socket: %w", err)
	}
	return &ipcSocket{
		path:    filepath.Join(socketDir, "socket"),
		cleanup: func() { os.RemoveAll(socketDir) },
	}, nil
}

// dial connects to the mpv IPC socket.
func (s *ipcSocket) dial() (io.ReadWriteCloser, error) {
	return net.Dial("unix", s.path)
}
