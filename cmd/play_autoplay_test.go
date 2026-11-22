package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/playlist"
	"lobster/internal/provider"
)

type playedEp struct{ season, episode int }

func autoplayStub() *stubProvider {
	return &stubProvider{
		seasons: []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}},
		episodesBySeason: map[string][]media.Episode{
			"s1": {{ID: "e11", Number: 1}, {ID: "e12", Number: 2}},
			"s2": {{ID: "e21", Number: 1}, {ID: "e22", Number: 2}},
		},
	}
}

var (
	endedNaturally = player.PlayResult{Position: 1440, Duration: 1440}
	quitEarly      = player.PlayResult{Position: 120, Duration: 1440}
)

func runLoop(t *testing.T, autoplay bool, season, episode int, outcomes []player.PlayResult, failAt int) ([]playedEp, error) {
	t.Helper()
	stub := autoplayStub()
	seasons, _ := stub.GetSeasons("x")
	seasonIdx := seasonIndex(seasons, season)
	eps, _ := stub.GetEpisodes("x", seasons[seasonIdx].ID)
	epIdx := 0
	for i, e := range eps {
		if e.Number == episode {
			epIdx = i
		}
	}
	sel := media.SearchResult{ID: "tv/show", Title: "Some Show", Type: media.TV}
	sess := playlist.NewWithProviderID(stub, sel, "x", stub, seasons, eps, seasonIdx, epIdx)

	var played []playedEp
	prevPlay := playEpisode
	playEpisode = func(s *playlist.Session) error {
		played = append(played, playedEp{s.CurrentSeason().Number, s.Current().Number})
		if failAt == len(played) {
			return errors.New("boom")
		}
		i := len(played) - 1
		if i >= len(outcomes) {
			i = len(outcomes) - 1
		}
		s.LastPosition, s.LastDuration = outcomes[i].Position, outcomes[i].Duration
		s.LastPositionUnknown = outcomes[i].PositionUnknown
		return nil
	}
	t.Cleanup(func() { playEpisode = prevPlay })

	prevAuto, prevCfg := flagAutoplay, cfg
	flagAutoplay = autoplay
	cfg = &config.Config{History: false}
	t.Cleanup(func() { flagAutoplay, cfg = prevAuto, prevCfg })

	err := runPlaybackLoop(sess)
	return played, err
}

func TestAutoplayContinuesThroughSeasonsToEndOfSeries(t *testing.T) {
	got, err := runLoop(t, true, 1, 2, []player.PlayResult{endedNaturally}, 0)
	if err != nil {
		t.Fatalf("runPlaybackLoop: %v", err)
	}
	want := []playedEp{{1, 2}, {2, 1}, {2, 2}}
	if len(got) != len(want) {
		t.Fatalf("played %v, want %v (last episode of a season rolls to the next season's E1, then stops at end of series)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("played %v, want %v", got, want)
		}
	}
}

