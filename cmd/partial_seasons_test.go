package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
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
