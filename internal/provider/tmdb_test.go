package provider

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func mkResult(id int, name, mediaType, year, poster string) tmdbSearchResult {
	r := tmdbSearchResult{ID: id, MediaType: mediaType, PosterPath: poster}
	if mediaType == "tv" {
		r.Name = name // TMDB puts the TV title in "name"
		r.FirstAirDate = year + "-01-01"
	} else {
		r.Title = name // ...and the movie title in "title"
		r.ReleaseDate = year + "-01-01"
	}
	return r
}

func TestPickPoster(t *testing.T) {
	office := []tmdbSearchResult{
		mkResult(1, "The Office", "movie", "1996", "/uk.jpg"), // UK short, sorts first
		mkResult(2, "The Office", "tv", "2005", "/us.jpg"),    // the US show
		mkResult(3, "Steve Carell", "person", "", "/p.jpg"),
	}
	cases := []struct {
		name        string
		results     []tmdbSearchResult
		title, year string
		isTV        bool
		want        string
	}{
		{"tv picks US show not results[0]", office, "The Office", "2005", true, "https://image.tmdb.org/t/p/w500/us.jpg"},
		{"movie picks UK short", office, "The Office", "1996", false, "https://image.tmdb.org/t/p/w500/uk.jpg"},
		{"case-insensitive name", office, "the office", "2005", true, "https://image.tmdb.org/t/p/w500/us.jpg"},
		{"year off by one still matches", []tmdbSearchResult{mkResult(1, "Up", "movie", "2009", "/up.jpg")}, "Up", "2008", false, "https://image.tmdb.org/t/p/w500/up.jpg"},
		{"year off by two -> no match", []tmdbSearchResult{mkResult(1, "Up", "movie", "2009", "/up.jpg")}, "Up", "2006", false, ""},
		{"missing input year -> name+type match", []tmdbSearchResult{mkResult(1, "Up", "movie", "2009", "/up.jpg")}, "Up", "", false, "https://image.tmdb.org/t/p/w500/up.jpg"},
		{"no name match -> empty", office, "Severance", "2022", true, ""},
		{"person never matches", []tmdbSearchResult{mkResult(1, "Up", "person", "", "/p.jpg")}, "Up", "", false, ""},
		{"empty poster path -> empty", []tmdbSearchResult{mkResult(1, "Up", "movie", "2009", "")}, "Up", "2009", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickPoster(c.results, c.title, c.year, c.isTV); got != c.want {
				t.Fatalf("pickPoster=%q want %q", got, c.want)
			}
		})
	}
}

func TestTMDBPosterFetchAndMemo(t *testing.T) {
	var calls int32
	body := `{"results":[
		"<span>autocomplete</span>",
		{"media_type":"tv","name":"Naruto","first_air_date":"2002-10-03","poster_path":"/n.jpg"},
		{"media_type":"person","name":"Naruto"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	defer func() { tmdbBaseURL = old }()
	tmdbPosterMemo = sync.Map{} // isolate from other tests

	got := TMDBPoster("Naruto", "2002", true)
	if got != "https://image.tmdb.org/t/p/w500/n.jpg" {
		t.Fatalf("TMDBPoster=%q", got)
	}
	// Memoized: a second identical call must not hit the server.
	_ = TMDBPoster("Naruto", "2002", true)
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Fatalf("expected 1 server call (memoized), got %d", c)
	}
	// Negative result is also cached: prime the miss, then a second identical
	// call must not hit the server again.
	if miss := TMDBPoster("Bleach", "2004", true); miss != "" {
		t.Fatalf("expected miss, got %q", miss)
	}
	afterMiss := atomic.LoadInt32(&calls)
	_ = TMDBPoster("Bleach", "2004", true)
	if c := atomic.LoadInt32(&calls); c != afterMiss {
		t.Fatalf("negative result not memoized: server calls %d -> %d", afterMiss, c)
	}
}

// A non-2xx response (rate-limit/proxy error) must NOT be cached as a miss, so a
// later selection of the same title retries instead of being stuck blank.
func TestTMDBPosterDoesNotCacheNon2xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`)) // parses with results==nil
	}))
	defer srv.Close()

	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	defer func() { tmdbBaseURL = old }()
	tmdbPosterMemo = sync.Map{}

	if got := TMDBPoster("Naruto", "2002", true); got != "" {
		t.Fatalf("expected empty on non-2xx, got %q", got)
	}
	// Not cached: a second call must hit the server again.
	if got := TMDBPoster("Naruto", "2002", true); got != "" {
		t.Fatalf("expected empty on retry, got %q", got)
	}
	if c := atomic.LoadInt32(&calls); c != 2 {
		t.Fatalf("non-2xx must not be cached: expected 2 server calls, got %d", c)
	}
}
