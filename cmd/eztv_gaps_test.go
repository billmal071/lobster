package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
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

func TestBatchListerWarnsWhenASeasonListHasGaps(t *testing.T) {
	hostileEnv(t)
	warnings := captureWarnings(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	l := newSeasonLister(primary, media.SearchResult{ID: "eztv/1", Title: "Show", Type: media.TV})
	if _, err := l.episodes(media.Season{ID: "5", Number: 5}); err != nil {
		t.Fatalf("episodes: %v", err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "no standalone release for some episodes of season 5") {
		t.Fatalf("warnings = %q, want one saying season 5 lacks standalone releases for some episodes", *warnings)
	}
}

func TestBatchListerWarnsWhenAFallbackSeasonListHasGaps(t *testing.T) {
	hostileEnv(t)
	warnings := captureWarnings(t)
	fb := scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(2)})
	fb.stubProvider.episodesWithErr = []media.Episode{{ID: "e1", Number: 1}, {ID: "e5", Number: 5}}
	fb.stubProvider.episodesErr = gapErr()
	chainOf(t, fb)
	l := newSeasonLister(&stubProvider{episodesErr: errBoom}, media.SearchResult{ID: "x/1", Title: "Show", Type: media.TV}, seasonsOf(2)...)
	if eps, err := l.episodes(media.Season{ID: "2", Number: 2}); err != nil || len(eps) != 2 {
		t.Fatalf("episodes = %d, err = %v; want the fallback's 2 listed episodes", len(eps), err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "no standalone release for some episodes of season 2") {
		t.Fatalf("warnings = %q, want one saying the fallback's season 2 lacks standalone releases for some episodes", *warnings)
	}
}

func TestBatchListerDoesNotWarnForACompleteList(t *testing.T) {
	hostileEnv(t)
	warnings := captureWarnings(t)
	primary := &stubProvider{seasons: []media.Season{{ID: "5", Number: 5}}, episodes: []media.Episode{{ID: "5x1", Number: 1}}}
	l := newSeasonLister(primary, media.SearchResult{ID: "eztv/1", Title: "Show", Type: media.TV})
	if _, err := l.episodes(media.Season{ID: "5", Number: 5}); err != nil {
		t.Fatalf("episodes: %v", err)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %q, want none for a complete list", *warnings)
	}
}

func TestValidateDefersAnEpisodeMissingFromAGappedList(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}
	if err := validateSeasonEpisode(primary, playRef{ID: "eztv/1", Title: "South Park", Type: "tv"}, 5, 4); err != nil {
		t.Fatalf("err = %v, want nil: a season pack or another source may still hold episode 4, so the resolver is asked", err)
	}
}

func TestValidateStillRefusesAnEpisodeMissingFromACompleteList(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	primary := &stubProvider{
		seasons:  []media.Season{{ID: "5", Number: 5}},
		episodes: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
	}
	err := validateSeasonEpisode(primary, playRef{ID: "x/1", Title: "South Park", Type: "tv"}, 5, 4)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitNoResults {
		t.Fatalf("err = %v, want exit %d (no_results) for an episode missing from a complete list", err, exitNoResults)
	}
}

