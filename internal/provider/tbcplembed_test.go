package provider

import (
	"testing"

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
