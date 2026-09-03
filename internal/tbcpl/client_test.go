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

// TestLoadRegionsAreNotCrossContaminated verifies that loading one region and
// then another (sequentially, sharing one cachePath) returns each region's own
// catalog — the single global cache file must never serve region-B's data for a
// region-A request.
func TestLoadRegionsAreNotCrossContaminated(t *testing.T) {
	indiaJSON := `{"categories":[{"id":"movies","name":"M","sites":[
		{"name":"IndiaOnly","url":"https://india.example/","enabled":true,"status":"trusted"}]}]}`
	brazilJSON := `{"categories":[{"id":"movies","name":"M","sites":[
		{"name":"BrazilOnly","url":"https://brazil.example/","enabled":true,"status":"trusted"}]}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/Region-Links/links.INDIA.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(indiaJSON)) })
	mux.HandleFunc("/Region-Links/links.BRAZIL.json", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(brazilJSON)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ob, orf := baseURL, regionURLFmt
	baseURL = srv.URL + "/links.json"
	regionURLFmt = srv.URL + "/Region-Links/links.%s.json"
	defer func() { baseURL, regionURLFmt = ob, orf }()

	cache := filepath.Join(t.TempDir(), "tbcpl-cache.json")
	cl := NewClient(cache, time.Hour, func(string, ...any) {})

	india := cl.Load(context.Background(), "INDIA")
	brazil := cl.Load(context.Background(), "BRAZIL")

	has := func(cat *Catalog, url string) bool {
		for _, s := range cat.Sites {
			if s.URL == url {
				return true
			}
		}
		return false
	}
	if !has(india, "https://india.example/") || has(india, "https://brazil.example/") {
		t.Fatalf("INDIA load contaminated: %+v", india.Sites)
	}
	if !has(brazil, "https://brazil.example/") || has(brazil, "https://india.example/") {
		t.Fatalf("BRAZIL load contaminated: %+v", brazil.Sites)
	}
	// Region loads must not have written the shared global cache file.
	if _, err := os.Stat(cache); err == nil {
		t.Fatalf("region load wrote the global cache file; it must not")
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
