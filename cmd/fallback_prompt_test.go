package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/media"
)

func withPromptNumber(t *testing.T, interactive bool, answers map[string]string) *[]string {
	t.Helper()
	asked := []string{}
	prevI, prevN := promptInteractive, promptNumber
	promptInteractive = func() bool { return interactive }
	promptNumber = func(label string) (string, error) {
		asked = append(asked, label)
		return answers[label], nil
	}
	t.Cleanup(func() { promptInteractive, promptNumber = prevI, prevN })
	return &asked
}

// TestUnlistableTVAsksForSeasonAndEpisode checks that a TV selection whose primary cannot list seasons prompts for the numbers and reaches the fallback with them.
func TestUnlistableTVAsksForSeasonAndEpisode(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "tv/31910", Title: "Naruto", Type: media.TV}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	asked := withPromptNumber(t, true, map[string]string{"Season": "2", "Episode": "5"})

	primary := &stubProvider{seasonsErr: errors.New("no seasons found")}
	if err := resolveAndPlay(primary, sel, 0, 0); err != nil {
		t.Fatalf("resolveAndPlay = %v (prompts: %v)", err, *asked)
	}
	if got := fb.episodeAsked(); got != "31910:2:5" {
		t.Fatalf("fallback asked for %q, want 31910:2:5 (prompts: %v)", got, *asked)
	}
}

// TestUnlistableTVNonInteractiveRefusesClearly checks that without a prompt the run errors with the season/episode message and never reaches Watch.
func TestUnlistableTVNonInteractiveRefusesClearly(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "tv/31910", Title: "Naruto", Type: media.TV}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	asked := withPromptNumber(t, false, nil)

	primary := &stubProvider{seasonsErr: errors.New("no seasons found")}
	err := resolveAndPlay(primary, sel, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "--season and --episode") {
		t.Fatalf("err = %v, want season/episode required message", err)
	}
	if fb.watchCallCnt != 0 || len(*asked) != 0 {
		t.Fatalf("watch calls %d, prompts %v; want none", fb.watchCallCnt, *asked)
	}
}

// TestUnlistableTVWithFlagsDoesNotPrompt checks that explicit numbers skip the prompt.
func TestUnlistableTVWithFlagsDoesNotPrompt(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "tv/31910", Title: "Naruto", Type: media.TV}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	asked := withPromptNumber(t, true, nil)

	primary := &stubProvider{seasonsErr: errors.New("no seasons found")}
	if err := resolveAndPlay(primary, sel, 1, 3); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 || fb.episodeAsked() != "31910:1:3" {
		t.Fatalf("prompts %v, episode %q", *asked, fb.episodeAsked())
	}
}

// TestFallbackMovieNeverPrompts checks that the movie path is unchanged.
func TestFallbackMovieNeverPrompts(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	sel := media.SearchResult{ID: "movie/603", Title: "The Matrix", Type: media.Movie}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)
	asked := withPromptNumber(t, true, nil)

	if err := resolveAndPlay(&stubProvider{}, sel, 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 || fb.watchCallCnt != 1 || fb.episodeAsked() != "" {
		t.Fatalf("prompts %v, watches %d, episode %q", *asked, fb.watchCallCnt, fb.episodeAsked())
	}
}
