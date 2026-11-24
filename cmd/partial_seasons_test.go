package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

var errSeasonsIncomplete = fmt.Errorf("%w: eztv could not fetch every page", provider.ErrIncompleteEpisodeList)

func partialSeasonsPrimary() *stubProvider {
	return &stubProvider{
		seasons:    []media.Season{{ID: "1", Number: 1}, {ID: "2", Number: 2}},
		seasonsErr: errSeasonsIncomplete,
		episodes:   []media.Episode{{ID: "2x1", Number: 1}},
	}
}

var partialSeasonsSelection = media.SearchResult{ID: "tv/1403", Title: "Show", Year: "2013", Type: media.TV}

// TestResolveAndPlayUsesAPartialSeasonListWhenItHoldsTheSeason checks that a season list flagged incomplete is still used when it contains the requested season, so the primary lists that season's episodes.
func TestResolveAndPlayUsesAPartialSeasonListWhenItHoldsTheSeason(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	withFallbackChain(t, &recordingStreamProvider{result: partialSeasonsSelection, url: stubStreamServer(t)})

	primary := partialSeasonsPrimary()
	_ = resolveAndPlay(primary, partialSeasonsSelection, 2, 1)
	if primary.lastSeasonID != "2" {
		t.Fatalf("primary was asked for season %q, want the partial list's season 2 to be used", primary.lastSeasonID)
	}
}

// TestResolveAndPlayDoesNotRefuseASeasonMissingFromAPartialList checks that a season absent from an incomplete list is not reported as nonexistent: the fallback chain is asked for it by number.
func TestResolveAndPlayDoesNotRefuseASeasonMissingFromAPartialList(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	fb := &recordingStreamProvider{result: partialSeasonsSelection, url: stubStreamServer(t)}
	withFallbackChain(t, fb)

	err := resolveAndPlay(partialSeasonsPrimary(), partialSeasonsSelection, 5, 1)
	var notFound errSeasonNotFound
	if errors.As(err, &notFound) {
		t.Fatalf("resolveAndPlay = %v; season 5 is absent only from a list that may be short", err)
	}
	if err != nil {
		t.Fatalf("resolveAndPlay = %v, want the resolver to be tried", err)
	}
	if got := fb.episodeAsked(); got != "1403:5:1" {
		t.Fatalf("fallback Watch asked for %q, want 1403:5:1", got)
	}
}

// TestValidateSeasonEpisodeDefersOnAPartialSeasonList checks that play validation does not reject a season missing from an incomplete season list.
func TestValidateSeasonEpisodeDefersOnAPartialSeasonList(t *testing.T) {
	hostileEnv(t)
	if err := validateSeasonEpisode(partialSeasonsPrimary(), playRef{ID: "tv/1403", Title: "Show"}, 5, 1); err != nil {
		t.Fatalf("validateSeasonEpisode = %v, want nil: a season missing from a partial list is not proof it does not exist", err)
	}
}

// TestEpisodesListsTheSeasonOfAPartialPrimarySeasonList checks that the episodes command lists a season held by the primary's incomplete season list instead of failing.
func TestEpisodesListsTheSeasonOfAPartialPrimarySeasonList(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	primary := partialSeasonsPrimary()
	primary.episodes = []media.Episode{{ID: "2x1", Number: 1, Title: "Return"}}
	withStubProvider(t, primary)
	withFallbackChain(t)
	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v; the partial list holds season 2 and must be used\n%s", err, buf.String())
	}
	if primary.lastSeasonID != "2" {
		t.Fatalf("primary was asked for season %q, want 2", primary.lastSeasonID)
	}
}

// TestEpisodesDoesNotCallASeasonMissingFromAPartialListNonexistent checks that the episodes command answers providers_failed, not no_results, for a season missing from an incomplete list.
func TestEpisodesDoesNotCallASeasonMissingFromAPartialListNonexistent(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	withStubProvider(t, partialSeasonsPrimary())
	withFallbackChain(t)
	withEpisodesFlags(t, tvRef(t, ""), 5)

	err := episodesRun(episodesCmd, nil)
	if got := exitCodeOf(err); got != exitProvidersFailed {
		t.Fatalf("exit = %d (%v), want %d: season 5 is absent from a list that may be short, which is not no_results", got, err, exitProvidersFailed)
	}
}

