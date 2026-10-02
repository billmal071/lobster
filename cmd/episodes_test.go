package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// An agent cannot guess episode numbers, so it needs a listing — and that
// listing must not prompt.
func TestEpisodesListsWithoutPrompting(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		return &stubProvider{
			seasons:  []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
			episodes: []media.Episode{{ID: "e1", Number: 1, Title: "Pilot"}},
		}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	prevSeason := flagSeason
	flagSeason = 1
	t.Cleanup(func() { flagSeason = prevSeason })

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Schema   int `json:"schema"`
		Episodes []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Number != 1 || got.Episodes[0].Title != "Pilot" {
		t.Fatalf("episodes = %+v", got.Episodes)
	}
}

// Asking for episodes of a film is a caller mistake worth naming clearly.
func TestEpisodesRejectsMovieRef(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "A Film", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := episodesRun(episodesCmd, nil); err == nil {
		t.Fatal("episodesRun accepted a movie ref, want an error")
	}
}

// tvRef is a TV ref carrying an optional originating base.
func tvRef(t *testing.T, base string) string {
	t.Helper()
	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv", Base: base})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	return ref
}

// twoSeasonStub answers with two seasons whose episode lists differ, so a
// command that ignores the requested season is distinguishable from one that
// honours it.
func twoSeasonStub() *stubProvider {
	return &stubProvider{
		seasons: []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
		episodesBySeason: map[string][]media.Episode{
			"s1": {{ID: "e1", Number: 1, Title: "Pilot"}},
			"s2": {{ID: "e2", Number: 1, Title: "Return"}, {ID: "e3", Number: 2, Title: "Fallout"}},
		},
	}
}

// withEpisodesFlags sets --ref/--season for the duration of the test.
func withEpisodesFlags(t *testing.T, ref string, season int) {
	t.Helper()
	prevRef, prevSeason := flagRef, flagSeason
	flagRef, flagSeason = ref, season
	t.Cleanup(func() { flagRef, flagSeason = prevRef, prevSeason })
}

// withStubProvider installs p as the provider the agent commands build.
func withStubProvider(t *testing.T, p *stubProvider) {
	t.Helper()
	prev := agentProvider
	agentProvider = func() provider.Provider { return p }
	t.Cleanup(func() { agentProvider = prev })
}

