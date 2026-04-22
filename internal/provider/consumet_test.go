package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
)

// infoJSON is the shared info response used across tests.
const infoJSON = `{
  "id": "tv/watch-breaking-bad-39506",
  "title": "Breaking Bad",
  "description": "A chemistry teacher turns to crime.",
  "type": "TV Series",
  "releaseDate": "2008-01-20",
  "genres": ["Crime", "Drama"],
  "casts": ["Bryan Cranston"],
  "country": "United States",
  "duration": "45 min",
  "rating": 9.5,
  "episodes": [
    {"id": "1001", "title": "Pilot", "number": 1, "season": 1},
    {"id": "1002", "title": "Cat's in the Bag", "number": 2, "season": 1},
    {"id": "2001", "title": "Seven Thirty-Seven", "number": 1, "season": 2}
  ]
}`

// newTestServer creates an httptest.Server routing all consumet endpoints.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Servers endpoint (must be registered before the catch-all below)
	mux.HandleFunc("/movies/flixhq/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "vidcloud", "url": "https://example.com/vidcloud", "id": "vc1"},
			{"name": "upcloud", "url": "https://example.com/upcloud", "id": "uc1"},
		})
	})

	// Watch endpoint
	mux.HandleFunc("/movies/flixhq/watch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sources": []map[string]interface{}{
				{"url": "https://example.com/1080.m3u8", "quality": "1080p", "isM3U8": true},
				{"url": "https://example.com/720.m3u8", "quality": "720p", "isM3U8": true},
			},
			"subtitles": []map[string]string{
				{"url": "https://example.com/en.vtt", "lang": "English"},
			},
		})
	})

	// Search and info share the /movies/flixhq/ catch-all prefix.
	mux.HandleFunc("/movies/flixhq/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "" {
			// Info endpoint
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(infoJSON))
			return
		}
		// Search response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"currentPage": 1,
			"hasNextPage": false,
			"results": []map[string]interface{}{
				{"id": "movie/watch-batman-19913", "title": "Batman Begins", "type": "Movie", "releaseDate": "2005"},
				{"id": "tv/watch-batman-67955", "title": "Batman: TAS", "type": "TV Series", "releaseDate": "1992", "seasons": 4},
			},
		})
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newConsumetForTest(srv *httptest.Server) *Consumet {
	c := NewConsumet(srv.URL)
	c.client = srv.Client()
	return c
}

// ---------------------------------------------------------------------------
// Task 2: Search
// ---------------------------------------------------------------------------

func TestConsumetSearch(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	results, err := c.Search("batman")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Type != media.Movie {
		t.Errorf("expected Movie for %q, got %v", results[0].Title, results[0].Type)
	}
	if results[0].Year != "2005" {
		t.Errorf("expected year 2005, got %q", results[0].Year)
	}
	if results[0].ID != "movie/watch-batman-19913" {
		t.Errorf("unexpected ID: %q", results[0].ID)
	}
	if results[1].Type != media.TV {
		t.Errorf("expected TV for %q, got %v", results[1].Title, results[1].Type)
	}
	if results[1].Seasons != 4 {
		t.Errorf("expected 4 seasons, got %d", results[1].Seasons)
	}
}

// ---------------------------------------------------------------------------
// Task 3: GetDetails, GetSeasons, GetEpisodes
// ---------------------------------------------------------------------------

func TestConsumetGetDetails(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	detail, err := c.GetDetails("tv/watch-breaking-bad-39506")
	if err != nil {
		t.Fatalf("GetDetails returned error: %v", err)
	}

	if detail.Description != "A chemistry teacher turns to crime." {
		t.Errorf("unexpected description: %q", detail.Description)
	}
	if detail.Rating != "9.5" {
		t.Errorf("expected rating 9.5, got %q", detail.Rating)
	}
	if detail.Duration != "45 min" {
		t.Errorf("expected duration '45 min', got %q", detail.Duration)
	}
	if detail.Country != "United States" {
		t.Errorf("unexpected country: %q", detail.Country)
	}
	if len(detail.Genre) != 2 || detail.Genre[0] != "Crime" {
		t.Errorf("unexpected genres: %v", detail.Genre)
	}
	if len(detail.Casts) != 1 || detail.Casts[0] != "Bryan Cranston" {
		t.Errorf("unexpected casts: %v", detail.Casts)
	}
}