// TestResolveAndPlayDoesNotRefuseASeasonLackingFromAPartialChainHit checks that a season missing from the only chain hit, whose list is flagged incomplete, is not reported as proven absent.
func TestResolveAndPlayDoesNotRefuseASeasonLackingFromAPartialChainHit(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	chain := partialSeasonsPrimary()
	chain.results = []media.SearchResult{partialSeasonsSelection}
	withFallbackChain(t, chain)

	primary := &stubProvider{seasonsErr: errors.New("seasons unavailable")}
	err := resolveAndPlay(primary, partialSeasonsSelection, 5, 1)
	var notFound errSeasonNotFound
	if errors.As(err, &notFound) {
		t.Fatalf("resolveAndPlay = %v; the only chain list is flagged incomplete, so season 5 is not proven absent", err)
	}
}

// completePrimaryWithPartialChainHit returns a primary with a complete list of seasons 1 to 3 and a chain provider for the same show whose season list, season 1 only, is flagged incomplete.
func completePrimaryWithPartialChainHit() (*stubProvider, *stubProvider) {
	primary := &stubProvider{seasons: []media.Season{{ID: "1", Number: 1}, {ID: "2", Number: 2}, {ID: "3", Number: 3}}}
	chain := &stubProvider{
		results:    []media.SearchResult{{ID: "tv/show-1", Title: "Some Show", Type: media.TV}},
		seasons:    []media.Season{{ID: "1", Number: 1}},
		seasonsErr: errSeasonsIncomplete,
	}
	return primary, chain
}

// TestEpisodesDoesNotCallASeasonMissingFromACompletePrimaryNonexistentWhenAChainHitIsPartial checks that the episodes command answers providers_failed for a season missing from a complete primary list when a consulted chain list is incomplete.
func TestEpisodesDoesNotCallASeasonMissingFromACompletePrimaryNonexistentWhenAChainHitIsPartial(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	primary, chain := completePrimaryWithPartialChainHit()
	withStubProvider(t, primary)
	withFallbackChain(t, chain)
	withEpisodesFlags(t, tvRef(t, ""), 5)

	err := episodesRun(episodesCmd, nil)
	if got := exitCodeOf(err); got != exitProvidersFailed {
		t.Fatalf("exit = %d (%v), want %d: a consulted chain list was incomplete", got, err, exitProvidersFailed)
	}
}

// TestValidateSeasonEpisodeDefersWhenAChainHitListIsPartial checks that play validation defers for the same season, agreeing with the episodes command.
func TestValidateSeasonEpisodeDefersWhenAChainHitListIsPartial(t *testing.T) {
	hostileEnv(t)
	primary, chain := completePrimaryWithPartialChainHit()
	withFallbackChain(t, chain)
	if err := validateSeasonEpisode(primary, playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv"}, 5, 1); err != nil {
		t.Fatalf("validateSeasonEpisode = %v, want nil: play must agree with episodes that season 5 is unconfirmed", err)
	}
}

// TestResolveAndPlayDefersWhenAChainHitListIsPartial checks that resolveAndPlay asks the chain for the season instead of reporting it not found when a consulted chain list is incomplete.
func TestResolveAndPlayDefersWhenAChainHitListIsPartial(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	primary, chain := completePrimaryWithPartialChainHit()
	fb := &recordingStreamProvider{result: media.SearchResult{ID: "tv/show-1", Title: "Some Show", Type: media.TV}, url: stubStreamServer(t)}
	withFallbackChain(t, chain, fb)

	err := resolveAndPlay(primary, media.SearchResult{ID: "tv/show-1", Title: "Some Show", Type: media.TV}, 5, 1)
	var notFound errSeasonNotFound
	if errors.As(err, &notFound) {
		t.Fatalf("resolveAndPlay = %v, want the chain asked for season 5", err)
	}
}

