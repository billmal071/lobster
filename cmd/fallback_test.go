package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func TestIsPermanentProviderError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("lookup kimcartoon.li: no such host"), true},
		{errors.New("unexpected status 522 from flixhq.to"), true},
		{errors.New("unexpected status 503"), true},
		{errors.New("connection refused"), true},
		{errors.New("search failed: connection timed out"), false},
		{errors.New("no embed ID in video URL"), false},
		{errors.New("moviebox watch: unexpected status 407"), false},
	}
	for _, tt := range tests {
		if got := isPermanentProviderError(tt.err); got != tt.want {
			t.Fatalf("isPermanentProviderError(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

type stubSearchProvider struct {
	search func(string) ([]media.SearchResult, error)
}

func (s *stubSearchProvider) Search(query string) ([]media.SearchResult, error) {
	return s.search(query)
}

func (s *stubSearchProvider) GetDetails(id string) (*media.ContentDetail, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) GetSeasons(id string) ([]media.Season, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) GetServers(id, episodeID string) ([]media.Server, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSearchProvider) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestTryFallbackStreamSkipsProviders(t *testing.T) {
	dnsErr := errors.New("search failed: lookup dead.example: no such host")
	primary := &stubSearchProvider{
		search: func(string) ([]media.SearchResult, error) { return nil, dnsErr },
	}

	permanentSkip := make(map[string]bool)
	skip := map[string]bool{"Soap2Day": true}

	_, _, err := tryFallbackStream(primary, "Test Show", media.TV, 1, 1, skip, permanentSkip)
	if err == nil {
		t.Fatal("expected resolve error")
	}

	name := providerDisplayName(primary)
	if !permanentSkip[name] {
		t.Fatalf("expected primary %q in permanentSkip after DNS error, got %v", name, permanentSkip)
	}
}

func TestIsEmbedPrimary(t *testing.T) {
	if !isEmbedPrimary(provider.NewFlixHQWS("flixhq.ws")) {
		t.Fatal("FlixHQWS should be embed primary")
	}
	if isEmbedPrimary(provider.NewMovieBox()) {
		t.Fatal("MovieBox should not be embed primary")
	}
}

func TestFallbackCandidatesPreferSameType(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/1", Title: "Movie", Type: media.Movie},
		{ID: "tv/1", Title: "Series", Type: media.TV},
		{ID: "tv/2", Title: "Series Alt", Type: media.TV},
	}

	got := fallbackCandidates(results, media.TV)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != "tv/1" || got[1].ID != "tv/2" {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}

func TestFallbackCandidatesLimitAndDedupe(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/1", Title: "One", Type: media.Movie},
		{ID: "movie/1", Title: "One Duplicate", Type: media.Movie},
		{ID: "movie/2", Title: "Two", Type: media.Movie},
		{ID: "movie/3", Title: "Three", Type: media.Movie},
		{ID: "movie/4", Title: "Four", Type: media.Movie},
		{ID: "movie/5", Title: "Five", Type: media.Movie},
		{ID: "movie/6", Title: "Six", Type: media.Movie},
	}

	got := fallbackCandidates(results, media.Movie)
	if len(got) != maxFallbackCandidates {
		t.Fatalf("len(got) = %d, want %d", len(got), maxFallbackCandidates)
	}
	if got[0].ID != "movie/1" || got[1].ID != "movie/2" || got[4].ID != "movie/5" {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}

func TestMergeSubtitlesDedupesByURL(t *testing.T) {
	embedded := []media.Subtitle{
		{Language: "English", Label: "English", URL: "https://subs.example/en.vtt"},
	}
	external := []media.Subtitle{
		{Language: "English", Label: "English Duplicate", URL: "https://subs.example/en.vtt"},
		{Language: "English", Label: "English SDH", URL: "subdl:https://subs.example/en-sdh.zip"},
	}

	got := mergeSubtitles(embedded, external)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Label != "English" || got[1].Label != "English SDH" {
		t.Fatalf("unexpected subtitles: %+v", got)
	}
}
