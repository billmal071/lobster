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

func (f *fakeSP) Search(string) ([]media.SearchResult, error)            { return f.searchResults, f.searchErr }
func (f *fakeSP) GetDetails(string) (*media.ContentDetail, error)        { return nil, nil }
func (f *fakeSP) GetSeasons(string) ([]media.Season, error)              { return nil, nil }
func (f *fakeSP) GetEpisodes(string, string) ([]media.Episode, error)    { return nil, nil }
func (f *fakeSP) GetServers(string, string) ([]media.Server, error)      { return nil, nil }
func (f *fakeSP) GetEmbedURL(string) (string, error)                     { return "", nil }
func (f *fakeSP) Trending(media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (f *fakeSP) Recent(media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
func (f *fakeSP) Watch(string, string, string, string) (*media.Stream, error) {
	return f.stream, f.watchErr
}

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
	// probe no longer records health itself; the result it returns carries the
	// outcome, and Resolve records it on the receive side.
	if res.Provider != "flakySP" {
		t.Fatalf("expected provider name flakySP, got %q", res.Provider)
	}
}

type episodeIDRecorder struct {
	*fakeSP
	got string
}

func (r *episodeIDRecorder) Watch(_, episodeID, _, _ string) (*media.Stream, error) {
	r.got = episodeID
	return r.stream, nil
}

func TestNyaaSequelCandidateGetsItsSeasonWhenTheTitleNamesIt(t *testing.T) {
	for _, tc := range []struct {
		name, title, want string
	}{
		{"title names the season", "X Season 3", "nyaa/X?s=3:3:12"},
		{"real season one request keeps season one", "X", "nyaa/X?s=3:1:12"},
		{"title names another season", "X Season 2", "nyaa/X?s=3:1:12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &episodeIDRecorder{fakeSP: &fakeSP{
				searchResults: []media.SearchResult{{ID: "nyaa/X?s=3", Title: tc.title, Type: media.TV}},
				stream:        &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"},
			}}
			_, _, err := resolveWithProvider(r, Request{Title: tc.title, MediaType: media.TV, Season: 1, Episode: 12}, func(string, ...any) {})
			if err != nil {
				t.Fatal(err)
			}
			if r.got != tc.want {
				t.Fatalf("episode ID = %q, want %q", r.got, tc.want)
			}
		})
	}
}

func TestSequelSeasonTitleForms(t *testing.T) {
	for _, tc := range []struct {
		name, title, id string
		want            int
	}{
		{"plain", "One Punch Man Season 3", "nyaa/One-Punch+Man?s=3", 3},
		{"zero padded", "One Punch Man Season 03", "nyaa/One-Punch+Man?s=3", 3},
		{"parenthesised", "One Punch Man (Season 3)", "nyaa/One-Punch+Man?s=3", 3},
		{"different show base", "Other Season 3", "nyaa/One-Punch+Man?s=3", 1},
		{"season mismatch", "One Punch Man Season 2", "nyaa/One-Punch+Man?s=3", 1},
		{"ordinal form not handled", "One Punch Man 3rd Season", "nyaa/One-Punch+Man?s=3", 1},
		{"part form not handled", "One Punch Man Season 2 Part 2", "nyaa/One-Punch+Man?s=2", 1},
		{"later season untouched", "One Punch Man Season 3", "nyaa/One-Punch+Man?s=3", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sequelSeason(tc.title, tc.id, 1); got != tc.want {
				t.Fatalf("sequelSeason(%q, %q, 1) = %d, want %d", tc.title, tc.id, got, tc.want)
			}
		})
	}
	if got := sequelSeason("One Punch Man Season 3", "nyaa/One-Punch+Man?s=3", 2); got != 2 {
		t.Fatalf("season 2 request remapped to %d", got)
	}
}
