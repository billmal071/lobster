package tbcpl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFetchesAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleJSON))
	}))
	defer srv.Close()
	oldBase := baseURL
	baseURL = srv.URL + "/links.json"
	defer func() { baseURL = oldBase }()

	cache := filepath.Join(t.TempDir(), "tbcpl-cache.json")
	cl := NewClient(cache, time.Hour, func(string, ...any) {})
	cat := cl.Load(context.Background(), "")
	if len(cat.Sites) != 4 {
		t.Fatalf("fetched %d sites, want 4", len(cat.Sites))
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
}

func TestLoadFallsBackToSnapshotOffline(t *testing.T) {
	oldBase := baseURL
	baseURL = "http://127.0.0.1:0/links.json" // unreachable
	defer func() { baseURL = oldBase }()

	cache := filepath.Join(t.TempDir(), "tbcpl-cache.json") // absent
	cl := NewClient(cache, time.Hour, func(string, ...any) {})
	cat := cl.Load(context.Background(), "")
	if len(cat.Sites) == 0 {
		t.Fatal("expected embedded snapshot fallback, got 0 sites")
	}
}
