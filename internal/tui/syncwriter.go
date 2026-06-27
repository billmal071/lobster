package tui

import (
	"io"
	"sync"

	"github.com/charmbracelet/x/term"
)

// syncWriter serializes writes so out-of-band image draws never interleave
// with bubbletea's frame writes. All TUI output must flow through one of these.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// Compile-time guarantee that *syncWriter satisfies term.File. Bubbletea
// type-asserts its output to term.File to detect the terminal and its size; if
// this assertion ever breaks, the TUI hangs on "Initializing...".
var _ term.File = (*syncWriter)(nil)

func newSyncWriter(w io.Writer) *syncWriter { return &syncWriter{w: w} }

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (s *syncWriter) WriteString(str string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.WriteString(s.w, str)
}

// The methods below let *syncWriter satisfy charmbracelet/x/term.File
// (io.ReadWriteCloser + Fd). Bubbletea type-asserts its output to term.File to
// detect the terminal and query its size; without Fd() it never sends the
// initial WindowSizeMsg and the UI hangs on "Initializing...". We delegate to
// the wrapped writer when it is a real terminal file, and degrade safely
// otherwise (e.g. in tests).

// Fd returns the underlying file descriptor, or an invalid one when the wrapped
// writer is not a file (so term.IsTerminal reports false).
func (s *syncWriter) Fd() uintptr {
	if f, ok := s.w.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}

// Read delegates to the wrapped writer when it is also a reader. Bubbletea
// never reads from its output, so this is here only to satisfy term.File.
func (s *syncWriter) Read(p []byte) (int, error) {
	if r, ok := s.w.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, io.EOF
}

// Close is a no-op: closing the shared stdout would be harmful, and bubbletea
// restores terminal state via Fd() rather than Close().
func (s *syncWriter) Close() error { return nil }
