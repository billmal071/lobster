package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/media"
)

func TestParseSearchResultsWithSeries(t *testing.T) {
	// Verify that parseSearchResults detects /series/ URLs as TV type
	doc := loadTestDoc(t, "ws_search_series.html")
	results := parseSearchResults(doc)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Type != media.Movie {
		t.Errorf("result[0].Type = %v, want Movie", results[0].Type)
	}
	if results[0].ID != "movie/free-the-exorcist-hd-75043" {
		t.Errorf("result[0].ID = %q, want 'movie/free-the-exorcist-hd-75043'", results[0].ID)
	}

	if results[1].Title != "Breaking Bad" {
		t.Errorf("result[1].Title = %q, want 'Breaking Bad'", results[1].Title)
	}
	if results[1].Type != media.TV {
		t.Errorf("result[1].Type = %v, want TV (via /series/ URL)", results[1].Type)
	}
	if results[1].ID != "series/watch-breaking-bad-39516" {
		t.Errorf("result[1].ID = %q, want 'series/watch-breaking-bad-39516'", results[1].ID)
	}
	if results[1].Seasons != 5 {
		t.Errorf("result[1].Seasons = %d, want 5", results[1].Seasons)
	}
	if results[1].Episodes != 62 {
		t.Errorf("result[1].Episodes = %d, want 62", results[1].Episodes)
	}
}

func TestParseWSSeasons(t *testing.T) {
	doc := loadTestDoc(t, "ws_seasons.html")
	seasons := parseWSSeason(doc)

	if len(seasons) != 3 {
		t.Fatalf("expected 3 seasons, got %d", len(seasons))
	}

	tests := []struct {
		idx    int
		number int
		id     string
	}{
		{0, 1, "abc123hash"},
		{1, 2, "def456hash"},
		{2, 3, "ghi789hash"},
	}

	for _, tt := range tests {
		if seasons[tt.idx].Number != tt.number {
			t.Errorf("seasons[%d].Number = %d, want %d", tt.idx, seasons[tt.idx].Number, tt.number)
		}
		if seasons[tt.idx].ID != tt.id {
			t.Errorf("seasons[%d].ID = %q, want %q", tt.idx, seasons[tt.idx].ID, tt.id)
		}
	}
}

func TestParseWSEpisodes(t *testing.T) {
	doc := loadTestDoc(t, "ws_episodes.html")
	episodes := parseWSEpisodes(doc)

	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(episodes))
	}

	if episodes[0].Number != 1 {
		t.Errorf("episodes[0].Number = %d, want 1", episodes[0].Number)
	}
	if episodes[0].Title != "Pilot" {
		t.Errorf("episodes[0].Title = %q, want 'Pilot'", episodes[0].Title)
	}
	if episodes[0].ID != "/series/batman-19660/1-1/" {
		t.Errorf("episodes[0].ID = %q, want '/series/batman-19660/1-1/'", episodes[0].ID)
	}

	if episodes[2].Number != 3 {
		t.Errorf("episodes[2].Number = %d, want 3", episodes[2].Number)
	}
	if episodes[2].Title != "Fine Feathered Finks" {
		t.Errorf("episodes[2].Title = %q, want 'Fine Feathered Finks'", episodes[2].Title)
	}
}

func TestParseWSServers(t *testing.T) {
	doc := loadTestDoc(t, "ws_servers.html")
	servers := parseWSServers(doc)

	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(servers))
	}

	if servers[0].Name != "Vidcdn" {
		t.Errorf("servers[0].Name = %q, want 'Vidcdn'", servers[0].Name)
	}
	if servers[0].ID != "https://vidcdn.co/iframe/abc123" {
		t.Errorf("servers[0].ID = %q, want 'https://vidcdn.co/iframe/abc123'", servers[0].ID)
	}

	if servers[1].Name != "UpCloud" {
		t.Errorf("servers[1].Name = %q, want 'UpCloud'", servers[1].Name)
	}
	if servers[1].ID != "https://upcloud.to/embed/def456" {
		t.Errorf("servers[1].ID = %q, want 'https://upcloud.to/embed/def456'", servers[1].ID)
	}
}