// --season must select that season's episodes, not season 1's. Covered only
// now that stubProvider.GetEpisodes honours its seasonID.
func TestEpisodesSelectsRequestedSeason(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	p := twoSeasonStub()
	withStubProvider(t, p)
	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if p.lastSeasonID != "s2" {
		t.Fatalf("GetEpisodes was asked for season %q, want s2", p.lastSeasonID)
	}

	var got struct {
		Season   int `json:"season"`
		Episodes []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Season != 2 {
		t.Fatalf("season = %d, want 2", got.Season)
	}
	if len(got.Episodes) != 2 || got.Episodes[0].Title != "Return" {
		t.Fatalf("episodes = %+v, want season 2's list", got.Episodes)
	}
}

// A season the show does not have is "no such thing", not "sources down".
func TestEpisodesUnknownSeasonExitsTwo(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	withStubProvider(t, twoSeasonStub())
	// A season the source does not have now sends the command to the chain,
	// since a season list can undercount the show (seasonAcrossHits). Empty it:
	// the real chain reaches the network, and this test took 2.18s the moment
	// that lookup was added.
	withNoFallbackProviders(t)
	withEpisodesFlags(t, tvRef(t, ""), 5)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("episodesRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
}

// withNoFallbackProviders empties the fallback chain for the duration of the
// test. Required wherever the primary cannot enumerate seasons: seasonSource
// then re-searches the chain, and the real chain reaches the network.
func withNoFallbackProviders(t *testing.T) {
	t.Helper()
	prev := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return nil }
	t.Cleanup(func() { agentFallbackProviders = prev })
}

// A provider that errors is exit 3, so the agent runs doctor rather than
// suggesting a different season number.
func TestEpisodesProviderErrorExitsThree(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	p := twoSeasonStub()
	p.seasonsErr = errors.New("upstream 503")
	withStubProvider(t, p)
	withNoFallbackProviders(t)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("episodesRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitProvidersFailed {
		t.Fatalf("exit code = %d, want %d", ee.code, exitProvidersFailed)
	}
}

// The episode listing can fail on its own, after seasons resolved fine.
func TestEpisodesEpisodeFetchErrorExitsThree(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	p := twoSeasonStub()
	p.episodesErr = errors.New("upstream 503")
	withStubProvider(t, p)
	// An episode-listing failure now re-searches the chain (a primary can
	// enumerate seasons and not episodes), so the chain must be stubbed here
	// too or this test reaches the network.
	withNoFallbackProviders(t)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("episodesRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitProvidersFailed {
		t.Fatalf("exit code = %d, want %d", ee.code, exitProvidersFailed)
	}
}

// A ref carries the base it was found under. play honours it; episodes must
// too, or a `find --base flixhq.ws` ref lists fine under play and fails under
// episodes with "no seasons found" because a MovieBox provider was handed a
// FlixHQ ID.
func TestEpisodesHonorsRefBaseWhenNotOverridden(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withInheritedFlags(t, episodesCmd, "base")

	prevCfg := cfg
	cfg = &config.Config{Base: "moviebox"}
	t.Cleanup(func() { cfg = prevCfg })

	var seenBase string
	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		seenBase = cfg.Base
		return twoSeasonStub()
	}
	t.Cleanup(func() { agentProvider = prevProv })

	withEpisodesFlags(t, tvRef(t, "flixhq.ws"), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if seenBase != "flixhq.ws" {
		t.Fatalf("cfg.Base seen by agentProvider = %q, want flixhq.ws (the ref's base)", seenBase)
	}
}

// An explicit --base is a deliberate override and must beat the ref's base,
// exactly as it does for play.
func TestEpisodesExplicitBaseFlagOverridesRef(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withInheritedFlags(t, episodesCmd, "base")

	prevCfg := cfg
	cfg = &config.Config{Base: "moviebox"}
	t.Cleanup(func() { cfg = prevCfg })

	if err := episodesCmd.Flags().Set("base", "moviebox"); err != nil {
		t.Fatalf("Set --base: %v", err)
	}

	var seenBase string
	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		seenBase = cfg.Base
		return twoSeasonStub()
	}
	t.Cleanup(func() { agentProvider = prevProv })

	withEpisodesFlags(t, tvRef(t, "flixhq.ws"), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if seenBase != "moviebox" {
		t.Fatalf("cfg.Base seen by agentProvider = %q, want moviebox (the explicit --base)", seenBase)
	}
}

// The two commands must agree on what a single ref means. If they disagree,
// the agent's own workflow breaks in the middle: episodes lists nothing for a
// ref that play then happily plays.
func TestPlayAndEpisodesAgreeOnRefBase(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withInheritedFlags(t, playCmd, "base")
	withInheritedFlags(t, episodesCmd, "base")

	ref := tvRef(t, "flixhq.ws")

	seen := map[string]string{}
	record := func(name string) func() provider.Provider {
		return func() provider.Provider {
			seen[name] = cfg.Base
			return twoSeasonStub()
		}
	}

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error { return nil }
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	prevProv := agentProvider
	t.Cleanup(func() { agentProvider = prevProv })

	prevCfg := cfg
	t.Cleanup(func() { cfg = prevCfg })

	cfg = &config.Config{Base: "moviebox"}
	withEpisodesFlags(t, ref, 1)
	agentProvider = record("episodes")
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	cfg = &config.Config{Base: "moviebox"}
	prevEpisode := flagEpisode
	flagEpisode = 1
	t.Cleanup(func() { flagEpisode = prevEpisode })
	agentProvider = record("play")
	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}

	if seen["episodes"] != seen["play"] {
		t.Fatalf("episodes resolved against base %q but play used %q; the same ref must mean the same thing", seen["episodes"], seen["play"])
	}
	if seen["play"] != "flixhq.ws" {
		t.Fatalf("both commands used base %q, want the ref's flixhq.ws", seen["play"])
	}
}

