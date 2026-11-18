package cmd

import (
	"errors"
	"testing"

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

	return played, runPlaybackLoop(sess)
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

func TestAutoplayIsNoOpForMovies(t *testing.T) {
	hostileEnv(t)
	captureAgentOut(t)
	plays := 0
	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "stub" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })
	prevPlay := agentResolveAndPlay
	agentResolveAndPlay = func(provider.Provider, media.SearchResult, int, int) error { plays++; return nil }
	t.Cleanup(func() { agentResolveAndPlay = prevPlay })
	prevProv := agentProvider
	agentProvider = func() provider.Provider { return autoplayStub() }
	t.Cleanup(func() { agentProvider = prevProv })

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "A Film", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevA := flagRef, flagAutoplay
	flagRef, flagAutoplay = ref, true
	t.Cleanup(func() { flagRef, flagAutoplay = prevRef, prevA })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if plays != 1 {
		t.Fatalf("a movie played %d times with --autoplay, want 1", plays)
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
		{"untracked player", player.PlayResult{PositionUnknown: true, PositionUntracked: true}, false},
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
