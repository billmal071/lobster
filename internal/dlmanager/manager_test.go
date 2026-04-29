package dlmanager

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

	"lobster/internal/dlmanager/engine"
	"lobster/internal/dlmanager/store"
)

func setupTestManager(t *testing.T, data []byte) (*Manager, *httptest.Server, string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	dir := t.TempDir()

	httpEng := &engine.HTTPEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	hlsEng := &engine.HLSEngine{Client: srv.Client(), Store: s, RetryDelay: 1 * time.Millisecond}

	mgr := New(s, httpEng, hlsEng, 2)
	mgr.SetPollRate(10 * time.Millisecond)

	return mgr, srv, dir
}

func TestManagerQueueAndProcess(t *testing.T) {
	data := make([]byte, 8*1024)
	rand.Read(data)

	mgr, srv, dir := setupTestManager(t, data)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)

	out := filepath.Join(dir, "test.mkv")
	dl := &store.Download{
		Title:      "Test Movie",
		MediaTitle: "Test",
		MediaType:  "movie",
		StreamURL:  srv.URL,
		StreamType: "http",
		OutputPath: out,
		Status:     "queued",
	}

	id, err := mgr.Queue(dl)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	// Wait for completion.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case p := <-mgr.Progress():
			if p.DownloadID == id && p.Status == "completed" {
				// Verify file exists.
				info, err := os.Stat(out)
				if err != nil {
					t.Fatalf("output missing: %v", err)
				}
				if info.Size() != int64(len(data)) {
					t.Errorf("size: got %d, want %d", info.Size(), len(data))
				}
				cancel()
				mgr.wg.Wait()
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for completion")
		}
	}
}

func TestManagerPause(t *testing.T) {
	// Use a server that blocks to give us time to pause.
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		chunk := make([]byte, 4096)
		w.Write(chunk)
		w.(http.Flusher).Flush()
		select {
		case <-started:
		default:
			close(started)
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer s.Close()

	dir := t.TempDir()
	httpEng := &engine.HTTPEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	hlsEng := &engine.HLSEngine{Client: srv.Client(), Store: s, RetryDelay: 1 * time.Millisecond}
	mgr := New(s, httpEng, hlsEng, 1)
	mgr.SetPollRate(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	out := filepath.Join(dir, "test.mkv")
	id, qErr := mgr.Queue(&store.Download{
		Title:      "Test",
		MediaTitle: "Test",
		MediaType:  "movie",
		StreamURL:  srv.URL,
		StreamType: "http",
		OutputPath: out,
		Status:     "queued",
	})
	if qErr != nil {
		t.Fatalf("queue: %v", qErr)
	}

	// Wait for the download to start (server receives request).
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		// Debug: check what's in the store.
		dl, _ := s.GetDownload(id)
		t.Fatalf("timed out waiting for download to start; status=%q", dl.Status)
	}

	if err := mgr.Pause(id); err != nil {
		t.Fatalf("pause: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	dl, _ := s.GetDownload(id)
	if dl.Status != "paused" {
		t.Errorf("status: got %q, want %q", dl.Status, "paused")
	}

	cancel()
	mgr.wg.Wait()
}

func TestManagerResume(t *testing.T) {
	data := make([]byte, 4*1024)
	rand.Read(data)

	mgr, srv, dir := setupTestManager(t, data)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)

	out := filepath.Join(dir, "test.mkv")
	id, _ := mgr.Queue(&store.Download{
		Title:      "Test",
		MediaTitle: "Test",
		MediaType:  "movie",
		StreamURL:  srv.URL,
		StreamType: "http",
		OutputPath: out,
		Status:     "paused",
	})

	// Resume it.
	if err := mgr.Resume(id); err != nil {
		t.Fatalf("resume: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case p := <-mgr.Progress():
			if p.DownloadID == id && p.Status == "completed" {
				cancel()
				mgr.wg.Wait()
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for resume completion")
		}
	}
}

func TestManagerConcurrency(t *testing.T) {
	// Track concurrent downloads.
	var concurrent int32
	var maxConcurrent int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&concurrent, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // Simulate work.
		atomic.AddInt32(&concurrent, -1)

		data := make([]byte, 1024)
		w.Header().Set("Content-Length", "1024")
		w.Write(data)
	}))
	defer srv.Close()

	s, _ := store.Open(":memory:")
	defer s.Close()

	dir := t.TempDir()
	httpEng := &engine.HTTPEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	hlsEng := &engine.HLSEngine{Client: srv.Client(), Store: s, RetryDelay: 1 * time.Millisecond}
	mgr := New(s, httpEng, hlsEng, 2)
	mgr.SetPollRate(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// Queue 4 downloads.
	ids := make([]int, 4)
	for i := 0; i < 4; i++ {
		out := filepath.Join(dir, fmt.Sprintf("test%d.mkv", i))
		id, _ := mgr.Queue(&store.Download{
			Title:      fmt.Sprintf("Test %d", i),
			MediaTitle: "Test",
			MediaType:  "movie",
			StreamURL:  srv.URL,
			StreamType: "http",
			OutputPath: out,
			Status:     "queued",
		})
		ids[i] = id
	}

	// Wait for all to complete.
	completed := 0
	deadline := time.After(10 * time.Second)
	for completed < 4 {
		select {
		case p := <-mgr.Progress():
			if p.Status == "completed" {
				completed++
			}
		case <-deadline:
			t.Fatalf("timed out: only %d/4 completed", completed)
		}
	}

	// Max concurrent should be <= 2.
	got := atomic.LoadInt32(&maxConcurrent)
	if got > 2 {
		t.Errorf("max concurrent: got %d, want <= 2", got)
	}

	cancel()
	mgr.wg.Wait()
}

func TestManagerProgressChannel(t *testing.T) {
	data := make([]byte, 4*1024)
	rand.Read(data)

	mgr, srv, dir := setupTestManager(t, data)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)

	out := filepath.Join(dir, "test.mkv")
	id, _ := mgr.Queue(&store.Download{
		Title:      "Test",
		MediaTitle: "Test",
		MediaType:  "movie",
		StreamURL:  srv.URL,
		StreamType: "http",
		OutputPath: out,
		Status:     "queued",
	})

	var gotDownloading, gotCompleted bool
	deadline := time.After(5 * time.Second)
	for !gotCompleted {
		select {
		case p := <-mgr.Progress():
			if p.DownloadID == id {
				if p.Status == "downloading" {
					gotDownloading = true
				}
				if p.Status == "completed" {
					gotCompleted = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for progress")
		}
	}

	if !gotDownloading {
		t.Error("never received downloading status")
	}

	cancel()
	mgr.wg.Wait()
}
