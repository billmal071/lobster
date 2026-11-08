package dlmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lobster/internal/dlmanager/engine"
	"lobster/internal/dlmanager/store"
)

func TestManagerDownloadsWithResolvedUserAgent(t *testing.T) {
	uaSeen := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uaSeen <- r.Header.Get("User-Agent")
		w.Write([]byte("payload"))
	}))
	t.Cleanup(srv.Close)

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mgr := New(s, &engine.HTTPEngine{Client: srv.Client(), RetryDelay: time.Millisecond}, &engine.HLSEngine{Client: srv.Client(), Store: s, RetryDelay: time.Millisecond}, 1, 0)
	mgr.SetPollRate(10 * time.Millisecond)
	mgr.SetResolver(func(ResolveRequest) (*StreamResult, error) {
		return &StreamResult{URL: srv.URL, StreamType: "http", UserAgent: "CdnOnly/9"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	id, err := mgr.Queue(&store.Download{Title: "T", MediaTitle: "T", MediaType: "movie", OutputPath: filepath.Join(t.TempDir(), "t.mkv"), Status: "queued", StreamType: "http"})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case ua := <-uaSeen:
		if ua != "CdnOnly/9" {
			t.Fatalf("User-Agent = %q, want the resolved stream's", ua)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the server")
	}
	var got *store.Download
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got, _ = s.GetDownload(id); got.Status == "completed" {
			break
		}
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.UserAgent != "CdnOnly/9" {
		t.Errorf("stored user_agent = %q", got.UserAgent)
	}
}
