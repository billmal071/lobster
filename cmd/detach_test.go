package cmd

import (
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// The supervisor must be told to actually play, and must be marked supervised
// so it does not recurse into spawning another supervisor.
func TestSupervisorArgsArePlayableAndNonRecursive(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF123", 2, 3, "")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "play") {
		t.Fatalf("args %v do not invoke play", args)
	}
	if !strings.Contains(joined, "--ref REF123") {
		t.Fatalf("args %v do not carry the ref", args)
	}
	if !strings.Contains(joined, "--season 2") || !strings.Contains(joined, "--episode 3") {
		t.Fatalf("args %v lost season/episode", args)
	}
	if !strings.Contains(joined, "--supervised") {
		t.Fatalf("args %v missing --supervised; the child would spawn its own child forever", args)
	}
	if strings.Contains(joined, "--detach") {
		t.Fatalf("args %v still carry --detach; that is the recursion", args)
	}
}

// Season and episode are meaningless for a film and must not be passed as
// zeroes, which the play command would reject.
func TestSupervisorArgsOmitZeroSeasonEpisode(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--season") || strings.Contains(joined, "--episode") {
		t.Fatalf("args %v pass zero season/episode", args)
	}
}

// When the caller passed --base explicitly on the foreground invocation, that
// override must be propagated to the supervised child. Without this, the
// child would fall back to the ref's own Base and silently ignore the
// explicit override (cmd/play.go's Changed("base") logic).
func TestSupervisorArgsPropagatesExplicitBase(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0, "yts")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--base yts") {
		t.Fatalf("args %v do not propagate explicit --base", args)
	}
}

// When the caller did not pass --base, none should be forwarded, so the
// child falls back to the ref's own Base as playRun already does.
func TestSupervisorArgsOmitBaseWhenNotExplicit(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--base") {
		t.Fatalf("args %v pass --base though it was not explicitly set", args)
	}
}

func TestDetachLogPathIsPerPID(t *testing.T) {
	a, err := detachLogPath(111)
	if err != nil {
		t.Fatalf("detachLogPath: %v", err)
	}
	b, err := detachLogPath(222)
	if err != nil {
		t.Fatalf("detachLogPath: %v", err)
	}
	if a == b {
		t.Fatalf("log paths collide: %q", a)
	}
	if !strings.Contains(a, "111") {
		t.Fatalf("log path %q does not identify the pid", a)
	}
}

// A supervised process must play, not spawn another supervisor. Without this
// guard --detach forks forever.
func TestSupervisedPlayDoesNotDetachAgain(t *testing.T) {
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

	ref, err := encodeRef(playRef{ID: "movie/x", Title: "X", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevD, prevS := flagRef, flagDetach, flagSupervised
	flagRef, flagDetach, flagSupervised = ref, true, true
	t.Cleanup(func() { flagRef, flagDetach, flagSupervised = prevRef, prevD, prevS })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}
	if !called {
		t.Fatal("supervised play did not reach playback; it re-detached instead")
	}
}