func TestConsumetGetSeasons(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	seasons, err := c.GetSeasons("tv/watch-breaking-bad-39506")
	if err != nil {
		t.Fatalf("GetSeasons returned error: %v", err)
	}

	if len(seasons) != 2 {
		t.Fatalf("expected 2 seasons, got %d", len(seasons))
	}
	if seasons[0].Number != 1 || seasons[0].ID != "1" {
		t.Errorf("unexpected season 0: %+v", seasons[0])
	}
	if seasons[1].Number != 2 || seasons[1].ID != "2" {
		t.Errorf("unexpected season 1: %+v", seasons[1])
	}
}

func TestConsumetGetEpisodes(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	episodes, err := c.GetEpisodes("tv/watch-breaking-bad-39506", "1")
	if err != nil {
		t.Fatalf("GetEpisodes returned error: %v", err)
	}

	if len(episodes) != 2 {
		t.Fatalf("expected 2 episodes in season 1, got %d", len(episodes))
	}
	if episodes[0].Title != "Pilot" || episodes[0].ID != "1001" {
		t.Errorf("unexpected episode 0: %+v", episodes[0])
	}
	if episodes[1].Title != "Cat's in the Bag" || episodes[1].ID != "1002" {
		t.Errorf("unexpected episode 1: %+v", episodes[1])
	}

	epS2, err := c.GetEpisodes("tv/watch-breaking-bad-39506", "2")
	if err != nil {
		t.Fatalf("GetEpisodes season 2 error: %v", err)
	}
	if len(epS2) != 1 || epS2[0].ID != "2001" {
		t.Errorf("unexpected season 2 episodes: %+v", epS2)
	}
}

// ---------------------------------------------------------------------------
// Task 4: GetServers, Watch, StreamProvider interface
// ---------------------------------------------------------------------------

func TestConsumetGetServers(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	servers, err := c.GetServers("tv/watch-breaking-bad-39506", "1001")
	if err != nil {
		t.Fatalf("GetServers returned error: %v", err)
	}

	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if servers[0].Name != "vidcloud" || servers[0].ID != "vc1" {
		t.Errorf("unexpected server 0: %+v", servers[0])
	}
}

func TestConsumetWatch_QualityMatch(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	stream, err := c.Watch("tv/watch-breaking-bad-39506", "1001", "vidcloud", "1080")
	if err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	if stream.URL != "https://example.com/1080.m3u8" {
		t.Errorf("expected 1080 stream URL, got %q", stream.URL)
	}
	if stream.Quality != "1080p" {
		t.Errorf("expected quality '1080p', got %q", stream.Quality)
	}
	if len(stream.Subtitles) != 1 || stream.Subtitles[0].Language != "English" {
		t.Errorf("unexpected subtitles: %+v", stream.Subtitles)
	}
}

func TestConsumetWatch_FallbackToFirst(t *testing.T) {
	srv := newTestServer(t)
	c := newConsumetForTest(srv)

	stream, err := c.Watch("tv/watch-breaking-bad-39506", "1001", "vidcloud", "4k")
	if err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	// No 4k source → falls back to first (1080p)
	if stream.URL != "https://example.com/1080.m3u8" {
		t.Errorf("expected fallback to first source, got %q", stream.URL)
	}
}

func TestConsumetGetEmbedURL_NotSupported(t *testing.T) {
	c := NewConsumet("https://example.com")
	_, err := c.GetEmbedURL("someID")
	if err == nil {
		t.Fatal("expected error from GetEmbedURL")
	}
}

func TestConsumetImplementsStreamProvider(t *testing.T) {
	var _ StreamProvider = (*Consumet)(nil)
}

// ---------------------------------------------------------------------------
// Task 5: Trending / Recent not supported
// ---------------------------------------------------------------------------

func TestConsumetTrending_NotSupported(t *testing.T) {
	c := NewConsumet("https://example.com")
	_, err := c.Trending(media.Movie)
	if err == nil {
		t.Fatal("expected error from Trending")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error should mention 'not supported', got: %v", err)
	}
}

func TestConsumetRecent_NotSupported(t *testing.T) {
	c := NewConsumet("https://example.com")
	_, err := c.Recent(media.TV)
	if err == nil {
		t.Fatal("expected error from Recent")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error should mention 'not supported', got: %v", err)
	}
}
