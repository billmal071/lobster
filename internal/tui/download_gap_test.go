package tui

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type gappedListProvider struct{ provider.Provider }

func (gappedListProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return []media.Episode{{ID: "2x2", Number: 2}, {ID: "2x3", Number: 3}},
		fmt.Errorf("%w: eztv carries only some episodes", provider.ErrIncompleteEpisodeList)
}

func TestFetchEpisodesKeepsAListWithGaps(t *testing.T) {
	prev := EpisodeListFallback
	EpisodeListFallback = nil
	t.Cleanup(func() { EpisodeListFallback = prev })

	d := &downloadDialog{
		item:     media.SearchResult{ID: "eztv/1", Type: media.TV},
		provider: gappedListProvider{},
		seasons:  []media.Season{{ID: "2", Number: 2}},
	}
	msg, ok := d.fetchEpisodes(0)().(dlEpisodesMsg)
	if !ok {
		t.Fatalf("fetchEpisodes returned %T, want dlEpisodesMsg", msg)
	}
	if msg.err != nil || len(msg.episodes) != 2 {
		t.Fatalf("err = %v, episodes = %d; want the 2 listed episodes and no error", msg.err, len(msg.episodes))
	}
}
