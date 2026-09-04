package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// A TV ref without both season and episode would fall through to the
// interactive season picker, which hangs. Refuse it with a clear message
// instead.
func TestPlayRejectsTVWithoutSeasonAndEpisode(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	ref, err := encodeRef(playRef{ID: "tv/show", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = ref, 0, 0
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })

	if err := playRun(playCmd, nil); err == nil {
		t.Fatal("playRun accepted a TV ref with no season/episode, want an error")
	}
}

// The ref's title and year must reach the playback path. If they are dropped,
// the resolver searches for "" and ranking collapses — which plays the wrong
// film rather than failing.
func TestPlayPassesFullSelectionThrough(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	var got media.SearchResult
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(p provider.Provider, sel media.SearchResult, season, episode int) error {
		got = sel
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{
		ID: "movie/watch-the-matrix-19724", Title: "The Matrix", Year: "1999", Type: "movie",
	})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if got.Title != "The Matrix" {
		t.Fatalf("Title = %q, want The Matrix", got.Title)
	}
	if got.Year != "1999" {
		t.Fatalf("Year = %q, want 1999 (the resolver ranks on it)", got.Year)
	}
	if got.ID != "movie/watch-the-matrix-19724" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Type != media.Movie {
		t.Fatalf("Type = %v, want Movie", got.Type)
	}
}

// A total resolution failure is exit 3, so the agent knows to run doctor
// rather than suggest a spelling fix.
func TestPlayResolutionFailureExitsThree(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		return fmt.Errorf("all providers failed")
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	err = playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitProvidersFailed {
		t.Fatalf("exit code = %d, want %d", ee.code, exitProvidersFailed)
	}
}

// An unavailable configured player must exit 4 (player_unavailable), not 3
// (providers_failed): the two call for completely different advice ("install
// mpv" vs. "try again later"). Before the fix, playRun's catch-all mapped
// every resolveAndPlay error, including player.NotFoundError, to exit 3.
func TestPlayUnavailablePlayerExitsFour(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	called := false
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		called = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return false, "mpv" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	err = playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitPlayerUnavailable {
		t.Fatalf("exit code = %d, want %d", ee.code, exitPlayerUnavailable)
	}
	if called {
		t.Fatal("playback was dispatched despite the player being reported unavailable")
	}
}

// The precondition check must not reject the happy path: an available player
// still reaches playback normally.
func TestPlayAvailablePlayerReachesPlayback(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevCfg := cfg
	cfg = &config.Config{Player: "mpv"}
	t.Cleanup(func() { cfg = prevCfg })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "mpv" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	called := false
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		called = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !called {
		t.Fatal("playback was not dispatched though the player was reported available")
	}
}

// withBaseFlag forces the same persistent-flag merge cobra performs before
// RunE during a real Execute() (see (*cobra.Command).mergePersistentFlags),
// so that cmd.Flags().Changed("base") reflects reality even though these
// tests call playRun directly rather than going through Execute(), and
// restores the flag's value and Changed bit afterwards.
func withBaseFlag(t *testing.T) {
	t.Helper()
	withInheritedFlags(t, playCmd, "base")
}

// A ref found under `--base yts` is meaningless resolved against another
// base: the ID's exact-match bonus is provider-specific. play must apply the
// ref's Base when the user did not explicitly override it on this
// invocation.
func TestPlayHonorsRefBaseWhenNotOverridden(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withBaseFlag(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	var seenBase string
	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		seenBase = cfg.Base
		return &stubProvider{}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error { return nil }
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "yts/1", Title: "X", Type: "movie", Base: "yts"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if seenBase != "yts" {
		t.Fatalf("cfg.Base seen by agentProvider = %q, want yts (the ref's base)", seenBase)
	}
}

// An explicit --base on the command line is a deliberate user override and
// must win over the ref's stored base.
func TestPlayExplicitBaseFlagOverridesRef(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withBaseFlag(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	if err := playCmd.Flags().Set("base", "moviebox"); err != nil {
		t.Fatalf("Set --base: %v", err)
	}
	cfg.Base = "moviebox" // mirrors loadConfig applying the explicit flag

	var seenBase string
	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		seenBase = cfg.Base
		return &stubProvider{}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error { return nil }
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "yts/1", Title: "X", Type: "movie", Base: "yts"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if seenBase != "moviebox" {
		t.Fatalf("cfg.Base seen by agentProvider = %q, want moviebox (the explicit --base)", seenBase)
	}
}

// A ref with no stored Base (e.g. an older ref, or one built without cfg) must
// leave cfg.Base exactly as configured.
func TestPlayLeavesConfigBaseUntouchedWhenRefBaseEmpty(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	withBaseFlag(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	var seenBase string
	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		seenBase = cfg.Base
		return &stubProvider{}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error { return nil }
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"}) // no Base
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if seenBase != "flixhq.ws" {
		t.Fatalf("cfg.Base seen by agentProvider = %q, want flixhq.ws (untouched)", seenBase)
	}
}

// playHarness wires the seams every season/episode validation test needs: an
// available player, a stub provider, a recording (non-launching) playback
// entry point, and the play flags. It returns a pointer to the "did playback
// run" flag so a test can assert dispatch happened or did not.
func playHarness(t *testing.T, p *stubProvider, ref string, season, episode int) *bool {
	t.Helper()
	hostileEnv(t)
	captureAgentOut(t)

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return p }
	t.Cleanup(func() { agentProvider = prevProv })

	dispatched := false
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		dispatched = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = ref, season, episode
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })

	return &dispatched
}