func TestAutoplayStopsWhenPlayerQuitEarly(t *testing.T) {
	got, err := runLoop(t, true, 1, 1, []player.PlayResult{quitEarly}, 0)
	if err != nil {
		t.Fatalf("runPlaybackLoop: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("played %v after an early quit, want only the first episode", got)
	}
}

func TestAutoplayStopsWhenPositionUnknown(t *testing.T) {
	got, err := runLoop(t, true, 1, 1, []player.PlayResult{{PositionUnknown: true}}, 0)
	if err != nil {
		t.Fatalf("runPlaybackLoop: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("played %v with no measured position, want only the first episode", got)
	}
}

func TestAutoplayStopsAndReportsWhenNextEpisodeFails(t *testing.T) {
	got, err := runLoop(t, true, 1, 1, []player.PlayResult{endedNaturally}, 2)
	if err == nil {
		t.Fatal("runPlaybackLoop returned nil after the next episode failed")
	}
	if len(got) != 2 {
		t.Fatalf("played %v, want to stop after the failing second episode", got)
	}
}

func TestAutoplayReportsWhenNextSeasonCannotBeLoaded(t *testing.T) {
	stub := autoplayStub()
	seasons, _ := stub.GetSeasons("x")
	eps, _ := stub.GetEpisodes("x", "s1")
	sel := media.SearchResult{ID: "tv/show", Title: "Some Show", Type: media.TV}
	sess := playlist.NewWithProviderID(stub, sel, "x", stub, seasons, eps, 0, len(eps)-1)
	stub.episodesErr = errors.New("season listing down")

	plays := 0
	prevPlay := playEpisode
	playEpisode = func(s *playlist.Session) error {
		plays++
		s.LastPosition, s.LastDuration = endedNaturally.Position, endedNaturally.Duration
		return nil
	}
	t.Cleanup(func() { playEpisode = prevPlay })
	prevAuto, prevCfg := flagAutoplay, cfg
	flagAutoplay, cfg = true, &config.Config{History: false}
	t.Cleanup(func() { flagAutoplay, cfg = prevAuto, prevCfg })

	err := runPlaybackLoop(sess)
	if err == nil || !strings.Contains(err.Error(), "advancing to next episode") {
		t.Fatalf("err = %v, want an advancing-to-next-episode error", err)
	}
	if plays != 1 {
		t.Fatalf("played %d episodes, want 1", plays)
	}
}

func TestAutoplayFlagIsOptIn(t *testing.T) {
	f := playCmd.Flags().Lookup("autoplay")
	if f == nil {
		t.Fatal("play has no --autoplay flag")
	}
	if f.DefValue != "false" {
		t.Fatalf("--autoplay default = %q, want false", f.DefValue)
	}
}

func TestPlayFinished(t *testing.T) {
	cases := []struct {
		name string
		res  player.PlayResult
		want bool
	}{
		{"ran to the end", player.PlayResult{Position: 1440, Duration: 1440}, true},
		{"last seconds", player.PlayResult{Position: 1435, Duration: 1440}, true},
		{"closed in the credits", player.PlayResult{Position: 1400, Duration: 1440}, false},
		{"quit mid-episode", player.PlayResult{Position: 600, Duration: 1440}, false},
		{"unknown duration", player.PlayResult{Position: 600}, false},
		{"never started", player.PlayResult{}, false},
		{"position unknown but at the end", player.PlayResult{Position: 1440, Duration: 1440, PositionUnknown: true}, false},
		{"zero position, short duration", player.PlayResult{Position: 0, Duration: 5}, false},
		{"negative position, short duration", player.PlayResult{Position: -3, Duration: 5}, false},
		{"untracked player", player.PlayResult{PositionUnknown: true, PositionUntracked: true}, false},
		{"ten seconds left of a long episode", player.PlayResult{Position: 1430, Duration: 1440}, true},
		{"eleven seconds left of a long episode", player.PlayResult{Position: 1429, Duration: 1440}, false},
		{"short episode quit after one second", player.PlayResult{Position: 1, Duration: 8}, false},
		{"short episode ran to the end", player.PlayResult{Position: 8, Duration: 8}, true},
		{"one-minute episode quit eight seconds early", player.PlayResult{Position: 52, Duration: 60}, false},
		{"one-minute episode quit five seconds early", player.PlayResult{Position: 55, Duration: 60}, true},
		{"position past the reported duration", player.PlayResult{Position: 9, Duration: 8}, true},
	}
	for _, c := range cases {
		if got := playFinished(c.res); got != c.want {
			t.Errorf("%s: playFinished = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestForwardedArgsCarriesAutoplay(t *testing.T) {
	if err := playCmd.Flags().Set("autoplay", "true"); err != nil {
		t.Fatalf("Set --autoplay: %v", err)
	}
	f := playCmd.Flags().Lookup("autoplay")
	t.Cleanup(func() { f.Changed = false; _ = f.Value.Set("false") })

	if got := forwardedArgs(playCmd); !containsArg(got, "--autoplay=true") {
		t.Fatalf("forwardedArgs = %v, the detached child would drop --autoplay", got)
	}
}

func detachArgvFor(t *testing.T, refType string, season, episode int, cliAutoplay string) []string {
	t.Helper()
	withInheritedFlags(t, playCmd, "autoplay")
	f := playCmd.Flags().Lookup("autoplay")
	f.Changed = false
	_ = f.Value.Set("false")
	if cliAutoplay != "" {
		if err := playCmd.Flags().Set("autoplay", cliAutoplay); err != nil {
			t.Fatalf("Set --autoplay=%s: %v", cliAutoplay, err)
		}
	}
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = "REF", season, episode
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })
	return detachArgv(playCmd, "/usr/bin/lobster", playRef{ID: "x", Title: "X", Type: refType})
}

func TestDetachedSeriesPlayAutoplaysByDefault(t *testing.T) {
	got := detachArgvFor(t, "tv", 1, 2, "")
	if !containsArg(got, "--autoplay=true") {
		t.Fatalf("argv = %v, a detached series play must default to --autoplay=true", got)
	}
}

func TestDetachedMoviePlayDoesNotAutoplay(t *testing.T) {
	got := detachArgvFor(t, "movie", 0, 0, "")
	for _, a := range got {
		if strings.HasPrefix(a, "--autoplay") {
			t.Fatalf("argv = %v, a movie must not get %s", got, a)
		}
	}
}

func TestDetachedSeriesExplicitAutoplayFalseWins(t *testing.T) {
	got := detachArgvFor(t, "tv", 1, 2, "false")
	if !containsArg(got, "--autoplay=false") || containsArg(got, "--autoplay=true") {
		t.Fatalf("argv = %v, explicit --autoplay=false must reach the child", got)
	}
}

func TestSupervisedWithoutAutoplayEndsAfterOneEpisodeWithoutMenu(t *testing.T) {
	menuCalls := 0
	prevMenu, prevSup := postPlayMenu, flagSupervised
	postPlayMenu = func(*playlist.Session) (postPlayAction, error) {
		menuCalls++
		return actionQuit, nil
	}
	flagSupervised = true
	t.Cleanup(func() { postPlayMenu, flagSupervised = prevMenu, prevSup })

	got, err := runLoop(t, false, 1, 1, []player.PlayResult{endedNaturally}, 0)
	if err != nil {
		t.Fatalf("runPlaybackLoop: %v", err)
	}
	if len(got) != 1 || menuCalls != 0 {
		t.Fatalf("played %d episodes, menu shown %d times; want 1 and 0", len(got), menuCalls)
	}
}

func TestAttachedWithoutAutoplayStillShowsMenu(t *testing.T) {
	menuCalls := 0
	prevMenu, prevSup := postPlayMenu, flagSupervised
	postPlayMenu = func(*playlist.Session) (postPlayAction, error) {
		menuCalls++
		return actionQuit, nil
	}
	flagSupervised = false
	t.Cleanup(func() { postPlayMenu, flagSupervised = prevMenu, prevSup })

	if _, err := runLoop(t, false, 1, 1, []player.PlayResult{endedNaturally}, 0); err != nil {
		t.Fatalf("runPlaybackLoop: %v", err)
	}
	if menuCalls != 1 {
		t.Fatalf("menu shown %d times, want 1", menuCalls)
	}
}

func TestPlayDetachedSpawnsChildWithAutoplayForSeries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake supervisor is a POSIX shell script")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "lobster-fake")
	pidFile := filepath.Join(dir, "pid")
	script := "#!/bin/sh\necho $$ > '" + pidFile + "'\nprintf '%s\\n' \"$@\" > '" + out + "'\nexec sleep 30\n"
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
		}
	})
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake supervisor: %v", err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	captureAgentOut(t)
	prevExe := detachExecutable
	detachExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { detachExecutable = prevExe })

	withInheritedFlags(t, playCmd, "autoplay")
	f := playCmd.Flags().Lookup("autoplay")
	f.Changed = false
	_ = f.Value.Set("false")
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = "REF", 1, 2
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })

	if err := playDetached(playCmd, playRef{ID: "x", Title: "X", Type: "tv"}); err != nil {
		t.Fatalf("playDetached: %v", err)
	}
	var data []byte
	for i := 0; i < 50 && len(data) == 0; i++ {
		data, _ = os.ReadFile(out)
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(data), "--autoplay=true") {
		t.Fatalf("child argv = %q, playDetached must spawn a series child with --autoplay=true", data)
	}
}

