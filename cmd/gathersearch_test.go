package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// stubProvider is a minimal Provider whose Search returns canned results/error.
type stubProvider struct {
	name    string
	results []media.SearchResult
	err     error
}

func (s stubProvider) Search(string) ([]media.SearchResult, error) { return s.results, s.err }
func (s stubProvider) GetDetails(string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (s stubProvider) GetSeasons(string) ([]media.Season, error)         { return nil, nil }
func (s stubProvider) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }
func (s stubProvider) GetServers(string, string) ([]media.Server, error) { return nil, nil }
func (s stubProvider) GetEmbedURL(string) (string, error)                { return "", nil }
func (s stubProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (s stubProvider) Recent(media.MediaType) ([]media.SearchResult, error) { return nil, nil }

func TestGatherSearchResultsFallsBackWhenPrimaryErrors(t *testing.T) {
	primary := stubProvider{name: "primary", err: fmt.Errorf("no results found")}
	fb := stubProvider{name: "fb", results: []media.SearchResult{
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
	primary := stubProvider{err: fmt.Errorf("no results")}
	fb := stubProvider{err: fmt.Errorf("no results")}
	if _, err := gatherSearchResults(primary, []provider.Provider{fb}, "zzz"); err == nil {
		t.Fatal("expected error when no provider has results")
	}
}