// TestEpisodesWarnsWhenTheSeasonListWasIncomplete checks that the episodes command adds a season_list_incomplete warning when the season list it used was flagged incomplete.
func TestEpisodesWarnsWhenTheSeasonListWasIncomplete(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	primary := partialSeasonsPrimary()
	withStubProvider(t, primary)
	withFallbackChain(t)
	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v\n%s", err, buf.String())
	}
	warnings := decodeEpisodesWarnings(t, buf.Bytes())
	if len(warnings) != 1 || warnings[0].Code != "season_list_incomplete" {
		t.Fatalf("warnings = %+v, want one season_list_incomplete", warnings)
	}
}

// TestEpisodesDoesNotWarnAboutSeasonsWhenTheListIsComplete checks that no season_list_incomplete warning appears for a complete season list.
func TestEpisodesDoesNotWarnAboutSeasonsWhenTheListIsComplete(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withStubProvider(t, twoSeasonStub())
	withFallbackChain(t)
	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v", err)
	}
	for _, w := range decodeEpisodesWarnings(t, buf.Bytes()) {
		if w.Code == "season_list_incomplete" {
			t.Fatalf("unexpected %+v for a complete season list", w)
		}
	}
}

var gappedShow = media.SearchResult{ID: "tv/show-1", Title: "Some Show", Type: media.TV}

func gappedSeasonsStub(partial bool, numbers ...int) *stubProvider {
	stub := &stubProvider{
		results:          []media.SearchResult{gappedShow},
		episodesBySeason: map[string][]media.Episode{},
	}
	for _, n := range numbers {
		id := fmt.Sprintf("s%d", n)
		stub.seasons = append(stub.seasons, media.Season{ID: id, Number: n})
		stub.episodesBySeason[id] = []media.Episode{{ID: id + "e1", Number: 1}}
	}
	if partial {
		stub.seasonsErr = errSeasonsIncomplete
	}
	return stub
}

func autoplayFrom(t *testing.T, primary provider.Provider, season int, chain ...provider.Provider) ([]playedEp, *[]string) {
	t.Helper()
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{})
	warned := captureWarnings(t)
	withFallbackChain(t, chain...)

	var played []playedEp
	prevPlay, prevAuto := playEpisode, flagAutoplay
	playEpisode = func(s *playlist.Session) error {
		played = append(played, playedEp{s.CurrentSeason().Number, s.Current().Number})
		s.LastPosition, s.LastDuration = endedNaturally.Position, endedNaturally.Duration
		return nil
	}
	flagAutoplay = true
	t.Cleanup(func() { playEpisode, flagAutoplay = prevPlay, prevAuto })

	if err := resolveAndPlay(primary, gappedShow, season, 1); err != nil {
		t.Fatalf("resolveAndPlay: %v", err)
	}
	return played, warned
}

func TestAutoplayStopsBeforeAGapInAPartialPrimarySeasonList(t *testing.T) {
	played, warned := autoplayFrom(t, gappedSeasonsStub(true, 1, 3), 1)
	wantPlayed(t, played, []playedEp{{1, 1}})
	if !containsSubstring(*warned, "season list may be incomplete: stopping after season 1") {
		t.Fatalf("warnings = %q, want one saying the season list stops after season 1", *warned)
	}
}

func TestAutoplayCrossesAGapInACompletePrimarySeasonList(t *testing.T) {
	played, warned := autoplayFrom(t, gappedSeasonsStub(false, 1, 3), 1)
	wantPlayed(t, played, []playedEp{{1, 1}, {3, 1}})
	if containsSubstring(*warned, "may be incomplete") {
		t.Fatalf("warnings = %q, want none for a complete season list", *warned)
	}
}

func TestAutoplayWarnsInsteadOfEndOfSeriesAtTheEndOfAPartialSeasonList(t *testing.T) {
	played, warned := autoplayFrom(t, gappedSeasonsStub(true, 1, 2), 1)
	wantPlayed(t, played, []playedEp{{1, 1}, {2, 1}})
	if !containsSubstring(*warned, "season list may be incomplete: stopping after season 2") {
		t.Fatalf("warnings = %q, want one saying the season list may be incomplete after season 2", *warned)
	}
}

