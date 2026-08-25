package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// tmdbMultiFixture mirrors the shape of TMDB's /search/multi response: full
// result objects for every match. The /search/trending typeahead endpoint we
// used before returned only the top 2-3 objects plus HTML <span> name
// suggestions (which every parser skips), so a query like "spiderman" showed a
// single film instead of the whole franchise.
const tmdbMultiFixture = `{"results":[
{"media_type":"movie","id":557,"title":"Spider-Man","release_date":"2002-05-01","poster_path":"/a.jpg"},
{"media_type":"movie","id":558,"title":"Spider-Man 2","release_date":"2004-07-17","poster_path":"/b.jpg"},
{"media_type":"movie","id":559,"title":"Spider-Man 3","release_date":"2007-05-01","poster_path":"/c.jpg"},
{"media_type":"movie","id":634649,"title":"Spider-Man: No Way Home","release_date":"2021-12-15","poster_path":"/d.jpg"},
{"media_type":"tv","id":888,"name":"Spider-Man","first_air_date":"1994-11-19","poster_path":"/e.jpg"},
{"media_type":"person","id":4646747,"name":"Spiderman Dato"},
"<span data-media-type=\"/movie\" data-search-name=\"Spider-Man\">Spider-Man</span>"
]}`

// newTMDBStub serves the multi-search fixture and records which path was hit.
func newTMDBStub(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/search/multi" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tmdbMultiFixture))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath
}

func TestTMDBBackedSearchUsesMultiEndpoint(t *testing.T) {
	srv, gotPath := newTMDBStub(t)

	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	t.Cleanup(func() { tmdbBaseURL = old })

	tbcpl := NewTBCPL("tbcpl")
	tbcpl.tmdbBaseURL = srv.URL

	cases := []struct {
		name string
		p    Provider
	}{
		{"soap2day", NewSoap2Day()},
		{"vidnest", NewVidNest()},
		{"vaplayer", NewVaPlayer()},
		{"tbcpl", tbcpl},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := tc.p.Search("spiderman")
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if *gotPath != "/search/multi" {
				t.Errorf("hit %q, want /search/multi", *gotPath)
			}
			// 5 movie/tv entries; the person and the <span> suggestion are dropped.
			if len(results) != 5 {
				t.Fatalf("got %d results, want 5: %+v", len(results), results)
			}
			if results[0].Title != "Spider-Man" || results[0].Year != "2002" {
				t.Errorf("first result = %q (%s), want Spider-Man (2002)", results[0].Title, results[0].Year)
			}
			if results[3].Title != "Spider-Man: No Way Home" {
				t.Errorf("fourth result = %q, want Spider-Man: No Way Home", results[3].Title)
			}
		})
	}
}
