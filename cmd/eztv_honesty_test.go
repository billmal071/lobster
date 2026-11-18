package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type carrierStub struct {
	*stubProvider
	carries error
}

func (c *carrierStub) CarriesEpisode(string, int, int) error { return c.carries }

func notCarried() error {
	return fmt.Errorf("eztv: no torrents for eztv/121955 S05E04 (%w)", provider.ErrNotCarried)
}

func TestTryFallbackStreamLeadsWithTheEZTVReason(t *testing.T) {
	hostileEnv(t)
	a, b := newDeadChain("South Park")
	withFallbackChain(t, a, b)

	primary := &carrierStub{stubProvider: &stubProvider{}, carries: notCarried()}
	withStubProviderBase(t, primary, "eztv")
	_, err := tryFallbackStream(primary, media.SearchResult{ID: "eztv/121955", Title: "South Park", Type: media.TV}, 5, 4)
	if err == nil {
		t.Fatal("want an error from a chain that streams nothing")
	}
	if !strings.HasPrefix(err.Error(), "EZTV has no standalone release of South Park S05E04") {
		t.Errorf("error = %q, want it to lead with the EZTV reason", err)
	}
	if !strings.Contains(err.Error(), "status 404 alpha") {
		t.Errorf("error = %q, want the chain's own reasons kept after it", err)
	}
	var rf *resolveFailure
	if !errors.As(err, &rf) || rf.report == nil {
		t.Errorf("error %T lost the resolver report", err)
	}
}

func TestTryFallbackStreamAddsNoEZTVReasonWhenEZTVHasTheEpisode(t *testing.T) {
	hostileEnv(t)
	a, b := newDeadChain("Show")
	withFallbackChain(t, a, b)

	primary := &carrierStub{stubProvider: &stubProvider{}, carries: nil}
	withStubProviderBase(t, primary, "eztv")
	_, err := tryFallbackStream(primary, media.SearchResult{ID: "eztv/121955", Title: "South Park", Type: media.TV}, 5, 4)
	if err == nil || strings.Contains(err.Error(), "EZTV has no standalone release") {
		t.Fatalf("error = %v, want no EZTV verdict when EZTV does carry the episode", err)
	}
}

func TestTryFallbackStreamAddsNoEZTVReasonWhenTheLookupFailed(t *testing.T) {
	hostileEnv(t)
	a, b := newDeadChain("Show")
	withFallbackChain(t, a, b)

	primary := &carrierStub{stubProvider: &stubProvider{}, carries: errors.New("eztv: unexpected status 429")}
	withStubProviderBase(t, primary, "eztv")
	_, err := tryFallbackStream(primary, media.SearchResult{ID: "eztv/121955", Title: "South Park", Type: media.TV}, 5, 4)
	if err == nil || strings.Contains(err.Error(), "EZTV has no standalone release") {
		t.Fatalf("error = %v, want no claim that EZTV lacks the episode when the lookup itself failed", err)
	}
}

func TestPlayReportsTheEZTVReasonFirst(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{})
	buf := captureAgentOut(t)
	withPlayerAvailable(t)

	a, b := newDeadChain("Some Show")
	withFallbackChain(t, a, b)
	primary := &carrierStub{
		stubProvider: &stubProvider{seasonsErr: errors.New("eztv: no torrents")},
		carries:      notCarried(),
	}
	withStubProviderBase(t, primary, "eztv")
	prev := agentProvider
	agentProvider = func() provider.Provider { return primary }
	t.Cleanup(func() { agentProvider = prev })

	withEpisodesFlags(t, tvRef(t, ""), 5)
	prevEpisode := flagEpisode
	flagEpisode = 4
	t.Cleanup(func() { flagEpisode = prevEpisode })

	if err := playRun(playCmd, nil); err == nil {
		t.Fatal("play succeeded with nothing to stream")
	}
	env := decodeErrEnvelope(t, buf.String())
	if env.Error.Code != "providers_failed" {
		t.Errorf("code = %q, want providers_failed", env.Error.Code)
	}
	if !strings.HasPrefix(env.Error.Message, "EZTV has no standalone release of Some Show S05E04") {
		t.Errorf("message = %q, want the EZTV reason first", env.Error.Message)
	}
	if len(env.Error.Providers) != 2 {
		t.Errorf("error.providers = %d rows, want the chain's 2 kept", len(env.Error.Providers))
	}
}

func TestEpisodesFromFallbackUnderEZTVSaysTheyWillNotPlayViaEZTV(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	primary := twoSeasonStub()
	primary.episodesErr = fmt.Errorf("eztv: no torrents for eztv/1 (%w)", provider.ErrNotCarried)
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
	if !strings.Contains(w[0].Message, "will not play via EZTV") {
		t.Errorf("message = %q, want it to say the listed episodes will not play via EZTV", w[0].Message)
	}
}

func TestEpisodesFromFallbackUnderOtherBasesDoesNotMentionEZTV(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "animeonsen")

	primary := twoSeasonStub()
	primary.episodesErr = fmt.Errorf("animeonsen: no episodes for animeonsen/1 (%w)", provider.ErrNotCarried)
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "animeonsen")

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
	if len(w) != 1 || strings.Contains(w[0].Message, "EZTV") {
		t.Fatalf("warnings = %+v, want the generic fallback warning with no EZTV claim", w)
	}
}

func TestEpisodesFlagsAPartialEZTVCatalogue(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	gap := fmt.Errorf("%w: eztv carries only some episodes of season 2 of eztv/1; episodes 1 is not on EZTV", provider.ErrIncompleteEpisodeList)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "2", Number: 2}},
		seasonsErr:      fmt.Errorf("%w: eztv carries only some seasons of eztv/1; season 1 is not on EZTV", provider.ErrIncompleteEpisodeList),
		episodesWithErr: []media.Episode{{ID: "2x2", Number: 2, Title: "Two"}},
		episodesErr:     gap,
	}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	codes := map[string]bool{}
	for _, w := range decodeEpisodesWarnings(t, buf.Bytes()) {
		codes[w.Code] = true
	}
	if !codes["season_list_incomplete"] || !codes["episode_list_incomplete"] {
		t.Fatalf("warning codes = %v, want season_list_incomplete and episode_list_incomplete", codes)
	}
}

func TestEpisodesFromFallbackForAnotherSeasonDoesNotClaimTheyWillNotPlayViaEZTV(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s2", Number: 2}, {ID: "s3", Number: 3}},
		episodesErr: fmt.Errorf("eztv: no standalone release for eztv/1 S02E01 (%w)", provider.ErrNotCarried),
	}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")

	fb := twoSeasonStub()
	fb.seasons = []media.Season{{ID: "s1", Number: 1}}
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return []provider.Provider{fb} }
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 0)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	if len(w) != 1 || w[0].Code != "episode_list_from_fallback" {
		t.Fatalf("warnings = %+v, want one episode_list_from_fallback", w)
	}
	if strings.Contains(w[0].Message, "will not play via EZTV") {
		t.Errorf("message = %q: EZTV was only checked for season 2, the list shown is season 1", w[0].Message)
	}
}
