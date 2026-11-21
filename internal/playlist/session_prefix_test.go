package playlist

import (
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type prefixProvider struct{ *mockProvider }

func (p prefixProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return []media.Episode{{Number: 1, ID: "e1"}}, fmt.Errorf("%w: probe 429", provider.ErrIncompleteEpisodeList)
}

func TestNextRejectsAPrefixFromAProbeFailure(t *testing.T) {
	s := gappedSession(0, 1)
	s.Provider = prefixProvider{&mockProvider{}}
	if _, err := s.Next(); err == nil {
		t.Fatal("Next accepted a 1-episode prefix from a probe failure as the whole season")
	}
	if s.SeasonIdx != 0 {
		t.Errorf("SeasonIdx = %d, want the rollback to 0", s.SeasonIdx)
	}
}