// find searches the fallback chain as well as the primary (gatherSearchResults)
// but stamps the primary's base on every ref, so a ref's ID can belong to a
// provider that is not the one episodes will ask. Handing a foreign ID to the
// primary yields no seasons — and episodes reported "no seasons found" for a
// show play resolves and plays without trouble, because resolveAndPlay
// re-searches by title across the chain (cmd/search.go). episodes must do the
// same or the two commands disagree about what one ref means.
func TestEpisodesFallsBackWhenPrimaryCannotEnumerate(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary does not index this ID: no error, just nothing.
	withStubProvider(t, &stubProvider{})

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{
		{ID: "fb/some-show", Title: "Some Show", Type: media.TV},
	}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Seasons  []int `json:"seasons"`
		Season   int   `json:"season"`
		Episodes []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Seasons) != 2 {
		t.Fatalf("seasons = %v, want the fallback provider's two", got.Seasons)
	}
	if got.Season != 2 || len(got.Episodes) != 2 || got.Episodes[0].Title != "Return" {
		t.Fatalf("season = %d, episodes = %+v; want the fallback's season 2", got.Season, got.Episodes)
	}
}

// The fallback must be looked up under the ref's own title, and it must not
// silently accept a provider that answers about a different show.
//
// resolver.Candidates ranks but does not threshold — a score-0 result is still
// returned — so "the first candidate whose GetSeasons is non-empty" accepts a
// provider that answered about something else entirely. The failure is silent
// and undetectable by the caller: the envelope echoes the ref's own title, so
// the agent sees "Some Show" with a different show's seasons and episodes.
// Listing the wrong work without failing loudly is the exact failure the ref
// design exists to prevent.
func TestEpisodesFallbackRefusesADifferentShow(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	withStubProvider(t, &stubProvider{})

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{
		{ID: "fb/totally-different", Title: "Completely Different Show", Year: "1999", Type: media.TV},
	}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 1)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("episodesRun returned %T (%v) for an unrelated fallback match, want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
}

// A fallback that simply does not carry the title at all is exit 2 too.
func TestEpisodesFallbackIgnoresUnrelatedFallbackResults(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	withStubProvider(t, &stubProvider{})

	fb := twoSeasonStub()
	fb.results = nil // the fallback does not have this title at all
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 1)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("episodesRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
}

// blockingProvider is a fallback that hangs in Search until its channel is
// closed. provider.Provider takes no context, so this is what a wedged
// upstream looks like from inside lobster: a call that simply does not return.
type blockingProvider struct {
	*stubProvider
	block chan struct{}
}

func (b *blockingProvider) Search(string) ([]media.SearchResult, error) {
	<-b.block
	return nil, nil
}

// newBlockingProvider returns a provider wedged in Search, released either by
// the test's cleanup or after grace — whichever comes first. The grace release
// is what keeps the test an assertion failure rather than a hang if the scan is
// ever unbounded again.
func newBlockingProvider(t *testing.T, grace time.Duration) *blockingProvider {
	t.Helper()
	b := &blockingProvider{stubProvider: &stubProvider{}, block: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(b.block) }) }
	timer := time.AfterFunc(grace, release)
	t.Cleanup(func() {
		timer.Stop()
		release()
	})
	return b
}

