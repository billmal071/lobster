package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func gapErr() error {
	return fmt.Errorf("%w: %w: eztv carries only some episodes of season 5; episode 4 is not on EZTV",
		provider.ErrIncompleteEpisodeList, provider.ErrNotCarried)
}

func TestBatchListsASeasonWithGaps(t *testing.T) {
	hostileEnv(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	l := newSeasonLister(primary, media.SearchResult{ID: "eztv/1", Title: "Show", Type: media.TV})
	eps, err := l.episodes(media.Season{ID: "5", Number: 5})
	if err != nil || len(eps) != 2 {
		t.Fatalf("episodes = %d, err = %v; want the 2 listed episodes downloadable", len(eps), err)
	}
}

func TestValidateKeepsTheOldRefusalForAnEpisodeEZTVLacks(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	err := validateSeasonEpisode(primary, playRef{ID: "eztv/1", Title: "South Park", Type: "tv"}, 5, 4)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitNoResults {
		t.Fatalf("err = %v, want exit %d (no_results) for an episode missing from a list of what EZTV carries", err, exitNoResults)
	}
}

func TestValidateDefersWhenThePartialListMayHideTheEpisode(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}},
		episodesErr:     fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList),
	}
	if err := validateSeasonEpisode(primary, playRef{ID: "eztv/1", Title: "South Park", Type: "tv"}, 5, 4); err != nil {
		t.Fatalf("err = %v, want nil: the list was cut short by a failed page, so episode 4 may exist", err)
	}
}

func TestEpisodesFromFallbackAfterATransientErrorMakesNoEZTVClaim(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	primary := twoSeasonStub()
	primary.episodesErr = errors.New("eztv: unexpected status 429")
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return []provider.Provider{fb} }
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	if len(w) != 1 || w[0].Code != "episode_list_from_fallback" {
		t.Fatalf("warnings = %+v, want one episode_list_from_fallback", w)
	}
	if strings.Contains(w[0].Message, "will not play via EZTV") {
		t.Errorf("message = %q: a 429 is not evidence the episodes cannot play via EZTV", w[0].Message)
	}
}

func TestEpisodesFromFallbackWhenEZTVListsNothingSaysTheyWillNotPlayViaEZTV(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	primary := &stubProvider{seasonsErr: fmt.Errorf("eztv: no torrents for eztv/1 (%w)", provider.ErrNotCarried)}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return []provider.Provider{fb} }
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	if len(w) != 1 || !strings.Contains(w[0].Message, "will not play via EZTV") {
		t.Fatalf("warnings = %+v, want the will-not-play-via-EZTV wording when EZTV has none of the show", w)
	}
}

func TestEpisodesGapWordingIsNotTheEnumerationShortfall(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")

	withEpisodesFlags(t, tvRef(t, ""), 5)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	if len(w) != 1 || w[0].Code != "episode_list_incomplete" {
		t.Fatalf("warnings = %+v, want one episode_list_incomplete", w)
	}
	if strings.Contains(w[0].Message, "establish where this season ends") || !strings.Contains(w[0].Message, "has no release for some episodes") {
		t.Errorf("message = %q, want gap wording rather than the enumeration-shortfall wording", w[0].Message)
	}
}
