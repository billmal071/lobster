package tui

import (
	"errors"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// listlessProvider enumerates seasons but cannot list episodes — the shape of
// MovieBox and VidNest since they stopped inventing lists.
type listlessProvider struct{ provider.Provider }

func (listlessProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, errors.New("episode listing unavailable")
}

// The download dialog showed the provider's error where another provider had
// the list, so a show under such a primary could not be downloaded from the
// TUI at all. It must consult the fallback hook before giving up.
func TestFetchEpisodesUsesTheFallbackWhenTheProviderCannotList(t *testing.T) {
	prev := EpisodeListFallback
	var askedSeason int
	EpisodeListFallback = func(_ provider.Provider, _ media.SearchResult, seasonNumber int) ([]media.Episode, error) {
		askedSeason = seasonNumber
		return []media.Episode{{ID: "f1e1", Number: 1}, {ID: "f1e2", Number: 2}}, nil
	}
	t.Cleanup(func() { EpisodeListFallback = prev })

	d := &downloadDialog{
		item:     media.SearchResult{ID: "tv/1403", Title: "Some Show", Type: media.TV},
		provider: listlessProvider{},
		seasons:  []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
	}

	msg, ok := d.fetchEpisodes(1)().(dlEpisodesMsg)
	if !ok {
		t.Fatalf("fetchEpisodes returned %T, want dlEpisodesMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("fetchEpisodes error = %v; the fallback had the list", msg.err)
	}
	if len(msg.episodes) != 2 {
		t.Fatalf("got %d episodes, want the fallback's 2", len(msg.episodes))
	}
	if askedSeason != 2 {
		t.Fatalf("fallback was asked for season %d, want the selected season 2", askedSeason)
	}
}