func TestFlixHQWSGetEmbedURL(t *testing.T) {
	p := NewFlixHQWS("flixhq.ws")

	// GetEmbedURL should return the serverID as-is
	url, err := p.GetEmbedURL("https://vidcdn.co/iframe/abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://vidcdn.co/iframe/abc123" {
		t.Errorf("GetEmbedURL = %q, want 'https://vidcdn.co/iframe/abc123'", url)
	}

	// Empty serverID should error
	_, err = p.GetEmbedURL("")
	if err == nil {
		t.Error("expected error for empty serverID")
	}
}

func TestFlixHQWSPlURLExtractionMovie(t *testing.T) {
	doc := loadTestDoc(t, "ws_movie_page.html")
	plURL := ""
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		if matches := plURLPattern.FindStringSubmatch(text); len(matches) > 1 {
			plURL = matches[1]
		}
	})

	if plURL != "https://flixhq.ws/ajax/ajax.php?vdkz=moviehash123" {
		t.Errorf("pl_url = %q, want 'https://flixhq.ws/ajax/ajax.php?vdkz=moviehash123'", plURL)
	}
}

func TestFlixHQWSPlURLExtractionTV(t *testing.T) {
	doc := loadTestDoc(t, "ws_episode_page.html")
	plURL := ""
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		if matches := plURLPattern.FindStringSubmatch(text); len(matches) > 1 {
			plURL = matches[1]
		}
	})

	if plURL != "https://flixhq.ws/ajax/ajax.php?vds=episodehash456" {
		t.Errorf("pl_url = %q, want 'https://flixhq.ws/ajax/ajax.php?vds=episodehash456'", plURL)
	}
}

func TestFlixHQWSGetServersIntegration(t *testing.T) {
	// Integration test using httptest server.
	// The movie page pl_url must point back to our test server.
	mux := http.NewServeMux()

	var tsURL string

	mux.HandleFunc("/movie/free-the-exorcist-hd-75043/", func(w http.ResponseWriter, r *http.Request) {
		// Serve a movie page whose pl_url points back to our test server
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>
const pl_url = '` + tsURL + `/ajax/ajax.php?vdkz=moviehash123';
</script></body></html>`))
	})

	mux.HandleFunc("/ajax/ajax.php", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "testdata/ws_servers.html")
	})

	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	tsURL = ts.URL

	base := strings.TrimPrefix(ts.URL, "https://")
	p := &FlixHQWS{
		base:   base,
		client: ts.Client(),
	}

	servers, err := p.GetServers("movie/free-the-exorcist-hd-75043", "")
	if err != nil {
		t.Fatalf("GetServers error: %v", err)
	}

	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(servers))
	}
	if servers[0].Name != "Vidcdn" {
		t.Errorf("servers[0].Name = %q, want 'Vidcdn'", servers[0].Name)
	}
}

func TestFlixHQWSGetServersTVIntegration(t *testing.T) {
	mux := http.NewServeMux()
	var tsURL string

	mux.HandleFunc("/series/batman-19660/1-3/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>
const pl_url = '` + tsURL + `/ajax/ajax.php?vds=episodehash456';
</script></body></html>`))
	})

	mux.HandleFunc("/ajax/ajax.php", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "testdata/ws_servers.html")
	})

	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	tsURL = ts.URL

	base := strings.TrimPrefix(ts.URL, "https://")
	p := &FlixHQWS{
		base:   base,
		client: ts.Client(),
	}

	servers, err := p.GetServers("series/batman-19660", "/series/batman-19660/1-3/")
	if err != nil {
		t.Fatalf("GetServers error: %v", err)
	}

	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(servers))
	}
}

func newWSTestProvider(t *testing.T, h http.HandlerFunc) *FlixHQWS {
	t.Helper()
	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	return &FlixHQWS{base: strings.TrimPrefix(ts.URL, "https://"), client: ts.Client()}
}

func serveFixture(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		http.ServeFile(w, r, "testdata/"+name)
	}
}

