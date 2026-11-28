package playlist

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type gappedProvider struct{ *mockProvider }

func (g gappedProvider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	eps, err := g.mockProvider.GetEpisodes(id, seasonID)
	if err != nil {
		return nil, err
	}
	return eps, fmt.Errorf("%w: %w", provider.ErrIncompleteEpisodeList, provider.ErrNotCarried)
}

func gappedSession(seasonIdx, episodeIdx int) *Session {
	mp := &mockProvider{episodes: map[string][]media.Episode{
		"s1": {{Number: 1, ID: "e1"}, {Number: 2, ID: "e2"}},
		"s2": {{Number: 2, ID: "e4"}, {Number: 3, ID: "e5"}},
	}}
	seasons := []media.Season{{Number: 1, ID: "s1"}, {Number: 2, ID: "s2"}}
	eps := mp.episodes[seasons[seasonIdx].ID]
	return New(gappedProvider{mp}, media.SearchResult{ID: "show-1", Type: media.TV}, seasons, eps, seasonIdx, episodeIdx)
}

func TestNextCrossesIntoASeasonListedWithGaps(t *testing.T) {
	s := gappedSession(0, 1)
	ep, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v; a season with gaps still has episodes to play", err)
	}
	if ep.Number != 2 || s.SeasonIdx != 1 {
		t.Errorf("got episode %d in season index %d, want episode 2 of season index 1", ep.Number, s.SeasonIdx)
	}
}

func TestPreviousCrossesIntoASeasonListedWithGaps(t *testing.T) {
	s := gappedSession(1, 0)
	s.Provider = gappedProvider{&mockProvider{episodes: map[string][]media.Episode{
		"s1": {{Number: 1, ID: "e1"}, {Number: 3, ID: "e3"}},
	}}}
	ep, err := s.Previous()
	if err != nil {
		t.Fatalf("Previous: %v; a season with gaps still has episodes to play", err)
	}
	if ep.Number != 3 || s.SeasonIdx != 0 {
		t.Errorf("got episode %d in season index %d, want the last episode of season index 0", ep.Number, s.SeasonIdx)
	}
}

func TestNextStillFailsOnAnErrorWithNoList(t *testing.T) {
	s := gappedSession(0, 1)
	s.Provider = &mockProvider{episodes: map[string][]media.Episode{}}
	if _, err := s.Next(); err == nil {
		t.Fatal("Next succeeded with no episode list")
	}
	if s.SeasonIdx != 0 {
		t.Errorf("SeasonIdx = %d, want the rollback to 0", s.SeasonIdx)
	}
}
