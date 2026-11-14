package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/resolver"
)

// withTTY fakes the terminal probe and --json, leaving promptInteractive's own gating real.
func withTTY(t *testing.T, tty, json bool) {
	t.Helper()
	prevT, prevJ := stdinIsTerminal, flagJSON
	stdinIsTerminal = func() bool { return tty }
	flagJSON = json
	t.Cleanup(func() { stdinIsTerminal, flagJSON = prevT, prevJ })
}

// withAnswers fakes the number prompt. Each label gets its answers in order;
// like ui.Input, a label with none left returns an error ("no input provided")
// rather than an empty answer, and an unbounded loop fails fast.
func withAnswers(t *testing.T, answers map[string][]string) *[]string {
	t.Helper()
	asked := []string{}
	prev := promptNumber
	promptNumber = func(label string) (string, error) {
		asked = append(asked, label)
		if len(asked) > 20 {
			t.Fatalf("prompt loop: asked %v", asked)
		}
		if q := answers[label]; len(q) > 0 {
			answers[label] = q[1:]
			return q[0], nil
		}
		return "", errors.New("no input provided")
	}
	t.Cleanup(func() { promptNumber = prev })
	return &asked
}

type unlistable struct {
	sel media.SearchResult
	fb  *recordingStreamProvider
}

func newUnlistable(t *testing.T) unlistable {
	t.Helper()
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "tv/31910", Title: "Naruto", Type: media.TV}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	return unlistable{sel: sel, fb: fb}
}

func (u unlistable) play(season, episode int) error {
	return resolveAndPlay(&stubProvider{seasonsErr: errors.New("no seasons found")}, u.sel, season, episode)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestUnlistableTVAsksForSeasonAndEpisode checks that 0/0 prompts for both numbers, season first, and reaches the fallback with them.
func TestUnlistableTVAsksForSeasonAndEpisode(t *testing.T) {
	u := newUnlistable(t)
	withTTY(t, true, false)
	asked := withAnswers(t, map[string][]string{"Season": {"2"}, "Episode": {"5"}})

	if err := u.play(0, 0); err != nil {
		t.Fatalf("resolveAndPlay = %v (prompts: %v)", err, *asked)
	}
	if got := u.fb.episodeAsked(); got != "31910:2:5" {
		t.Fatalf("fallback asked for %q, want 31910:2:5", got)
	}
	if !equalStrings(*asked, []string{"Season", "Episode"}) {
		t.Fatalf("prompts = %v, want [Season Episode]", *asked)
	}
}

// TestUnlistableTVWithSeasonAsksOnlyForEpisode checks a history-style S2E0 asks only for the episode and keeps the season.
func TestUnlistableTVWithSeasonAsksOnlyForEpisode(t *testing.T) {
	u := newUnlistable(t)
	withTTY(t, true, false)
	asked := withAnswers(t, map[string][]string{"Episode": {"7"}})

	if err := u.play(2, 0); err != nil {
		t.Fatalf("resolveAndPlay = %v (prompts: %v)", err, *asked)
	}
	if got := u.fb.episodeAsked(); got != "31910:2:7" {
		t.Fatalf("fallback asked for %q, want 31910:2:7", got)
	}
	if !equalStrings(*asked, []string{"Episode"}) {
		t.Fatalf("prompts = %v, want [Episode]", *asked)
	}
}

// TestUnlistableTVSpecialsAreLeftAlone checks season 0 with an episode (TMDB Specials) is a real request, not an unset one.
func TestUnlistableTVSpecialsAreLeftAlone(t *testing.T) {
	u := newUnlistable(t)
	withTTY(t, true, false)
	asked := withAnswers(t, nil)

	if err := u.play(0, 5); err != nil {
		t.Fatalf("resolveAndPlay = %v (prompts: %v)", err, *asked)
	}
	if got := u.fb.episodeAsked(); got != "31910:0:5" {
		t.Fatalf("fallback asked for %q, want 31910:0:5", got)
	}
	if len(*asked) != 0 {
		t.Fatalf("prompted %v for a Specials request", *asked)
	}
}

// TestUnlistableTVWithFlagsDoesNotPrompt checks explicit numbers skip the prompt.
func TestUnlistableTVWithFlagsDoesNotPrompt(t *testing.T) {
	u := newUnlistable(t)
	withTTY(t, true, false)
	asked := withAnswers(t, nil)

	if err := u.play(1, 3); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 || u.fb.episodeAsked() != "31910:1:3" {
		t.Fatalf("prompts %v, episode %q", *asked, u.fb.episodeAsked())
	}
}

// TestUnlistableTVRefusesWhereNobodyCanBeAsked checks --json (even on a terminal) and a missing terminal both refuse with a hint that names flags that exist, wraps the typed error, and never prompt or call Watch.
func TestUnlistableTVRefusesWhereNobodyCanBeAsked(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tty, json bool
	}{
		{"json on a terminal", true, true},
		{"no terminal", false, false},
		{"json and no terminal", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUnlistable(t)
			withTTY(t, tc.tty, tc.json)
			asked := withAnswers(t, map[string][]string{"Season": {"1"}, "Episode": {"1"}})

			err := u.play(0, 0)
			if !errors.Is(err, resolver.ErrSeasonEpisodeRequired) {
				t.Fatalf("err = %v, want ErrSeasonEpisodeRequired", err)
			}
			for _, want := range []string{"Naruto", "without --json", "lobster play --ref", "--season N --episode M"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err %q lacks %q", err, want)
				}
			}
			if u.fb.watchCallCnt != 0 || len(*asked) != 0 {
				t.Fatalf("watch calls %d, prompts %v; want none", u.fb.watchCallCnt, *asked)
			}
		})
	}
}

// TestAskPositiveRetriesBadInputAndCancels checks 0, negatives and non-numbers are rejected and asked again, and a cancelled prompt returns its error.
func TestAskPositiveRetriesBadInputAndCancels(t *testing.T) {
	asked := withAnswers(t, map[string][]string{"Season": {"0", "-3", "abc", "", "99999999999999999999", " 4 "}})
	n, err := askPositive("Season")
	if err != nil || n != 4 {
		t.Fatalf("askPositive = (%d, %v), want 4", n, err)
	}
	if len(*asked) != 6 {
		t.Fatalf("asked %d times, want 6 (5 rejected, 1 accepted)", len(*asked))
	}

	withAnswers(t, nil)
	if n, err := askPositive("Season"); err == nil || n != 0 {
		t.Fatalf("cancelled prompt = (%d, %v), want an error", n, err)
	}
}

// TestFallbackMovieNeverPrompts checks that the movie path is unchanged.
func TestFallbackMovieNeverPrompts(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "movie/603", Title: "The Matrix", Type: media.Movie}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	withTTY(t, true, false)
	asked := withAnswers(t, nil)

	if err := resolveAndPlay(&stubProvider{}, sel, 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 || fb.watchCallCnt != 1 || fb.episodeAsked() != "" {
		t.Fatalf("prompts %v, watches %d, episode %q", *asked, fb.watchCallCnt, fb.episodeAsked())
	}
}