// The fallback scan must be bounded. It fans out over the whole chain — up to
// eleven providers, each a Search plus several GetSeasons calls against a 30s
// HTTP client timeout — and episodes is one of the commands whose entire
// premise is that an agent is never left waiting. find bounds the same fan-out
// at multiSearchTimeout (cmd/multisearch.go); this must too, or one wedged
// provider turns a one-call command into minutes of silence.
func TestEpisodesFallbackScanIsBounded(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	prevTimeout := episodesFallbackTimeout
	episodesFallbackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { episodesFallbackTimeout = prevTimeout })

	withStubProvider(t, &stubProvider{})

	// Wedged provider first in chain order, so a scan that waits for it in turn
	// cannot reach the healthy one until it gives up.
	blocked := newBlockingProvider(t, 3*time.Second)
	healthy := twoSeasonStub()
	healthy.results = []media.SearchResult{
		{ID: "fb/some-show", Title: "Some Show", Type: media.TV},
	}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{blocked, healthy}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 1)

	start := time.Now()
	err := episodesRun(episodesCmd, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("episodesRun took %v with one wedged provider; the scan is not bounded by episodesFallbackTimeout (%v)", elapsed, episodesFallbackTimeout)
	}

	var got struct {
		Episodes []struct {
			Title string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Title != "Pilot" {
		t.Fatalf("episodes = %+v, want the healthy fallback's season 1", got.Episodes)
	}
}

// slowListerProvider enumerates seasons at once and then wedges in
// GetEpisodes, which is the shape a degraded upstream actually has: the season
// list is often cached or comes from search data while the episode listing is
// a fresh request against a 30s HTTP client timeout.
type slowListerProvider struct {
	*stubProvider
	delay time.Duration
}

func (p *slowListerProvider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	time.Sleep(p.delay)
	return p.stubProvider.GetEpisodes(id, seasonID)
}

// Every other chain call in episodes.go goes through seasonsWithContext or
// episodesWithContext, and one did not: the first GetEpisodes, made directly on
// whichever provider seasonSource picked. When that provider came from the
// chain rather than from the primary, the call had no deadline at all — with
// the real 5s scan deadline and a 30s HTTP timeout, `episodes` could sit ~30s
// on it. Agent-facing commands must stay bounded.
func TestEpisodesFirstListingCallIsBoundedForAChainProvider(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevTimeout := episodesFallbackTimeout
	episodesFallbackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { episodesFallbackTimeout = prevTimeout })

	// The primary cannot enumerate seasons, so the answering provider — and
	// therefore that first GetEpisodes call — comes from the chain.
	withStubProvider(t, &stubProvider{seasonsErr: errProviderCannotList})

	slow := &slowListerProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/slow-1", Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "s1", Number: 1}},
			episodesBySeason: map[string][]media.Episode{
				"s1": {{ID: "s1e1", Number: 1, Title: "Pilot"}},
			},
		},
		delay: 2 * time.Second,
	}
	withFallbackChain(t, slow)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	start := time.Now()
	_ = episodesRun(episodesCmd, nil)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("episodesRun took %v with a chain provider slow to list episodes; that call is not bounded by episodesFallbackTimeout (%v)", elapsed, episodesFallbackTimeout)
	}
}