func TestPlaybackSessionWarnsWhenSeasonCrossingIsPartial(t *testing.T) {
	stub := autoplayStub()
	seasons, _ := stub.GetSeasons("x")
	eps, _ := stub.GetEpisodes("x", "s1")
	stub.episodesErr = fmt.Errorf("probe budget: %w", provider.ErrIncompleteEpisodeList)
	stub.episodesWithErr = []media.Episode{{ID: "e21", Number: 1}}
	sel := media.SearchResult{ID: "tv/show", Title: "Some Show", Type: media.TV}
	sess := newPlaybackSession(stub, sel, "x", stub, seasons, eps, 0, len(eps)-1, false)

	var warned []string
	prev := warnf
	warnf = func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = prev })

	if _, err := sess.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "season 2") || !strings.Contains(warned[0], "may be short") {
		t.Fatalf("warnings = %q, want one saying season 2 may be short", warned)
	}
}

func partialSeasonSession(trailingSeason bool) *playlist.Session {
	stub := autoplayStub()
	if trailingSeason {
		stub.seasons = append(stub.seasons, media.Season{ID: "s3", Number: 3})
	}
	seasons, _ := stub.GetSeasons("x")
	eps, _ := stub.GetEpisodes("x", "s1")
	stub.episodesErr = fmt.Errorf("probe budget: %w", provider.ErrIncompleteEpisodeList)
	stub.episodesWithErr = []media.Episode{{ID: "e21", Number: 1}, {ID: "e22", Number: 2}}
	sel := media.SearchResult{ID: "tv/show", Title: "Some Show", Type: media.TV}
	return newPlaybackSession(stub, sel, "x", stub, seasons, eps, 0, len(eps)-1, false)
}

