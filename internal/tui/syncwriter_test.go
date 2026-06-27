package tui

import (
	"bytes"
	"sync"
	"testing"
)

// recordingWriter records each Write call's bytes as a separate chunk.
type recordingWriter struct {
	mu     sync.Mutex
	chunks [][]byte
}

func (r *recordingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := append([]byte(nil), p...)
	r.chunks = append(r.chunks, cp)
	return len(p), nil
}

func TestSyncWriterNoInterleave(t *testing.T) {
	rec := &recordingWriter{}
	sw := newSyncWriter(rec)

	blockA := bytes.Repeat([]byte("A"), 64)
	blockB := bytes.Repeat([]byte("B"), 64)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); sw.Write(blockA) }()
		go func() { defer wg.Done(); sw.Write(blockB) }()
	}
	wg.Wait()

	if len(rec.chunks) != 200 {
		t.Fatalf("got %d chunks want 200", len(rec.chunks))
	}
	for i, c := range rec.chunks {
		if !bytes.Equal(c, blockA) && !bytes.Equal(c, blockB) {
			t.Fatalf("chunk %d interleaved: %q", i, c)
		}
	}
}