// A provider that returns a list it knows may be short gets the list printed
// and a warning alongside it.
//
// The two halves are the test. Dropping the list would cost the caller a
// measured answer; printing it without the warning is the bug — the AnimeOnsen
// probe enumerates by HEADing manifests, and a run that stopped at its ceiling
// or could not confirm where the series ends was indistinguishable in this
// envelope from one that counted the season exactly.
func TestEpisodesWarnsWhenTheProviderCouldNotFinishEnumerating(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	p := twoSeasonStub()
	p.episodesWithErr = []media.Episode{
		{ID: "1", Number: 1, Title: "Episode 1"},
		{ID: "2", Number: 2, Title: "Episode 2"},
	}
	p.episodesErr = fmt.Errorf("%w: stopped at 2", provider.ErrIncompleteEpisodeList)
	withStubProvider(t, p)
	withNoFallbackProviders(t)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
		Warnings []struct {
			Code           string `json:"code"`
			Provider       string `json:"provider"`
			EpisodesListed int    `json:"episodes_listed"`
			Message        string `json:"message"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 2 {
		t.Fatalf("episodes = %+v, want the 2 the provider measured", got.Episodes)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one; a partial list that reports itself as complete is the whole bug", got.Warnings)
	}
	w := got.Warnings[0]
	// Written out, not read from the code: a test that compares against the
	// same string literal the implementation emits would pass for any code.
	if w.Code != "episode_list_incomplete" {
		t.Fatalf("warning code = %q, want %q", w.Code, "episode_list_incomplete")
	}
	if w.EpisodesListed != 2 {
		t.Fatalf("warning episodes_listed = %d, want 2", w.EpisodesListed)
	}
	if w.Message == "" {
		t.Fatalf("warning carries no message: %+v", w)
	}
}

// The ordinary case stays silent. A warning on a complete list is noise, and
// noise is what trains a caller to ignore the one that matters.
func TestEpisodesDoesNotWarnWhenTheListIsComplete(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	withStubProvider(t, twoSeasonStub())
	withNoFallbackProviders(t)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if _, ok := got["warnings"]; ok {
		t.Fatalf("a complete listing carried warnings: %q", buf.String())
	}
}

// The flag travels with a list that came from the fallback chain, not only
// with the primary's.
//
// A primary that cannot enumerate is the common path for an anime ref (every
// anime source here is a separate catalogue entry per season), so the provider
// whose enumeration stopped short is usually a chain member. Carrying the
// warning on one route and dropping it on the other would mean the envelope's
// honesty depended on which provider happened to answer.
func TestEpisodesWarnsWhenAFallbackProviderCouldNotFinishEnumerating(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary does not index this ID: no error, just nothing.
	withStubProvider(t, &stubProvider{})

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{
		{ID: "fb/some-show", Title: "Some Show", Type: media.TV},
	}
	fb.episodesWithErr = []media.Episode{{ID: "1", Number: 1, Title: "Episode 1"}}
	fb.episodesErr = fmt.Errorf("%w: stopped at 1", provider.ErrIncompleteEpisodeList)
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	var got struct {
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
		Warnings []struct {
			Code           string `json:"code"`
			EpisodesListed int    `json:"episodes_listed"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 1 {
		t.Fatalf("episodes = %+v, want the 1 the fallback measured", got.Episodes)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Code != "episode_list_incomplete" || got.Warnings[0].EpisodesListed != 1 {
		t.Fatalf("warnings = %+v, want one episode_list_incomplete over 1 episode", got.Warnings)
	}
}

// The flag survives the second hop too: the chain member that ends up
// supplying the list is not always the one the season scan picked.
//
// seasonSource chooses a hit on "can you enumerate seasons?", and the real
// chain leads with two providers that answer that and not "can you enumerate
// episodes?" (VidNest, MovieBox). So firstEpisodeList is the route a list
// usually arrives by, and it carries its own copy of the flag.
func TestEpisodesWarnsWhenTheSecondChainHopCouldNotFinishEnumerating(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	withStubProvider(t, &stubProvider{})

	hit := []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	// First in chain order: enumerates seasons, cannot list episodes at all.
	seasonsOnly := twoSeasonStub()
	seasonsOnly.results = hit
	seasonsOnly.episodesErr = errors.New("upstream 503")
	// Second: lists a prefix and says it is one.
	partial := twoSeasonStub()
	partial.results = hit
	partial.episodesWithErr = []media.Episode{{ID: "1", Number: 1, Title: "Episode 1"}}
	partial.episodesErr = fmt.Errorf("%w: stopped at 1", provider.ErrIncompleteEpisodeList)

	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{seasonsOnly, partial}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	var got struct {
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
		Warnings []struct {
			Code           string `json:"code"`
			EpisodesListed int    `json:"episodes_listed"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 1 {
		t.Fatalf("episodes = %+v, want the 1 the second chain hit measured", got.Episodes)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Code != "episode_list_incomplete" || got.Warnings[0].EpisodesListed != 1 {
		t.Fatalf("warnings = %+v, want one episode_list_incomplete over 1 episode", got.Warnings)
	}
}

// withStubProviderBase maps a stub onto a --base token, so the base-aware
// warnings can be exercised without a concrete provider that reaches the net.
func withStubProviderBase(t *testing.T, want provider.Provider, token string) {
	t.Helper()
	prev := searchProviderBase
	searchProviderBase = func(p provider.Provider) string {
		if p == want {
			return token
		}
		return prev(p)
	}
	t.Cleanup(func() { searchProviderBase = prev })
}

// episodesWarnings decodes just the warnings array.
type episodesWarning struct {
	Code           string `json:"code"`
	Base           string `json:"base"`
	Provider       string `json:"provider"`
	EpisodesListed int    `json:"episodes_listed"`
	Message        string `json:"message"`
}

func decodeEpisodesWarnings(t *testing.T, b []byte) []episodesWarning {
	t.Helper()
	var got struct {
		Warnings []episodesWarning `json:"warnings"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, string(b))
	}
	return got.Warnings
}

// The silent downgrade, which is the shape of the reported bug: the requested
// base could not enumerate, a chain member answered instead, and the envelope
// said only who answered — never that anyone else had been asked. The chain
// member's list was shorter than the truth and it cannot stream a frame of it,
// and exit 0 with a plausible list is indistinguishable from a good answer.
func TestEpisodesWarnsWhenTheRequestedBaseDidNotAnswer(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "animeonsen")

	// The primary enumerates seasons and cannot list episodes — the shape a
	// throttled AnimeOnsen probe produced.
	primary := twoSeasonStub()
	primary.episodesErr = errors.New("episodes: animeonsen: manifest status 429")
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "animeonsen")

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	if len(w) != 1 || w[0].Code != "episode_list_from_fallback" {
		t.Fatalf("warnings = %+v, want one episode_list_from_fallback", w)
	}
	// Written out rather than read back from cfg, which the code also reads.
	if w[0].Base != "animeonsen" {
		t.Fatalf("warning base = %q, want animeonsen", w[0].Base)
	}
	if w[0].EpisodesListed != 2 {
		t.Fatalf("warning episodes_listed = %d, want 2", w[0].EpisodesListed)
	}
}

