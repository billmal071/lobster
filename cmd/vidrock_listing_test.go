package cmd

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// matchedVidrock is the real Vidrock with its TMDB search answered offline, so
// the season lookup reaches Vidrock's own GetSeasons.
type matchedVidrock struct{ *provider.Vidrock }

func (matchedVidrock) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "tv/1", Title: "Some Show", Type: media.TV}}, nil
}

func TestVidrockInTheChainHasAnsweredRatherThanFailed(t *testing.T) {
	code, message := episodesErrEnvelope(t, &stubProvider{seasonsErr: provider.ErrNoResults}, matchedVidrock{provider.NewVidrock()})
	if code != "no_results" {
		t.Fatalf("code = %q (%s), want no_results: Vidrock never lists seasons, which is an answer and not a failed lookup", code, message)
	}
}

func TestPlayUnderAVidrockPrimaryDefersAnUnlistedSeasonToTheResolver(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	chainOf(t, &stubProvider{}, &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults})
	r := playRef{ID: "tv/1", Title: "Some Show", Type: "tv"}
	if err := validateSeasonEpisode(provider.NewVidrock(), r, 3, 2); err != nil {
		t.Fatalf("validateSeasonEpisode = %v, want nil: Vidrock has no season index, so play resolves S03E02 by number", err)
	}
	if res := resolveSeason(provider.NewVidrock(), seasonRequest(r), lookupPrimarySeasons(provider.NewVidrock(), r.ID), 3); res.verdict == seasonAbsent {
		t.Fatalf("verdict = seasonAbsent, want no refusal when no source listed any season")
	}
}