// showRef builds a TV ref for the validation tests.
func showRef(t *testing.T) string {
	t.Helper()
	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	return ref
}

// resolveAndPlay initialises seasonIdx/episodeIdx to 0 and only overwrites
// them on an exact Number match, so a season the show does not have silently
// played S1E1 and reported success. episodes already answers exit 2 for the
// same input; the two commands must not disagree about whether a season
// exists.
func TestPlayUnknownSeasonExitsTwo(t *testing.T) {
	dispatched := playHarness(t, twoSeasonStub(), showRef(t), 5, 12)

	err := playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
	if *dispatched {
		t.Fatal("playback was dispatched for a season the show does not have (this is the S1E1 bug)")
	}
}

// The same hole exists one level down: a valid season with a non-existent
// episode played that season's first episode.
func TestPlayUnknownEpisodeExitsTwo(t *testing.T) {
	dispatched := playHarness(t, twoSeasonStub(), showRef(t), 2, 12)

	err := playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
	if *dispatched {
		t.Fatal("playback was dispatched for an episode the season does not have")
	}
}

// Validation must not reject what does exist.
func TestPlayValidSeasonEpisodeReachesPlayback(t *testing.T) {
	p := twoSeasonStub()
	dispatched := playHarness(t, p, showRef(t), 2, 2)

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !*dispatched {
		t.Fatal("a season and episode that both exist did not reach playback")
	}
	if p.lastSeasonID != "s2" {
		t.Fatalf("validation looked up season %q, want s2", p.lastSeasonID)
	}
}

// A primary provider that cannot enumerate seasons must NOT be turned into a
// verdict. resolveAndPlay has a purpose-built branch for exactly this state
// (cmd/search.go:237, `if err != nil || len(seasons) == 0`) that resolves by
// title/year/season/episode across the whole fallback chain and plays. A ref
// whose ID actually came from a fallback provider still carries the primary's
// base (find stamps cfg.Base on every result), so this is the normal case
// whenever the primary is degraded — which is this repo's normal state.
// Validating here must not make that branch unreachable.
func TestPlaySeasonLookupErrorFallsThroughToResolver(t *testing.T) {
	p := twoSeasonStub()
	p.seasonsErr = errors.New("unexpected status 404")
	dispatched := playHarness(t, p, showRef(t), 2, 1)

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun returned %v; a primary that cannot answer must fall through to the resolver's fallback branch, not become a verdict", err)
	}
	if !*dispatched {
		t.Fatal("playback was not dispatched; the resolver's fallback-stream branch is unreachable")
	}
}

// The empty list is the same state as the error: cmd/search.go:237 treats
// `err != nil || len(seasons) == 0` identically, and the old code funnelled
// the empty case into !found and answered exit 2 — "no such season" for a show
// whose seasons simply were not enumerable.
func TestPlayEmptySeasonListFallsThroughToResolver(t *testing.T) {
	p := &stubProvider{} // GetSeasons returns nil, nil
	dispatched := playHarness(t, p, showRef(t), 2, 1)

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun returned %v; an empty season list means \"cannot validate\", not \"no such season\"", err)
	}
	if !*dispatched {
		t.Fatal("playback was not dispatched for an unenumerable show")
	}
}

