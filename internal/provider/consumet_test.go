package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/media"
)

// newSearchTestServer creates an httptest.Server serving search results.
func newSearchTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/movies/flixhq/", func(w http.ResponseWriter, r *http.Request) {
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

func TestConsumetSearch(t *testing.T) {
	srv := newSearchTestServer(t)
	c := NewConsumet(srv.URL)
	c.client = srv.Client()

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
