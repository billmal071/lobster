package provider

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"lobster/internal/media"
	"lobster/internal/tbcpl"
)

func TestTBCPLEmbedSearchUsesTMDB(t *testing.T) {
	srv, _ := newTMDBStub(t)
	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	t.Cleanup(func() { tmdbBaseURL = old })

	p := NewTBCPLEmbed([]tbcpl.Site{
		{Name: "X", URL: "https://x.example/", Category: "movies", Status: "trusted", Enabled: true},
	})
	results, err := p.Search("spiderman")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected TMDB results")
	}
}

func TestTBCPLEmbedFiltersToMovieAnime(t *testing.T) {
	p := NewTBCPLEmbed([]tbcpl.Site{
		{URL: "https://a/", Category: "movies", Enabled: true, Status: "trusted"},
		{URL: "https://b/", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://c/", Category: "anime", Enabled: true, Status: "trusted"},
	})
	if len(p.sites) != 2 {
		t.Fatalf("kept %d sites, want 2 (movies+anime)", len(p.sites))
	}
}

func TestEmbedCandidates(t *testing.T) {
	movie := embedCandidates("https://x.example", "603", 0, 0)
	wantMovie := []string{
		"https://x.example/embed/movie/603",
		"https://x.example/movie/603",
		"https://x.example/e/603",
	}
	if !reflect.DeepEqual(movie, wantMovie) {
		t.Fatalf("movie candidates = %v, want %v", movie, wantMovie)
	}
	tv := embedCandidates("https://x.example", "1399", 2, 5)
	want0 := "https://x.example/embed/tv/1399/2/5"
	if tv[0] != want0 {
		t.Fatalf("tv[0] = %q, want %q", tv[0], want0)
	}
}

func TestResolveIframeURL(t *testing.T) {
	cases := []struct{ page, src, want string }{
		// scheme-relative resolves against the page scheme
		{"https://x.example/embed/movie/42", "//cdn.example/a", "https://cdn.example/a"},
		// root-relative resolves against the origin
		{"https://x.example/embed/movie/42", "/e/abc", "https://x.example/e/abc"},
		// page-relative resolves against the candidate page path, not the origin
		{"https://x.example/embed/movie/42", "player/index.html", "https://x.example/embed/movie/player/index.html"},
		// absolute src is preserved
		{"https://x.example/embed/movie/42", "https://cdn.example/e/abc", "https://cdn.example/e/abc"},
	}
	for _, c := range cases {
		if got := resolveIframeURL(c.page, c.src); got != c.want {
			t.Errorf("resolveIframeURL(%q, %q) = %q, want %q", c.page, c.src, got, c.want)
		}
	}
}

func TestWatchSniffsIframeAndExtracts(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><iframe src="https://megacloud.example/e/abc"></iframe></body></html>`))
	}))
	defer site.Close()

	p := NewTBCPLEmbed([]tbcpl.Site{{Name: "X", URL: site.URL, Category: "movies", Status: "trusted", Enabled: true}})
	var sniffed string
	p.resolve = func(embed, referer string) (*media.Stream, error) {
		sniffed = embed
		return &media.Stream{URL: "https://cdn.example/x.m3u8"}, nil
	}
	stream, err := p.Watch("603", "", "", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if stream.URL == "" {
		t.Fatal("empty stream URL")
	}
	if sniffed != "https://megacloud.example/e/abc" {
		t.Fatalf("sniffed iframe = %q", sniffed)
	}
}

// The watch budget is checked only between sites, so a site entered just under
// the deadline can still run every candidate request to completion. Watch must
// stop issuing requests once the budget is spent, not merely stop starting new
// sites.
func TestWatchBudgetBoundsCandidateRequests(t *testing.T) {
	const perRequest = 80 * time.Millisecond
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perRequest)
		w.Write([]byte(`<html><body>no iframe here</body></html>`))
	}))
	defer site.Close()

	old := embedWatchBudget
	embedWatchBudget = 100 * time.Millisecond
	defer func() { embedWatchBudget = old }()

	var sites []tbcpl.Site
	for i := 0; i < 4; i++ {
		sites = append(sites, tbcpl.Site{Name: "X", URL: site.URL, Category: "movies", Status: "trusted", Enabled: true})
	}
	p := NewTBCPLEmbed(sites)

	start := time.Now()
	if _, err := p.Watch("603", "", "", "1080"); err == nil {
		t.Fatal("expected Watch to fail with no playable embed")
	}
	// Only the one request already in flight when the deadline passes may
	// overrun it. Anything beyond that means the candidate loop ignores it.
	if elapsed := time.Since(start); elapsed > embedWatchBudget+perRequest {
		t.Fatalf("Watch took %v, want the %v budget to bound candidate requests", elapsed, embedWatchBudget)
	}
}

// A schemeless catalog entry parses with an empty Host, which made siteOrigin
// return "://" and every candidate URL invalid — a silent whole-site skip.
func TestSiteOriginSchemeless(t *testing.T) {
	for _, raw := range []string{"site.example/x", "site.example"} {
		if got := siteOrigin(raw); got != "https://site.example" {
			t.Errorf("siteOrigin(%q) = %q, want https://site.example", raw, got)
		}
	}
	if got := siteOrigin("https://site.example/x"); got != "https://site.example" {
		t.Errorf("siteOrigin(absolute) = %q", got)
	}
}
