package engine

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPDownload(t *testing.T) {
	// Serve 64KB of random data.
	data := make([]byte, 64*1024)
	rand.Read(data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	e := &HTTPEngine{Client: srv.Client()}
	err := e.Download(context.Background(), srv.URL, out, "", nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("size: got %d, want %d", len(got), len(data))
	}
}

func TestHTTPResume(t *testing.T) {
	data := make([]byte, 64*1024)
	rand.Read(data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			var start int64
			fmt.Sscanf(rangeHeader, "bytes=%d-", &start)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", int64(len(data))-start))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[start:])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")
	partPath := out + ".part"

	// Write first half as .part file.
	half := len(data) / 2
	if err := os.WriteFile(partPath, data[:half], 0644); err != nil {
		t.Fatalf("writing part: %v", err)
	}

	e := &HTTPEngine{Client: srv.Client()}
	err := e.Resume(context.Background(), srv.URL, out, "", nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if len(got) != len(data) {
		t.Errorf("size: got %d, want %d", len(got), len(data))
	}

	// Verify content matches.
	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("content mismatch at byte %d", i)
		}
	}
}

func TestHTTPProgress(t *testing.T) {
	data := make([]byte, 64*1024)
	rand.Read(data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		// Write in small chunks to trigger multiple progress calls.
		for i := 0; i < len(data); i += 1024 {
			end := i + 1024
			if end > len(data) {
				end = len(data)
			}
			w.Write(data[i:end])
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	var lastDone int64
	var calls int32
	e := &HTTPEngine{Client: srv.Client()}
	err := e.Download(context.Background(), srv.URL, out, "", func(done, total int64) {
		atomic.AddInt32(&calls, 1)
		if done < atomic.LoadInt64(&lastDone) {
			t.Errorf("progress went backwards: %d < %d", done, lastDone)
		}
		atomic.StoreInt64(&lastDone, done)
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	// At minimum the final report should be called.
	if atomic.LoadInt32(&calls) == 0 {
		t.Error("progress function never called")
	}
}

func TestHTTPCancel(t *testing.T) {
	// Use a server that blocks between chunks so cancellation can interrupt.
	written := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576") // claim 1MB
		// Write 4KB then block forever (let cancellation kill it).
		chunk := make([]byte, 4096)
		rand.Read(chunk)
		w.Write(chunk)
		w.(http.Flusher).Flush()
		close(written)
		// Block until client disconnects.
		<-r.Context().Done()
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-written
		cancel()
	}()

	e := &HTTPEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(ctx, srv.URL, out, "", nil)
	if err == nil {
		t.Fatal("expected error from cancellation")
	}

	// Part file should exist with partial content.
	partPath := out + ".part"
	info, statErr := os.Stat(partPath)
	if statErr != nil {
		t.Fatalf("part file missing: %v", statErr)
	}
	if info.Size() == 0 {
		t.Error("part file is empty")
	}
}

func TestHTTPRetry(t *testing.T) {
	data := make([]byte, 4*1024)
	rand.Read(data)

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	e := &HTTPEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(context.Background(), srv.URL, out, "", nil)
	if err != nil {
		t.Fatalf("download with retries: %v", err)
	}

	got, _ := os.ReadFile(out)
	if len(got) != len(data) {
		t.Errorf("size: got %d, want %d", len(got), len(data))
	}
}

func TestHTTPRangeNotSupported(t *testing.T) {
	data := make([]byte, 8*1024)
	rand.Read(data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore Range header, always serve full content.
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	// Create a .part file to simulate partial download.
	partPath := out + ".part"
	os.WriteFile(partPath, data[:1024], 0644)

	e := &HTTPEngine{Client: srv.Client()}
	err := e.Resume(context.Background(), srv.URL, out, "", nil)
	if err != nil {
		t.Fatalf("resume (no range): %v", err)
	}

	// Should have full file (restarted from beginning).
	got, _ := os.ReadFile(out)
	if len(got) != len(data) {
		t.Errorf("size: got %d, want %d", len(got), len(data))
	}
}
