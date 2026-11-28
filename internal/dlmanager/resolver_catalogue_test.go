package dlmanager

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"lobster/internal/dlmanager/store"
)

func TestManagerTellsTheResolverAQueuedRowCameFromTheCatalogue(t *testing.T) {
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
		Title:         "Some Show S01E01",
		MediaTitle:    "Some Show",
		MediaType:     "tv",
		MediaID:       "tv/37854",
		FromCatalogue: true,
		OutputPath:    filepath.Join(dir, "some-show.mkv"),
		Status:        "queued",
		StreamType:    "http",
	}); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	select {
	case req := <-got:
		if !req.FromCatalogue {
			t.Errorf("resolver got %+v, want FromCatalogue", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolver was never called")
	}
}