func TestResolveAndPlayWarnsThatAGappedSeasonListLacksOnlyStandaloneReleases(t *testing.T) {
	hostileEnv(t)
	warnings := captureWarnings(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	withFallbackChain(t)
	primary := &stubProvider{
		seasons:    seasonsOf(1, 3),
		seasonsErr: fmt.Errorf("%w: %w: eztv carries only some seasons; season 2 is not on EZTV", provider.ErrIncompleteEpisodeList, provider.ErrNotCarried),
		episodes:   []media.Episode{{ID: "1x1", Number: 1}},
	}
	_ = resolveAndPlay(primary, media.SearchResult{ID: "eztv/1", Title: "Show", Type: media.TV}, 1, 1)
	for _, w := range *warnings {
		if strings.Contains(w, "some seasons") {
			if !strings.Contains(w, "has no standalone release for some seasons") {
				t.Fatalf("warning = %q, want it to claim no standalone release: a season pack may still hold a season", w)
			}
			return
		}
	}
	t.Fatalf("warnings = %q, want one about the seasons EZTV lacks", *warnings)
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
	if strings.Contains(w[0].Message, "establish where this season ends") || !strings.Contains(w[0].Message, "has no standalone release for some episodes") {
		t.Errorf("message = %q, want gap wording rather than the enumeration-shortfall wording", w[0].Message)
	}
	if strings.Contains(w[0].Message, "all it carries") || !strings.Contains(w[0].Message, "the 2 episodes above are the ones it has standalone releases for") {
		t.Errorf("message = %q, want the listed count qualified as standalone releases: a season pack may hold the rest", w[0].Message)
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
	if !strings.Contains(out, "EZTV has no standalone release for season 1") || strings.Contains(out, "FLIXHQ") {
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

func chainMember(id string, eps []media.Episode, err error) *stubProvider {
	return &stubProvider{
		results:         []media.SearchResult{{ID: id, Title: "Some Show", Type: media.TV}},
		seasons:         seasonsOf(1),
		episodes:        eps,
		episodesWithErr: eps,
		episodesErr:     err,
	}
}

func numbered(nums ...int) []media.Episode {
	var eps []media.Episode
	for _, n := range nums {
		eps = append(eps, media.Episode{ID: fmt.Sprintf("1x%d", n), Number: n})
	}
	return eps
}

func TestConfirmedEpisodeListSkipsPrefixesThatMayBeTruncated(t *testing.T) {
	truncated := fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)
	unconfirmed := fmt.Errorf("%w: %w: episode 2 answered 404 once", provider.ErrIncompleteEpisodeList, provider.ErrUnconfirmedEpisodeList)
	cases := []struct {
		name        string
		chain       []provider.Provider
		wantFrom    string
		wantEps     int
		wantRefusal string
	}{
		{"a truncated prefix then a complete list",
			[]provider.Provider{chainMember("a", numbered(1), truncated), chainMember("b", numbered(1, 2, 3), nil)}, "b", 3, ""},
		{"an unconfirmed prefix then a complete list",
			[]provider.Provider{chainMember("a", numbered(1), unconfirmed), chainMember("b", numbered(1, 2, 3), nil)}, "b", 3, ""},
		{"a truncated prefix then a gapped list",
			[]provider.Provider{chainMember("a", numbered(1), truncated), chainMember("b", numbered(1, 5), gapErr())}, "b", 2, ""},
		{"two truncated prefixes then a complete list",
			[]provider.Provider{chainMember("a", numbered(1), truncated), chainMember("b", numbered(1, 2), unconfirmed), chainMember("c", numbered(1, 2, 3), nil)}, "c", 3, ""},
		{"a complete list ahead of a longer one keeps chain order",
			[]provider.Provider{chainMember("a", numbered(1, 2), nil), chainMember("b", numbered(1, 2, 3), nil)}, "a", 2, ""},
		{"a failing member then a truncated prefix then a complete list",
			[]provider.Provider{chainMember("a", nil, errors.New("boom")), chainMember("b", numbered(1), truncated), chainMember("c", numbered(1, 2), nil)}, "c", 2, ""},
		{"only truncated prefixes are refused with the first one's reason",
			[]provider.Provider{chainMember("a", numbered(1), truncated), chainMember("b", numbered(1, 2), unconfirmed)}, "", 0, "page 2 failed"},
		{"a truncated prefix then a failing member is refused",
			[]provider.Provider{chainMember("a", numbered(1), truncated), chainMember("b", nil, errors.New("boom"))}, "", 0, "page 2 failed"},
		{"a listing that failed and nothing else is a failure",
			[]provider.Provider{chainMember("a", nil, errors.New("boom")), chainMember("b", nil, nil)}, "", 0, "a fallback lookup failed"},
		{"a search that failed and nothing else is a failure",
			[]provider.Provider{&stubProvider{searchErr: errors.New("boom")}, chainMember("b", nil, nil)}, "", 0, "a fallback lookup failed"},
		{"a failed listing beside a complete list is not",
			[]provider.Provider{chainMember("a", nil, errors.New("boom")), chainMember("b", numbered(1, 2), nil)}, "b", 2, ""},
		{"members that answer with no episodes are neither an answer nor a failure",
			[]provider.Provider{chainMember("a", nil, provider.ErrNoResults), chainMember("b", nil, provider.ErrNotCarried), chainMember("c", nil, nil)}, "", 0, ""},
		{"an empty chain", nil, "", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			chainOf(t, c.chain...)
			sel := media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV}
			hits, scanFailed := fallbackSeasonScan(&stubProvider{}, contentRequest(sel), []int{1})
			a, err := confirmedEpisodeList(hits, scanFailed, 1, sel.Title)
			if c.wantRefusal != "" {
				if a != nil || err == nil || !strings.Contains(err.Error(), c.wantRefusal) {
					t.Fatalf("answer = %v, err = %v; want a refusal naming %q", a, err, c.wantRefusal)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want none", err)
			}
			if c.wantFrom == "" {
				if a != nil {
					t.Fatalf("answer from %s, want none", a.hit.id)
				}
				return
			}
			if a == nil || a.hit.id != c.wantFrom || len(a.episodes) != c.wantEps {
				t.Fatalf("answer = %+v, want %d episodes from %s", a, c.wantEps, c.wantFrom)
			}
		})
	}
}

func TestBatchPassesOverATruncatedFallbackPrefixForALaterCompleteList(t *testing.T) {
	hostileEnv(t)
	chainOf(t,
		chainMember("a", numbered(1), fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)),
		chainMember("b", numbered(1, 2, 3), nil),
	)
	primary := &stubProvider{episodesErr: errors.New("boom")}
	l := newSeasonLister(primary, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV})
	eps, err := l.episodes(media.Season{ID: "1", Number: 1})
	if err != nil || len(eps) != 3 {
		t.Fatalf("episodes = %d, err = %v; want the second fallback's 3 episodes", len(eps), err)
	}
}