func playUntilStopped(t *testing.T, sess *playlist.Session, autoplay bool) ([]playedEp, error) {
	t.Helper()
	return playUntilStoppedWithMenu(t, sess, autoplay, func() {})
}

func playUntilStoppedWithMenu(t *testing.T, sess *playlist.Session, autoplay bool, onMenu func()) ([]playedEp, error) {
	t.Helper()
	var played []playedEp
	prevPlay := playEpisode
	playEpisode = func(s *playlist.Session) error {
		played = append(played, playedEp{s.CurrentSeason().Number, s.Current().Number})
		s.LastPosition, s.LastDuration = endedNaturally.Position, endedNaturally.Duration
		return nil
	}
	prevAuto, prevCfg, prevMenu, prevSup := flagAutoplay, cfg, postPlayMenu, flagSupervised
	flagAutoplay, cfg, flagSupervised = autoplay, &config.Config{History: false}, false
	postPlayMenu = func(*playlist.Session) (postPlayAction, error) { onMenu(); return actionNext, nil }
	t.Cleanup(func() {
		playEpisode, flagAutoplay, cfg, postPlayMenu, flagSupervised = prevPlay, prevAuto, prevCfg, prevMenu, prevSup
	})
	err := runPlaybackLoop(sess)
	return played, err
}

func TestAutoplayStopsCleanlyAtEndOfMeasuredPrefixBeforeALaterSeason(t *testing.T) {
	warned := captureWarnings(t)
	got, err := playUntilStopped(t, partialSeasonSession(true), true)
	if err != nil {
		t.Fatalf("err = %v, want a clean stop", err)
	}
	wantPlayed(t, got, []playedEp{{1, 2}, {2, 1}, {2, 2}})
	if !containsSubstring(*warned, "season 2 episode list may be incomplete") {
		t.Fatalf("warnings = %q, want one saying season 2's list may be incomplete", *warned)
	}
}

func TestAutoplayStopsWithAnErrorWhenNextSeasonsPartialListSkipsEpisodeOne(t *testing.T) {
	warned := captureWarnings(t)
	sess := partialSeasonSession(false)
	sess.Provider.(*stubProvider).episodesWithErr = []media.Episode{{ID: "e22", Number: 2}, {ID: "e23", Number: 3}}
	got, err := playUntilStopped(t, sess, true)
	if err == nil || !errors.Is(err, provider.ErrIncompleteEpisodeList) {
		t.Fatalf("err = %v, want the incomplete-list error: season 2 would start at episode 2", err)
	}
	wantPlayed(t, got, []playedEp{{1, 2}})
	if containsSubstring(*warned, "may be short") {
		t.Fatalf("warnings = %q, want none about a season that was not entered", *warned)
	}
}

func TestAutoplayStopsCleanlyAtAGapInAPartialSeason(t *testing.T) {
	warned := captureWarnings(t)
	sess := partialSeasonSession(false)
	sess.Provider.(*stubProvider).episodesWithErr = []media.Episode{{ID: "e21", Number: 1}, {ID: "e23", Number: 3}}
	got, err := playUntilStopped(t, sess, true)
	if err != nil {
		t.Fatalf("err = %v, want a clean stop", err)
	}
	wantPlayed(t, got, []playedEp{{1, 2}, {2, 1}})
	if !containsSubstring(*warned, "season 2 episode list may be incomplete: stopping after episode 1") {
		t.Fatalf("warnings = %q, want one saying season 2 stops after episode 1", *warned)
	}
}

func TestMenuIsNotShownAtEndOfMeasuredPrefixBeforeALaterSeason(t *testing.T) {
	warned := captureWarnings(t)
	sess := partialSeasonSession(true)
	menuCalls := 0
	got, err := playUntilStoppedWithMenu(t, sess, false, func() { menuCalls++ })
	if err != nil {
		t.Fatalf("err = %v, want a clean stop", err)
	}
	wantPlayed(t, got, []playedEp{{1, 2}, {2, 1}, {2, 2}})
	if menuCalls != 2 {
		t.Fatalf("menu shown %d times, want twice (after season 1 and inside the prefix) and not at the end of the prefix", menuCalls)
	}
	if !containsSubstring(*warned, "season 2 episode list may be incomplete") {
		t.Fatalf("warnings = %q, want one saying season 2's list may be incomplete", *warned)
	}
}

