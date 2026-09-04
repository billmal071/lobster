package cmd

import (
	"encoding/json"
	"errors"
	"testing"

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

// A provider that errors is exit 3, so the agent runs doctor rather than
// suggesting a different season number.
func TestEpisodesProviderErrorExitsThree(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	p := twoSeasonStub()
	p.seasonsErr = errors.New("upstream 503")
	withStubProvider(t, p)
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
