package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// gatherStubProvider is a minimal Provider whose Search returns canned results/error.
type gatherStubProvider struct {
	name    string
	results []media.SearchResult
	err     error
}

func (s gatherStubProvider) Search(string) ([]media.SearchResult, error) { return s.results, s.err }
func (s gatherStubProvider) GetDetails(string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (s gatherStubProvider) GetSeasons(string) ([]media.Season, error)         { return nil, nil }
func (s gatherStubProvider) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }
func (s gatherStubProvider) GetServers(string, string) ([]media.Server, error) { return nil, nil }
func (s gatherStubProvider) GetEmbedURL(string) (string, error)                { return "", nil }
func (s gatherStubProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (s gatherStubProvider) Recent(media.MediaType) ([]media.SearchResult, error) { return nil, nil }

func TestGatherSearchResultsFallsBackWhenPrimaryErrors(t *testing.T) {
	primary := gatherStubProvider{name: "primary", err: fmt.Errorf("no results found")}
	fb := gatherStubProvider{name: "fb", results: []media.SearchResult{
		{ID: "kam", Title: "KAMUI: He's Behind You", Type: media.TV},
	}}

	results, err := gatherSearchResults(primary, []provider.Provider{fb}, "KAMUI: He's Behind You")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected fallback results, got none")
	}
}

func TestGatherSearchResultsErrorsWhenNothingFound(t *testing.T) {
	primary := gatherStubProvider{err: fmt.Errorf("no results")}
	fb := gatherStubProvider{err: fmt.Errorf("no results")}
	if _, err := gatherSearchResults(primary, []provider.Provider{fb}, "zzz"); err == nil {
		t.Fatal("expected error when no provider has results")
	}
}

// flakyProvider succeeds on the first Search and fails on every one after,
// mimicking a provider that rate-limits or drops a connection.
type flakyProvider struct {
	gatherStubProvider
	calls int
}

func (f *flakyProvider) Search(q string) ([]media.SearchResult, error) {
	f.calls++
	if f.calls == 1 {
		return f.results, nil
	}
	return nil, fmt.Errorf("rate limited")
}

// A thin but successful primary result set must survive the broadening pass.
// Re-querying the primary and replacing the originals on a transient failure
// silently dropped the only results the user was going to see.
func TestGatherSearchResultsKeepsInitialPrimaryResults(t *testing.T) {
	primary := &flakyProvider{gatherStubProvider: gatherStubProvider{results: []media.SearchResult{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002"},
	}}}
	fb := gatherStubProvider{results: []media.SearchResult{
		{ID: "movie/558", Title: "Spider-Man 2", Type: media.Movie, Year: "2004"},
	}}

	results, err := gatherSearchResults(primary, []provider.Provider{fb}, "spider-man")
	if err != nil {
		t.Fatalf("gatherSearchResults: %v", err)
	}
	if primary.calls != 1 {
		t.Errorf("primary searched %d times, want 1", primary.calls)
	}
	var found bool
	for _, r := range results {
		if r.ID == "movie/557" {
			found = true
		}
	}
	if !found {
		t.Fatalf("initial primary result was dropped: %+v", results)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2: %+v", len(results), results)
	}
}
