package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// newTestServer creates an httptest.Server that routes based on path/query.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Search and info share the /movies/flixhq/ prefix.
	mux.HandleFunc("/movies/flixhq/", func(w http.ResponseWriter, r *http.Request) {
		// Distinguish info (has ?id=...) vs search.
		if r.URL.Query().Get("id") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(infoJSON))
			return
		}
		// Search response.
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

	// First result: movie
	if results[0].Type != media.Movie {
		t.Errorf("expected Movie for %q, got %v", results[0].Title, results[0].Type)
	}
	if results[0].Year != "2005" {
		t.Errorf("expected year 2005, got %q", results[0].Year)
	}
	if results[0].ID != "movie/watch-batman-19913" {
		t.Errorf("unexpected ID: %q", results[0].ID)
	}

	// Second result: TV
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

	// Season 2 filter
	epS2, err := c.GetEpisodes("tv/watch-breaking-bad-39506", "2")
	if err != nil {
		t.Fatalf("GetEpisodes season 2 error: %v", err)
	}
	if len(epS2) != 1 || epS2[0].ID != "2001" {
		t.Errorf("unexpected season 2 episodes: %+v", epS2)
	}
}
