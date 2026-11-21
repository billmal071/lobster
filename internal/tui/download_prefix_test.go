package tui

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type prefixListProvider struct{ provider.Provider }

func (prefixListProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return []media.Episode{{ID: "1x1", Number: 1}}, fmt.Errorf("%w: probe 429", provider.ErrIncompleteEpisodeList)
}

func TestFetchEpisodesRejectsAPrefixFromAProbeFailure(t *testing.T) {
	prev := EpisodeListFallback
	EpisodeListFallback = nil
	t.Cleanup(func() { EpisodeListFallback = prev })

	d := &downloadDialog{
		item:     media.SearchResult{ID: "x", Type: media.TV},
		provider: prefixListProvider{},
		seasons:  []media.Season{{ID: "1", Number: 1}},
	}
	msg := d.fetchEpisodes(0)().(dlEpisodesMsg)
	if msg.err == nil {
		t.Fatalf("accepted a 1-episode prefix (%d episodes) from a probe failure", len(msg.episodes))
	}
}