func TestFlixHQWSSearchNoMatchIsNoResults(t *testing.T) {
	p := newWSTestProvider(t, serveFixture("ws_search_noresults.html"))
	_, err := p.Search("xyzzyqq")
	if !errors.Is(err, ErrNoResults) || errors.Is(err, ErrUnrecognisedResponse) {
		t.Fatalf("Search on the site's own no-match page = %v, want ErrNoResults and not ErrUnrecognisedResponse", err)
	}
	if !strings.Contains(err.Error(), "xyzzyqq") {
		t.Errorf("error %q does not name the query", err)
	}
}

func TestFlixHQWSSearchUnrecognisedPages(t *testing.T) {
	cases := map[string]string{
		"empty body": ``,
		"cloudflare challenge": `<!DOCTYPE html><html><head><title>Just a moment...</title></head>` +
			`<body><h1>Checking your browser before accessing flixhq.ws</h1>` +
			`<form id="challenge-form" action="/cdn-cgi/challenge-platform/"><input type="hidden" name="md"></form></body></html>`,
		"truncated before the search form": `<!DOCTYPE html><html lang="en"><head><title>FlixHQ | Watch Free TV Series Online HD</title>` +
			`<link rel="canonical" href="https://flixhq.ws/search/"/><link rel="stylesheet" hr`,
		"truncated after the keyword input": `<!DOCTYPE html><html lang="en"><head><link rel="canonical" href="https://flixhq.ws/search/"/></head>` +
			`<body><div id="header"><div id="search"><form><input type="text" name="keyword" class="form-control search-input">`,
		"canonical alone":    `<html><head><link rel="canonical" href="https://flixhq.ws/search/"/></head><body></body></html>`,
		"keyword form alone": `<html><body><form action="/search"><input name="keyword"></form></body></html>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := newWSTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(body))
			})
			_, err := p.Search("xyzzyqq")
			if !errors.Is(err, ErrUnrecognisedResponse) || errors.Is(err, ErrNoResults) {
				t.Fatalf("Search = %v, want ErrUnrecognisedResponse and not ErrNoResults", err)
			}
		})
	}
}

func TestFlixHQWSSearchEmptyResultContainerIsNoResults(t *testing.T) {
	p := newWSTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div class="film_list-wrap"></div></body></html>`))
	})
	if _, err := p.Search("xyzzyqq"); !errors.Is(err, ErrNoResults) {
		t.Fatalf("Search = %v, want ErrNoResults", err)
	}
}

func TestFlixHQWSSearchHTTPErrorsAreNotNoResults(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		p := newWSTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", code)
		})
		_, err := p.Search("dune")
		if err == nil || errors.Is(err, ErrNoResults) {
			t.Fatalf("status %d: Search = %v, want a non-ErrNoResults error", code, err)
		}
	}
}

func TestFlixHQWSRecentUsesLiveListingPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/movies/", serveFixture("ws_recent_movies.html"))
	mux.HandleFunc("/tv-series/", serveFixture("ws_recent_tv.html"))
	p := newWSTestProvider(t, mux.ServeHTTP)

	movies, err := p.Recent(media.Movie)
	if err != nil {
		t.Fatalf("Recent(Movie) = %v", err)
	}
	if len(movies) != 3 || movies[0].Type != media.Movie || movies[0].ID != "movie/you-me-us-49834" {
		t.Errorf("Recent(Movie) = %+v, want 3 movie cards starting with movie/you-me-us-49834", movies)
	}

	shows, err := p.Recent(media.TV)
	if err != nil {
		t.Fatalf("Recent(TV) = %v", err)
	}
	if len(shows) != 3 || shows[0].Type != media.TV || shows[0].ID != "series/the-body-88253" {
		t.Errorf("Recent(TV) = %+v, want 3 series cards starting with series/the-body-88253", shows)
	}
}

// Ensure FlixHQWS implements the Provider interface at compile time.
var _ Provider = (*FlixHQWS)(nil)

// Suppress unused import warning for goquery used in test helpers
var _ = (*goquery.Selection)(nil)
