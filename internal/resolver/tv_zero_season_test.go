package resolver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// countingSP is a StreamProvider that counts Search and records the episode ID Watch is given.
type countingSP struct {
	*fakeSP
	searches atomic.Int32
	episode  atomic.Value
}

func newCountingSP(id string, typ media.MediaType) *countingSP {
	c := &countingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: id, Title: "Naruto", Type: typ}},
		stream:        &media.Stream{URL: "https://cdn/x.m3u8"},
	}}
	c.episode.Store("unset")
	return c
}

func (c *countingSP) Search(q string) ([]media.SearchResult, error) {
	c.searches.Add(1)
	return c.fakeSP.Search(q)
}

func (c *countingSP) Watch(_, episodeID, _, _ string) (*media.Stream, error) {
	c.episode.Store(episodeID)
	return c.stream, nil
}

// embedOnly is a Provider that is not a StreamProvider (no Watch), so the embed path is taken.
type embedOnly struct {
	searches   atomic.Int32
	serverAsks atomic.Int32
}

func (e *embedOnly) Search(string) ([]media.SearchResult, error) {
	e.searches.Add(1)
	return []media.SearchResult{{ID: "tv/1", Title: "Naruto", Type: media.TV}}, nil
}
func (e *embedOnly) GetDetails(string) (*media.ContentDetail, error)     { return nil, nil }
func (e *embedOnly) GetSeasons(string) ([]media.Season, error)           { return nil, nil }
func (e *embedOnly) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }
func (e *embedOnly) GetEmbedURL(string) (string, error)                  { return "", nil }
func (e *embedOnly) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (e *embedOnly) Recent(media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (e *embedOnly) GetServers(string, string) ([]media.Server, error) {
	e.serverAsks.Add(1)
	return nil, nil
}

// TestResolveRefusesTVWithoutSeasonEpisodeBeforeProbing checks that a TV request with season and episode both 0 is refused with ErrSeasonEpisodeRequired before any provider is searched, asked for servers or scored.
func TestResolveRefusesTVWithoutSeasonEpisodeBeforeProbing(t *testing.T) {
	stream := newCountingSP("tv/31910", media.TV)
	embed := &embedOnly{}
	health := NewHealthStore()
	r := New([]provider.Provider{stream, embed}, health, nil)
	r.validate = false

	got, rep, err := r.Resolve(context.Background(), Request{Title: "Naruto", MediaType: media.TV})
	if got != nil || !errors.Is(err, ErrSeasonEpisodeRequired) {
		t.Fatalf("got (%v, %v), want nil stream and ErrSeasonEpisodeRequired", got, err)
	}
	if rep == nil || len(rep.Attempts) != 0 {
		t.Fatalf("report = %+v, want empty non-nil report", rep)
	}
	if stream.searches.Load() != 0 || embed.searches.Load() != 0 || embed.serverAsks.Load() != 0 {
		t.Fatalf("providers were probed: stream searches %d, embed searches %d, embed servers %d",
			stream.searches.Load(), embed.searches.Load(), embed.serverAsks.Load())
	}
	for _, p := range []provider.Provider{stream, embed} {
		if _, ok := health.records[ProviderName(p)]; ok {
			t.Fatalf("health recorded for %s after a caller mistake", ProviderName(p))
		}
	}
}

// TestResolveTVWithSeasonEpisodeBuildsEpisodeID checks the valid TV forms still resolve: S1E1 builds a numeric episode ID, and a Specials request (season 0, episode > 0) is not mistaken for "unset".
func TestResolveTVWithSeasonEpisodeBuildsEpisodeID(t *testing.T) {
	for _, tc := range []struct {
		season, episode int
		want            string
	}{
		{1, 1, "31910:1:1"},
		{0, 5, "31910:0:5"},
		{2, 0, "31910:2:0"},
	} {
		c := newCountingSP("tv/31910", media.TV)
		r := New([]provider.Provider{c}, NewHealthStore(), nil)
		r.validate = false
		if _, _, err := r.Resolve(context.Background(), Request{Title: "Naruto", MediaType: media.TV, Season: tc.season, Episode: tc.episode}); err != nil {
			t.Fatalf("S%dE%d: %v", tc.season, tc.episode, err)
		}
		if got := c.episode.Load(); got != tc.want {
			t.Errorf("S%dE%d episode ID = %v, want %q", tc.season, tc.episode, got, tc.want)
		}
	}
}

// TestResolveMovieWithoutSeasonEpisodeUsesMoviePath checks that a film is unaffected: empty episode ID, one search.
func TestResolveMovieWithoutSeasonEpisodeUsesMoviePath(t *testing.T) {
	c := newCountingSP("movie/603", media.Movie)
	r := New([]provider.Provider{c}, NewHealthStore(), nil)
	r.validate = false
	if _, _, err := r.Resolve(context.Background(), Request{Title: "Naruto", MediaType: media.Movie}); err != nil {
		t.Fatal(err)
	}
	if got := c.episode.Load(); got != "" {
		t.Fatalf("movie episode ID = %v, want empty", got)
	}
}