func TestBatchDoesNotSkipASeasonTheChainFailedToList(t *testing.T) {
	cases := []struct {
		name     string
		member   provider.Provider
		wantFail bool
	}{
		{"a fallback listing failed", chainMember("a", nil, errors.New("boom")), true},
		{"a fallback search failed", &stubProvider{searchErr: errors.New("boom")}, true},
		{"the fallback also lists nothing", chainMember("a", nil, nil), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			chainOf(t, c.member)
			l := newSeasonLister(&stubProvider{}, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV})
			eps, err := l.episodes(media.Season{ID: "1", Number: 1})
			if (err != nil) != c.wantFail || len(eps) != 0 {
				t.Fatalf("episodes = %d, err = %v; want failure %v", len(eps), err, c.wantFail)
			}
		})
	}
}

func TestEpisodesSaysAGappedSeasonListIsAllTheProviderCarries(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withStubProvider(t, &stubProvider{
		seasons:    []media.Season{{ID: "9", Number: 9}},
		seasonsErr: gapErr(),
		episodes:   []media.Episode{{ID: "9x1", Number: 1}},
	})
	chainOf(t)

	withEpisodesFlags(t, tvRef(t, ""), 9)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	msg := seasonListWarning(t, buf.Bytes())
	if strings.Contains(msg, "may be missing") || !strings.Contains(msg, "does not carry every season") {
		t.Fatalf("message = %q, want the confirmed catalogue gap stated, not hedged", msg)
	}
}

func TestEpisodesHedgesASeasonListCutShort(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withStubProvider(t, partialSeasonsPrimary())
	withFallbackChain(t)

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if msg := seasonListWarning(t, buf.Bytes()); !strings.Contains(msg, "may be missing") {
		t.Fatalf("message = %q, want a list cut short described as possibly missing seasons", msg)
	}
}

func seasonListWarning(t *testing.T, out []byte) string {
	t.Helper()
	var payload struct {
		Warnings []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	for _, w := range payload.Warnings {
		if w.Code == "season_list_incomplete" {
			return w.Message
		}
	}
	t.Fatalf("no season_list_incomplete warning in %s", out)
	return ""
}