// Silence on a clean answer. The requested base answered, so there is nothing
// to say, and a warning on every listing is noise a caller learns to ignore.
func TestEpisodesSaysNothingWhenTheRequestedBaseAnswered(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "animeonsen")

	primary := twoSeasonStub()
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "animeonsen")
	withEpisodesFlags(t, tvRef(t, ""), 2)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if w := decodeEpisodesWarnings(t, buf.Bytes()); len(w) != 0 {
		t.Fatalf("warnings = %+v, want none", w)
	}
}

// Silence under "auto": broadening is then the documented behaviour rather
// than a departure from what was asked, which is the rule cmd/find.go's
// baseBroadeningWarnings already follows.
func TestEpisodesSaysNothingAboutFallbackUnderAutoBase(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, config.BaseAuto)

	primary := twoSeasonStub()
	primary.episodesErr = errors.New("nope")
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "animeonsen")

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	if w := decodeEpisodesWarnings(t, buf.Bytes()); len(w) != 0 {
		t.Fatalf("warnings = %+v, want none under base auto", w)
	}
}

// Both warnings can be true at once, and both have to be emitted: the chain
// answered *and* its answer is short. Collapsing to one would hide whichever
// the caller most needed.
func TestEpisodesWarnsAboutBothFallbackAndIncompleteness(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	withBase(t, "animeonsen")

	primary := twoSeasonStub()
	primary.episodesErr = errors.New("episodes: animeonsen: manifest status 429")
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "animeonsen")

	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	fb.episodesWithErr = []media.Episode{{ID: "1", Number: 1, Title: "Episode 1"}}
	fb.episodesErr = fmt.Errorf("%w: stopped at 1", provider.ErrIncompleteEpisodeList)
	prevFB := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider {
		return []provider.Provider{fb}
	}
	t.Cleanup(func() { agentFallbackProviders = prevFB })

	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	w := decodeEpisodesWarnings(t, buf.Bytes())
	codes := map[string]bool{}
	for _, x := range w {
		codes[x.Code] = true
	}
	if len(w) != 2 || !codes["episode_list_incomplete"] || !codes["episode_list_from_fallback"] {
		t.Fatalf("warnings = %+v, want both episode_list_incomplete and episode_list_from_fallback", w)
	}
}
