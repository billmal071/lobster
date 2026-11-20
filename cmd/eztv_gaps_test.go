package cmd

import (
	"encoding/json"
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

func TestValidateNamesTheSourceWhenAGappedListLacksTheEpisode(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	withStubProviderBase(t, primary, "eztv")
	_ = validateSeasonEpisode(primary, playRef{ID: "eztv/1", Title: "South Park", Type: "tv"}, 5, 4)
	out := buf.String()
	if !strings.Contains(out, "EZTV does not carry") || strings.Contains(out, "has no episode") {
		t.Errorf("output = %s, want it to say EZTV does not carry the episode, not that the show has none", out)
	}
}

func fallbackNamed(t *testing.T, token string, seasons []media.Season, err error) *stubProvider {
	t.Helper()
	fb := chainShow()
	fb.seasons, fb.seasonsErr = seasons, err
	withStubProviderBase(t, fb, token)
	return fb
}

func TestGappedFallbackListNamesItselfNotTheFailedPrimary(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "flixhq.ws")
	primary := &stubProvider{seasonsErr: errors.New("boom")}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "flixhq.ws")
	chainOf(t, fallbackNamed(t, "eztv", []media.Season{{ID: "3", Number: 3}}, gapErr()))
	withEpisodesFlags(t, tvRef(t, ""), 1)

	_ = episodesRun(episodesCmd, nil)
	out := buf.String()
	if !strings.Contains(out, "EZTV does not carry season 1") || strings.Contains(out, "FLIXHQ") {
		t.Errorf("output = %s, want EZTV named, not the primary whose lookup merely failed", out)
	}
}

func TestIncompleteFallbackListBeatsAGappedOneForSeasonVerdict(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withBase(t, "flixhq.ws")
	primary := &stubProvider{seasonsErr: errors.New("boom")}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "flixhq.ws")
	gapped := fallbackNamed(t, "eztv", []media.Season{{ID: "3", Number: 3}}, gapErr())
	partial := fallbackNamed(t, "other", []media.Season{{ID: "1", Number: 1}},
		fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList))
	chainOf(t, gapped, partial)
	withEpisodesFlags(t, tvRef(t, ""), 2)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitProvidersFailed {
		t.Fatalf("err = %v, want exit %d: an incomplete list cannot establish season 2 is absent", err, exitProvidersFailed)
	}
}

func batchFallbackWith(t *testing.T, eps []media.Episode, err error) {
	t.Helper()
	fb := chainShow()
	fb.episodesBySeason = nil
	fb.episodes = eps
	fb.episodesWithErr = eps
	fb.episodesErr = err
	fb.seasons = []media.Season{{ID: "1", Number: 1}}
	chainOf(t, fb)
}

func TestBatchRejectsAFallbackPrefixThatMayBeTruncated(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"cut short by a failed probe", fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)},
		{"end not confirmed", fmt.Errorf("%w: %w: episode 2 answered 404 once", provider.ErrIncompleteEpisodeList, provider.ErrUnconfirmedEpisodeList)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			batchFallbackWith(t, []media.Episode{{ID: "1x1", Number: 1}}, c.err)
			boom := errors.New("boom")
			primary := &stubProvider{episodesErr: boom}
			l := newSeasonLister(primary, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV})
			eps, err := l.episodes(media.Season{ID: "1", Number: 1})
			if !errors.Is(err, boom) {
				t.Fatalf("episodes = %d, err = %v; want the primary's own error, not a fallback prefix downloaded as the whole season", len(eps), err)
			}
		})
	}
}

func TestBatchFailsASeasonWhenThePrimaryListsNothingAndTheFallbackPrefixMayBeTruncated(t *testing.T) {
	hostileEnv(t)
	batchFallbackWith(t, []media.Episode{{ID: "1x1", Number: 1}}, fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList))
	l := newSeasonLister(&stubProvider{}, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV})
	eps, err := l.episodes(media.Season{ID: "1", Number: 1})
	if err == nil {
		t.Fatalf("episodes = %d, err = nil; want a failure, not a season skipped as empty or downloaded short", len(eps))
	}
}

func TestBatchUsesAFallbackListThatIsCompleteOrGapped(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"complete", nil},
		{"catalogue gap", gapErr()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			batchFallbackWith(t, []media.Episode{{ID: "1x1", Number: 1}, {ID: "1x3", Number: 3}}, c.err)
			primary := &stubProvider{episodesErr: errors.New("boom")}
			l := newSeasonLister(primary, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV})
			eps, err := l.episodes(media.Season{ID: "1", Number: 1})
			if err != nil || len(eps) != 2 {
				t.Fatalf("episodes = %d, err = %v; want the fallback's 2 episodes", len(eps), err)
			}
		})
	}
}

func TestEpisodesOffersTheOnlySeasonOfAGappedSeasonList(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	primary := &stubProvider{
		seasons:    []media.Season{{ID: "9", Number: 9}},
		seasonsErr: gapErr(),
		episodes:   []media.Episode{{ID: "9x1", Number: 1}},
	}
	withStubProvider(t, primary)
	chainOf(t)

	withEpisodesFlags(t, tvRef(t, ""), 9)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	var out struct {
		Seasons  []int `json:"seasons"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if len(out.Seasons) != 1 || out.Seasons[0] != 9 || len(out.Warnings) == 0 || out.Warnings[0].Code != "season_list_incomplete" {
		t.Fatalf("output = %s, want season 9 offered with a season_list_incomplete warning", buf.String())
	}
}
