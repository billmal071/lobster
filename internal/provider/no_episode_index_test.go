package provider

import (
	"errors"
	"testing"
)

func TestSourcesWithoutAnEpisodeIndexSaySo(t *testing.T) {
	for name, p := range map[string]Provider{"vidnest": NewVidNest(), "moviebox": NewMovieBox()} {
		t.Run(name, func(t *testing.T) {
			eps, err := p.GetEpisodes("tv/1", "1")
			if len(eps) != 0 || !errors.Is(err, ErrNoEpisodeIndex) {
				t.Fatalf("GetEpisodes = %d episodes, %v; want none and ErrNoEpisodeIndex", len(eps), err)
			}
		})
	}
}
