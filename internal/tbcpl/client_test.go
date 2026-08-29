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

func TestLoadMergedOverlaysRegion(t *testing.T) {
	globalJSON := sampleJSON
	regionJSON := `{"categories":[{"id":"movies","name":"Movies & Shows","sites":[
		{"name":"RegionOnly","url":"https://regiononly.example/","enabled":true,"status":"trusted"}]}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/links.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(globalJSON)) })
	mux.HandleFunc("/Region-Links/links.INDIA.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(regionJSON)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ob, orf := baseURL, regionURLFmt
	baseURL = srv.URL + "/links.json"
	regionURLFmt = srv.URL + "/Region-Links/links.%s.json"
	defer func() { baseURL, regionURLFmt = ob, orf }()

	cl := NewClient(filepath.Join(t.TempDir(), "c.json"), time.Hour, nil)
	cat := cl.LoadMerged(context.Background(), "INDIA")
	found := false
	for _, s := range cat.Sites {
		if s.URL == "https://regiononly.example/" {
			found = true
		}
	}
	if !found {
		t.Fatal("region-only site not merged")
	}
	if len(cat.Sites) <= 4 {
		t.Fatalf("expected globals + region, got %d", len(cat.Sites))
	}
}
