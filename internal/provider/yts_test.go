package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
)

func ytsStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"status": "ok", "data": map[string]any{"movie_count": 1, "movies": []any{
			map[string]any{
				"id": 123, "title": "The Amazing Spider-Man", "year": 2012,
				"medium_cover_image": "http://img/cover.jpg",
				"runtime":            136,
				"torrents": []any{
					map[string]any{"hash": "CA10982EF0698923AAAAAAAAAAAAAAAAAAAAAAAA", "quality": "720p", "type": "bluray", "size": "899 MB", "seeds": 75},
					map[string]any{"hash": "7A3736A3B7DB99F5BBBBBBBBBBBBBBBBBBBBBBBB", "quality": "1080p", "type": "bluray", "size": "2.00 GB", "seeds": 100},
					map[string]any{"hash": "57F79B41F6BB7159CCCCCCCCCCCCCCCCCCCCCCCC", "quality": "2160p", "type": "bluray", "size": "6.7 GB", "seeds": 40},
				},
			},
		}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	old := ytsBase
	ytsBase = srv.URL
	t.Cleanup(func() { ytsBase = old })
	return srv
}

// The live movie_details.json returns data.movie as a single object, while
// list_movies.json returns data.movies as a list. Stubbing only the list shape
// hid this: GetServers and Watch both go through movie_details.
func TestYTSMovieDetailsObjectShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"movie": map[string]any{
				"id": 3024, "title": "The Amazing Spider-Man", "year": 2012,
				"torrents": []any{
					map[string]any{"hash": "AAAA000000000000000000000000000000000000", "quality": "1080p", "type": "bluray", "size": "2.00 GB", "seeds": 100},
				},
			},
		}})
	}))
	defer srv.Close()
	old := ytsBase
	ytsBase = srv.URL
	defer func() { ytsBase = old }()

	servers, err := NewYTS().GetServers("yts/3024", "")
	if err != nil {
		t.Fatalf("GetServers on movie_details shape: %v", err)
	}
	if len(servers) != 1 || !strings.Contains(servers[0].Name, "1080p") {
		t.Errorf("bad servers: %+v", servers)
	}
}

func TestYTSSearchMapsResults(t *testing.T) {
	defer ytsStub(t).Close()
	res, err := NewYTS().Search("amazing spider")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results", len(res))
	}
	if res[0].Title != "The Amazing Spider-Man" || res[0].Year != "2012" || res[0].Type != media.Movie {
		t.Errorf("bad mapping: %+v", res[0])
	}
	// The ID has to round-trip back to the API, since Watch re-queries by it.
	if res[0].ID != "yts/123" {
		t.Errorf("ID = %q, want yts/123", res[0].ID)
	}
}

// Seed count is the difference between a stream that plays and one that stalls,
// so it belongs in the name the user picks from.
func TestYTSGetServersListsQualitiesWithSeeds(t *testing.T) {
	defer ytsStub(t).Close()
	servers, err := NewYTS().GetServers("yts/123", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 {
		t.Fatalf("got %d servers: %+v", len(servers), servers)
	}
	// Highest quality first, so the best available is what you get by default.
	if !strings.Contains(servers[0].Name, "2160p") {
		t.Errorf("servers not ordered highest-first: %+v", servers)
	}
	var found string
	for _, sv := range servers {
		if strings.Contains(sv.Name, "720p") {
			found = sv.Name
		}
	}
	if !strings.Contains(found, "75 seeds") || !strings.Contains(found, "899 MB") {
		t.Errorf("720p entry lacks seeds or size: %q", found)
	}
}

func TestYTSWatchBuildsMagnetForRequestedQuality(t *testing.T) {
	defer ytsStub(t).Close()
	st, err := NewYTS().Watch("yts/123", "", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:7A3736A3B7DB99F5") {
		t.Errorf("wrong torrent selected: %s", st.URL)
	}
	if !strings.Contains(st.URL, "&tr=") {
		t.Error("magnet has no trackers; a trackerless magnet relies on DHT alone and often never starts")
	}
}

// "best" must reach 2160p — this is the case the numeric qualities cannot
// express, and the whole reason it exists.
func TestYTSWatchBestPicksHighestQuality(t *testing.T) {
	defer ytsStub(t).Close()
	st, err := NewYTS().Watch("yts/123", "", "", "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:57F79B41F6BB7159") {
		t.Errorf("best did not pick 2160p: %s", st.URL)
	}
}

// An unavailable quality must fall back rather than fail: YTS does not carry
// every rung for every film.
func TestYTSWatchMissingQualityFallsBack(t *testing.T) {
	defer ytsStub(t).Close()
	st, err := NewYTS().Watch("yts/123", "", "", "480")
	if err != nil {
		t.Fatalf("480 not stocked should fall back, got error: %v", err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:") {
		t.Errorf("no magnet returned: %s", st.URL)
	}
}

func TestYTSWatchExplicitServerWins(t *testing.T) {
	defer ytsStub(t).Close()
	servers, _ := NewYTS().GetServers("yts/123", "")
	// servers[0] is 2160p (highest-first); asking for quality 720 as well proves
	// the explicit choice beats the quality preference rather than merging with it.
	st, err := NewYTS().Watch("yts/123", "", servers[0].Name, "720")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:57F79B41F6BB7159") {
		t.Errorf("explicit server ignored: %s", st.URL)
	}
}
