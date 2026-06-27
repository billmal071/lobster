package tui

import (
	"io"
	"sync"
)

// syncWriter serializes writes so out-of-band image draws never interleave
// with bubbletea's frame writes. All TUI output must flow through one of these.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

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
