package dlmanager

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"lobster/internal/dlmanager/store"
)

// A queued download resolves its stream later, often in a different process
// run, so everything the resolver needs to identify the work must come off the
// stored row. Without the year, a same-title franchise entry can resolve to
// the wrong film.
func TestManagerPassesStoredYearToResolver(t *testing.T) {
	data := make([]byte, 1024)
	rand.Read(data)

	mgr, srv, dir := setupTestManager(t, data)

	got := make(chan ResolveRequest, 1)
	mgr.SetResolver(func(req ResolveRequest) (*StreamResult, error) {
		got <- req
		return &StreamResult{URL: srv.URL, StreamType: "http"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	if _, err := mgr.Queue(&store.Download{
		Title:      "Spider-Man",
		MediaTitle: "Spider-Man",
		MediaType:  "movie",
		Year:       "2002",
		MediaID:    "movie/557",
		OutputPath: filepath.Join(dir, "spiderman.mkv"),
		Status:     "queued",
		StreamType: "http",
	}); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	select {
	case req := <-got:
		if req.Year != "2002" {
			t.Errorf("resolver got Year %q, want 2002", req.Year)
		}
		if req.MediaID != "movie/557" {
			t.Errorf("resolver got MediaID %q, want movie/557", req.MediaID)
		}
		if req.Title != "Spider-Man" {
			t.Errorf("resolver got Title %q, want Spider-Man", req.Title)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolver was never called")
	}
}
