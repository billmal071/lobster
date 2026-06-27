package resolver

import (
	"errors"
	"testing"

	"lobster/internal/media"
)

// fakeSP is a minimal StreamProvider for tests.
type fakeSP struct {
	searchResults []media.SearchResult
	searchErr     error
	stream        *media.Stream
	watchErr      error
}

func (f *fakeSP) Search(string) ([]media.SearchResult, error) { return f.searchResults, f.searchErr }
func (f *fakeSP) GetDetails(string) (*media.ContentDetail, error) { return nil, nil }
func (f *fakeSP) GetSeasons(string) ([]media.Season, error)       { return nil, nil }
func (f *fakeSP) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }
func (f *fakeSP) GetServers(string, string) ([]media.Server, error)   { return nil, nil }
func (f *fakeSP) GetEmbedURL(string) (string, error)                  { return "", nil }
func (f *fakeSP) Trending(media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (f *fakeSP) Recent(media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
func (f *fakeSP) Watch(string, string, string, string) (*media.Stream, error) { return f.stream, f.watchErr }

func TestResolveWithProviderSuccess(t *testing.T) {
	f := &fakeSP{
		searchResults: []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie}},
		stream:        &media.Stream{URL: "https://cdn/x.m3u8"},
	}
	req := Request{Title: "Foo", MediaType: media.Movie, Quality: "1080"}
	got, stage, err := resolveWithProvider(f, req, func(string, ...any) {})
	if err != nil || got == nil || got.URL != "https://cdn/x.m3u8" {
		t.Fatalf("got (%v,%q,%v) want stream", got, stage, err)
	}
}

func TestResolveWithProviderSearchFailStage(t *testing.T) {
	f := &fakeSP{searchErr: errors.New("boom")}
	_, stage, err := resolveWithProvider(f, Request{Title: "Foo", MediaType: media.Movie}, func(string, ...any) {})
	if err == nil || stage != "search" {
		t.Fatalf("expected search-stage failure, got stage=%q err=%v", stage, err)
	}
}