func TestAutoplayCrossesAGapInACompleteChainSeasonListAfterAPartialPrimary(t *testing.T) {
	played, warned := autoplayFrom(t, gappedSeasonsStub(true, 1, 3), 2, gappedSeasonsStub(false, 2, 4))
	wantPlayed(t, played, []playedEp{{2, 1}, {4, 1}})
	if containsSubstring(*warned, "stopping after") {
		t.Fatalf("warnings = %q, want no stop: the season list in use is the chain's complete one", *warned)
	}
}

func TestAutoplayStopsBeforeAGapInAPartialChainSeasonListAfterACompletePrimary(t *testing.T) {
	played, warned := autoplayFrom(t, gappedSeasonsStub(false, 1), 2, gappedSeasonsStub(true, 2, 4))
	wantPlayed(t, played, []playedEp{{2, 1}})
	if !containsSubstring(*warned, "season list may be incomplete: stopping after season 2") {
		t.Fatalf("warnings = %q, want one saying the season list stops after season 2", *warned)
	}
}

func TestAutoplayCrossesAGapInACompleteChainSeasonListWhenAPartialPrimaryCannotListEpisodes(t *testing.T) {
	primary := gappedSeasonsStub(true, 1, 3)
	primary.episodesErr = errors.New("episodes unavailable")
	played, warned := autoplayFrom(t, primary, 1, gappedSeasonsStub(false, 1, 3))
	wantPlayed(t, played, []playedEp{{1, 1}, {3, 1}})
	if containsSubstring(*warned, "stopping after") {
		t.Fatalf("warnings = %q, want no stop: the season list in use is the chain's complete one", *warned)
	}
}

func TestAutoplayStopsBeforeAGapInAPartialChainSeasonListWhenACompletePrimaryCannotListEpisodes(t *testing.T) {
	primary := gappedSeasonsStub(false, 1, 3)
	primary.episodesErr = errors.New("episodes unavailable")
	played, warned := autoplayFrom(t, primary, 1, gappedSeasonsStub(true, 1, 3))
	wantPlayed(t, played, []playedEp{{1, 1}})
	if !containsSubstring(*warned, "season list may be incomplete: stopping after season 1") {
		t.Fatalf("warnings = %q, want one saying the season list stops after season 1", *warned)
	}
}

// TestSeasonMissingFromAPartialListAsksForTheEpisode checks that --season with no --episode prompts for the episode before the chain is asked by number, instead of requesting episode 0.
func TestSeasonMissingFromAPartialListAsksForTheEpisode(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	fb := &recordingStreamProvider{result: partialSeasonsSelection, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	withTTY(t, true, false)
	asked := withAnswers(t, map[string][]string{"Episode": {"4"}})

	if err := resolveAndPlay(partialSeasonsPrimary(), partialSeasonsSelection, 5, 0); err != nil {
		t.Fatalf("resolveAndPlay = %v (prompts: %v)", err, *asked)
	}
	if got := fb.episodeAsked(); got != "1403:5:4" {
		t.Fatalf("fallback Watch asked for %q, want 1403:5:4", got)
	}
	if !equalStrings(*asked, []string{"Episode"}) {
		t.Fatalf("prompts = %v, want [Episode]", *asked)
	}
}

// TestSeasonMissingFromAPartialListRefusesEpisodeZeroWhereNobodyCanBeAsked checks that without a terminal the same request is refused with the typed error and no provider is asked for episode 0.
func TestSeasonMissingFromAPartialListRefusesEpisodeZeroWhereNobodyCanBeAsked(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	fb := &recordingStreamProvider{result: partialSeasonsSelection, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	withTTY(t, false, false)
	asked := withAnswers(t, map[string][]string{"Episode": {"4"}})

	err := resolveAndPlay(partialSeasonsPrimary(), partialSeasonsSelection, 5, 0)
	if !errors.Is(err, resolver.ErrSeasonEpisodeRequired) {
		t.Fatalf("err = %v, want ErrSeasonEpisodeRequired", err)
	}
	if fb.watchCallCnt != 0 || len(*asked) != 0 {
		t.Fatalf("watch calls %d (episode %q), prompts %v; want none", fb.watchCallCnt, fb.episodeAsked(), *asked)
	}
}