// Same reasoning one level down: an error fetching episodes means the primary
// cannot answer, so let the resolver decide rather than pre-empting it.
func TestPlayEpisodeLookupErrorFallsThroughToResolver(t *testing.T) {
	p := twoSeasonStub()
	p.episodesErr = errors.New("unexpected status 404")
	dispatched := playHarness(t, p, showRef(t), 2, 1)

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun returned %v; a failed episode lookup must fall through to the resolver", err)
	}
	if !*dispatched {
		t.Fatal("playback was not dispatched though only the primary's episode lookup failed")
	}
}

// A film has no seasons. Validation must not run for it — a provider whose
// GetSeasons errors would otherwise turn every movie play into exit 3.
func TestPlayMovieSkipsSeasonValidation(t *testing.T) {
	p := &stubProvider{seasonsErr: errors.New("GetSeasons must not be called for a film")}
	ref, err := encodeRef(playRef{ID: "movie/x", Title: "A Film", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	dispatched := playHarness(t, p, ref, 0, 0)

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !*dispatched {
		t.Fatal("a movie ref did not reach playback")
	}
}

// Validation must happen before the --detach dispatch. Otherwise the parent
// forks a supervisor, waits one second, and reports success while the child
// exits 2 into a log the caller has no reason to read. If it did reach
// playDetached, os.Executable()/exec.Command would run for real (no seam
// exists) and spawn a process this test does not permit.
func TestPlayDetachedRejectsUnknownSeasonBeforeSpawning(t *testing.T) {
	dispatched := playHarness(t, twoSeasonStub(), showRef(t), 5, 1)

	prevD, prevS := flagDetach, flagSupervised
	flagDetach, flagSupervised = true, false
	t.Cleanup(func() { flagDetach, flagSupervised = prevD, prevS })

	err := playRun(playCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("playRun returned %T (%v), want *exitError", err, err)
	}
	if ee.code != exitNoResults {
		t.Fatalf("exit code = %d, want %d", ee.code, exitNoResults)
	}
	if *dispatched {
		t.Fatal("playback was dispatched despite an unknown season")
	}
}

// The decode-time type check, seen from the command: a series ref whose Type
// was lost must never reach playback. Without it the ref reads as a movie,
// season/episode are not required, and resolveAndPlay is handed season 0.
func TestPlayRejectsRefWithUnknownType(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	played := false
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		played = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	withStubProvider(t, twoSeasonStub())

	ref, err := encodeRef(playRef{ID: "tv/show-1", Title: "Some Show", Type: ""})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevSeason, prevEpisode := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = ref, 0, 0
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevSeason, prevEpisode })

	if err := playRun(playCmd, nil); err == nil {
		t.Fatal("playRun accepted a ref with no type, want a bad_ref error")
	}
	if played {
		t.Fatal("resolveAndPlay was reached with an untyped ref")
	}
}

// playMovieRefForContinue wires the stubs a continue-defaulting test needs:
// a stub provider, a stubbed resolve-and-play that records flagContinue as
// seen at dispatch time, and a movie ref in flagRef. Returns a pointer to
// the recorded value.
func playMovieRefForContinue(t *testing.T) *bool {
	t.Helper()
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return &stubProvider{} }
	t.Cleanup(func() { agentProvider = prevProv })

	seen := new(bool)
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error {
		*seen = flagContinue
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef := flagRef
	flagRef = ref
	t.Cleanup(func() { flagRef = prevRef })

	prevCont := flagContinue
	t.Cleanup(func() { flagContinue = prevCont })

	return seen
}

// The agent play command must resume from history by default: nothing in the
// JSON envelope tells a caller to pass -c, and "resume_tracking":true reads
// as a promise that it will. When --continue was not passed on this
// invocation, playRun must behave as if it were.
func TestPlayDefaultsContinueOn(t *testing.T) {
	seen := playMovieRefForContinue(t)
	withInheritedFlags(t, playCmd, "continue") // restores the Changed bit

	flagContinue = false // cobra's default when the flag is not passed

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !*seen {
		t.Fatal("flagContinue was false at playback; play must resume from history by default")
	}
}

// An explicit --continue=false is a deliberate fresh start and must survive
// the defaulting.
func TestPlayExplicitContinueFalseForcesFreshStart(t *testing.T) {
	seen := playMovieRefForContinue(t)
	withInheritedFlags(t, playCmd, "continue")

	if err := playCmd.Flags().Set("continue", "false"); err != nil {
		t.Fatalf("Set --continue=false: %v", err)
	}

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if *seen {
		t.Fatal("flagContinue was true at playback despite an explicit --continue=false")
	}
}
