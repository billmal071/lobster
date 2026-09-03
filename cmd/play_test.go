package cmd

import (
	"errors"
	"fmt"
	"testing"

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
