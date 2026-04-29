//go:build windows

package player

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"time"
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

// dial connects to the mpv IPC named pipe.
// Windows named pipes can be opened as regular files.
func (s *ipcSocket) dial() (io.ReadWriteCloser, error) {
	var f *os.File
	var err error
	// Retry as mpv may not have created the pipe yet.
	// Use a longer timeout on Windows where antivirus and slower I/O can delay pipe creation.
	for i := 0; i < 30; i++ {
		f, err = os.OpenFile(s.path, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("opening named pipe %s: %w", s.path, err)
}