func TestAutoplayWarnsInsteadOfEndOfSeriesWhenLastSeasonIsAMeasuredPrefix(t *testing.T) {
	var warned []string
	prev := warnf
	warnf = func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = prev })
	got, err := playUntilStopped(t, partialSeasonSession(false), true)
	if err != nil {
		t.Fatalf("err = %v, want a clean stop with a warning", err)
	}
	wantPlayed(t, got, []playedEp{{1, 2}, {2, 1}, {2, 2}})
	found := false
	for _, w := range warned {
		found = found || strings.Contains(w, "season 2 episode list may be incomplete")
	}
	if !found {
		t.Fatalf("warnings = %q, want one saying season 2's list may be incomplete", warned)
	}
}

func wantPlayed(t *testing.T, got, want []playedEp) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("played %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("played %v, want %v", got, want)
		}
	}
}

func TestRetryStartPosDistinguishesMeasuredZeroFromNoMeasurement(t *testing.T) {
	cases := []struct {
		name    string
		current float64
		res     player.PlayResult
		want    float64
	}{
		{"measured position moves the offset", 600, player.PlayResult{Position: 900, Duration: 5400}, 900},
		{"measured zero resets the offset", 600, player.PlayResult{Position: 0, Duration: 5400}, 0},
		{"no measurement keeps the offset", 600, player.PlayResult{PositionUnknown: true}, 600},
		{"untracked player keeps the offset", 600, player.PlayResult{PositionUnknown: true, PositionUntracked: true}, 600},
		{"measured position near the end is kept", 600, player.PlayResult{Position: 5395, Duration: 5400}, 5395},
		{"measured position at the end is kept", 600, player.PlayResult{Position: 5400, Duration: 5400}, 5400},
		{"measured position without a duration is kept", 600, player.PlayResult{Position: 900}, 900},
		{"negative measured position clamps to zero", 600, player.PlayResult{Position: -3, Duration: 5400}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := retryStartPos(c.current, c.res); got != c.want {
				t.Fatalf("retryStartPos(%v, %+v) = %v, want %v", c.current, c.res, got, c.want)
			}
		})
	}
}

func playFailureMessage(t *testing.T, autoplay bool, season, episode int) string {
	t.Helper()
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{})
	buf := captureAgentOut(t)
	withPlayerAvailable(t)

	a, b := newDeadChain("Some Show")
	withFallbackChain(t, a, b)
	withStubProvider(t, autoplayStub())
	withEpisodesFlags(t, tvRef(t, ""), season)
	prevEpisode, prevAuto := flagEpisode, flagAutoplay
	flagEpisode, flagAutoplay = episode, autoplay
	t.Cleanup(func() { flagEpisode, flagAutoplay = prevEpisode, prevAuto })

	plays := 0
	prevPlay := playEpisode
	playEpisode = func(s *playlist.Session) error {
		plays++
		if autoplay && plays == 1 {
			s.LastPosition, s.LastDuration = endedNaturally.Position, endedNaturally.Duration
			return nil
		}
		return playCurrentEpisode(s)
	}
	t.Cleanup(func() { playEpisode = prevPlay })

	if err := playRun(playCmd, nil); exitCodeOf(err) != exitProvidersFailed {
		t.Fatalf("playRun = %v (exit %d), want exit %d; envelope: %s", err, exitCodeOf(err), exitProvidersFailed, buf.String())
	}
	env := decodeErrEnvelope(t, buf.String())
	if len(env.Error.Providers) != 2 {
		t.Fatalf("error.providers has %d rows, want 2; envelope: %s", len(env.Error.Providers), buf.String())
	}
	return env.Error.Message
}

func TestPlayFailureAfterAutoplayAdvanceNamesTheEpisodeThatFailed(t *testing.T) {
	msg := playFailureMessage(t, true, 1, 2)
	if !strings.Contains(msg, `no source could stream season 2 episode 1 of "Some Show"`) {
		t.Fatalf("message = %q, want it to name season 2 episode 1, the episode autoplay advanced to", msg)
	}
}

func TestPlayFailureOfASingleEpisodeNamesTheRequestedEpisode(t *testing.T) {
	msg := playFailureMessage(t, false, 1, 2)
	if !strings.Contains(msg, `no source could stream season 1 episode 2 of "Some Show"`) {
		t.Fatalf("message = %q, want it to name the requested season 1 episode 2", msg)
	}
}
