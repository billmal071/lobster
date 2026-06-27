package resolver

import (
	"errors"
	"net/http"
	"testing"
	"time"

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

// flakySP fails the first Watch with a transient error, then succeeds.
type flakySP struct {
	*fakeSP
	failFirst bool
	stream    *media.Stream
	calls     *int
}

func (s *flakySP) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie}}, nil
}
func (s *flakySP) Watch(a, b, c, d string) (*media.Stream, error) {
	*s.calls++
	if s.failFirst && *s.calls == 1 {
		return nil, errors.New("connection reset by peer")
	}
	return s.stream, nil
}

func TestProbeRetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	f := &fakeSP{} // override Search via a closure-backed fake
	fr := &flakySP{fakeSP: f, failFirst: true, stream: &media.Stream{URL: "https://cdn/x.m3u8"}, calls: &calls}
	r := &Resolver{health: NewHealthStore(), client: http.DefaultClient, attemptTimeout: 2 * time.Second, log: func(string, ...any) {}, validate: false}
	res := r.probe(fr, Request{Title: "Foo", MediaType: media.Movie})
	if res.Err != nil || res.Stream == nil {
		t.Fatalf("expected success after retry, got %+v", res)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts (1 transient retry), got %d", calls)
	}
	if r.health.records["flakySP"] == nil {
		t.Fatal("probe did not record health")
	}
}
