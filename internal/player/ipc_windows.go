//go:build windows

package player

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
)

// ipcSocket holds the IPC named pipe path and cleanup function.
type ipcSocket struct {
	path    string
	cleanup func()
}

// newIPCSocket creates a randomized named pipe path for mpv IPC on Windows.
func newIPCSocket() (*ipcSocket, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generating random pipe name: %w", err)
	}
	name := fmt.Sprintf(`\\.\pipe\lobster-mpv-%x`, buf)
	return &ipcSocket{
		path:    name,
		cleanup: func() {}, // Named pipes are cleaned up automatically
	}, nil
}

// dial makes a single attempt to open the mpv IPC named pipe (Windows named
// pipes can be opened as regular files). It must not retry or sleep here:
// retry pacing, the overall deadline, and the stop channel (mpv already
// exited) all live in dialWithRetry, whose 30s bound comfortably covers
// antivirus- or slow-I/O-delayed pipe creation. An earlier in-dial loop slept
// through 30 attempts (~6s) with no way to observe stop, so a dead-on-arrival
// mpv held Play for the whole loop.
func (s *ipcSocket) dial() (io.ReadWriteCloser, error) {
	f, err := os.OpenFile(s.path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening named pipe %s: %w", s.path, err)
	}
	return f, nil
}
