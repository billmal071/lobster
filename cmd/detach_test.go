package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// containsArg reports whether args contains want as a contiguous, exact
// subsequence. Deliberately not implemented via strings.Join + Contains:
// joining collapses argv into one string, so "play" would also match
// "--replay" or a path component, and "--ref REF123" would match even if
// they landed as one mangled argv entry instead of two adjacent ones.
func containsArg(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		match := true
		for j, w := range want {
			if args[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// hermeticCacheDir points os.UserCacheDir at a throwaway directory so tests
// never touch (or depend on) the real user cache dir.
func hermeticCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CACHE_HOME", dir)
	}
}

// withInheritedFlags forces the same persistent-flag merge cobra performs
// before RunE during a real Execute() (see (*cobra.Command).mergePersistentFlags),
// the same technique play_test.go's withBaseFlag uses, generalized to any
// flag name a test needs to Set/Changed on playCmd directly rather than
// through Execute(). Restores each flag's Changed bit and value afterward.
func withInheritedFlags(t *testing.T, names ...string) {
	t.Helper()
	playCmd.InheritedFlags() // side effect: merges rootCmd's persistent flags into playCmd.Flags()
	for _, name := range names {
		f := playCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("playCmd.Flags().Lookup(%q) is nil after merge", name)
		}
		prevChanged, prevVal := f.Changed, f.Value.String()
		t.Cleanup(func() {
			f.Changed = prevChanged
			_ = f.Value.Set(prevVal)
		})
	}
}

// The supervisor must be told to actually play, and must be marked supervised
// so it does not recurse into spawning another supervisor.
func TestSupervisorArgsArePlayableAndNonRecursive(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF123", 2, 3, nil)

	if args[0] != "/usr/bin/lobster" || args[1] != "play" {
		t.Fatalf("args %v do not invoke play", args)
	}
	if !containsArg(args, "--ref", "REF123") {
		t.Fatalf("args %v do not carry the ref", args)
	}
	if !containsArg(args, "--season", "2") || !containsArg(args, "--episode", "3") {
		t.Fatalf("args %v lost season/episode", args)
	}
	if !containsArg(args, "--supervised") {
		t.Fatalf("args %v missing --supervised; the child would spawn its own child forever", args)
	}
	if containsArg(args, "--detach") {
		t.Fatalf("args %v still carry --detach; that is the recursion", args)
	}
}

// Season and episode are meaningless for a film and must not be passed as
// zeroes, which the play command would reject.
func TestSupervisorArgsOmitZeroSeasonEpisode(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0, nil)
	if containsArg(args, "--season") || containsArg(args, "--episode") {
		t.Fatalf("args %v pass zero season/episode", args)
	}
}

// extra is appended verbatim, exercising the mechanism forwardedArgs relies
// on independent of any particular flag.
func TestSupervisorArgsAppendsExtra(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 2, 3, []string{"--base", "yts"})
	if !containsArg(args, "--base", "yts") {
		t.Fatalf("args %v do not carry extra", args)
	}
	// Distinct from the zero-season/episode case: season/episode are present
	// here alongside extra, so this is not testing the same thing twice.
	if !containsArg(args, "--season", "2") || !containsArg(args, "--episode", "3") {
		t.Fatalf("args %v lost season/episode alongside extra", args)
	}
}

func TestSupervisorArgsOmitsExtraWhenNil(t *testing.T) {
	args := supervisorArgs("/usr/bin/lobster", "REF", 0, 0, nil)
	if containsArg(args, "--base") {
		t.Fatalf("args %v carry --base though extra was nil", args)
	}
}

// detachChildArgv is the actual decision point: it must read the flag's real
// Changed() state via cmd, not just plumb a value through. An implementation
// that hardcoded "no override" or unconditionally forwarded flagBase would
// pass a test that only exercises supervisorArgs directly — these two tests
// exercise detachChildArgv itself, in both directions, driving Changed()
// through playCmd the same way a real Execute() would.
func TestDetachChildArgvPropagatesExplicitBase(t *testing.T) {
	withInheritedFlags(t, "base")
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = "REF", 0, 0
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })

	if err := playCmd.Flags().Set("base", "yts"); err != nil {
		t.Fatalf("Set --base: %v", err)
	}

	got := detachChildArgv(playCmd, "/usr/bin/lobster")
	if !containsArg(got, "--base", "yts") {
		t.Fatalf("detachChildArgv(...) = %v, want explicit --base forwarded", got)
	}
}

func TestDetachChildArgvOmitsBaseWhenNotExplicit(t *testing.T) {
	withInheritedFlags(t, "base")
	prevRef, prevS, prevE := flagRef, flagSeason, flagEpisode
	flagRef, flagSeason, flagEpisode = "REF", 0, 0
	t.Cleanup(func() { flagRef, flagSeason, flagEpisode = prevRef, prevS, prevE })
	// Deliberately do not Set("base", ...): Changed must be false here.

	got := detachChildArgv(playCmd, "/usr/bin/lobster")
	if containsArg(got, "--base") {
		t.Fatalf("detachChildArgv(...) = %v, forwarded --base though it was not explicit", got)
	}
}

// forwardedArgs is the table-driven mechanism behind detachChildArgv. Cover
// it at its own level: a string flag, a bool flag, and the "nothing passed"
// case, so a change that forwards unconditionally or drops the table entirely
// cannot pass by accident.
func TestForwardedArgsIncludesExplicitStringAndBoolFlags(t *testing.T) {
	withInheritedFlags(t, "quality", "no-subs")

	if err := playCmd.Flags().Set("quality", "1080"); err != nil {
		t.Fatalf("Set --quality: %v", err)
	}
	if err := playCmd.Flags().Set("no-subs", "true"); err != nil {
		t.Fatalf("Set --no-subs: %v", err)
	}

	got := forwardedArgs(playCmd)
	if !containsArg(got, "--quality", "1080") {
		t.Fatalf("forwardedArgs(...) = %v, missing explicit --quality", got)
	}
	if !containsArg(got, "--no-subs") {
		t.Fatalf("forwardedArgs(...) = %v, missing explicit --no-subs", got)
	}
}

func TestForwardedArgsOmitsFlagsNotPassed(t *testing.T) {
	withInheritedFlags(t, "player", "provider", "language", "audio-language", "debug", "base")

	got := forwardedArgs(playCmd)
	if len(got) != 0 {
		t.Fatalf("forwardedArgs(...) = %v, want none: nothing was passed explicitly", got)
	}
}

// --download is rejected earlier in playRun and must never reach detach's
// forwarding table, explicit or not.
func TestForwardedArgsNeverForwardsDownload(t *testing.T) {
	for _, f := range forwardedStringFlags {
		if f.name == "download" {
			t.Fatalf("forwardedStringFlags carries --download; it must never be forwarded")
		}
	}
	for _, f := range forwardedBoolFlags {
		if f.name == "download" {
			t.Fatalf("forwardedBoolFlags carries --download; it must never be forwarded")
		}
	}
}

// flagContinue is read inside playStream (cmd/search.go:560) to load history
// and set the resume position. Without forwarding it, `lobster play --ref R
// --continue --detach` would silently start from the beginning despite the
// caller explicitly asking to resume — the same bug class as the other
// seven flags.
func TestForwardedArgsIncludesExplicitContinue(t *testing.T) {
	withInheritedFlags(t, "continue")

	if err := playCmd.Flags().Set("continue", "true"); err != nil {
		t.Fatalf("Set --continue: %v", err)
	}

	got := forwardedArgs(playCmd)
	if !containsArg(got, "--continue") {
		t.Fatalf("forwardedArgs(...) = %v, missing explicit --continue", got)
	}
}

// flagJSON is read inside playStream (cmd/search.go:470), where it prints
// stream metadata and returns *before playing*. Forwarding it to the
// supervised child would make the child print JSON into its log and exit
// without ever starting playback, while the parent had already reported
// "status":"playing" — so omitting it is a deliberate decision, not a gap.
// This test pins that decision so a later "make forwarding exhaustive"
// change does not silently turn it back into a bug.
func TestForwardedArgsNeverForwardsJSON(t *testing.T) {
	withInheritedFlags(t, "json")

	if err := playCmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("Set --json: %v", err)
	}

	got := forwardedArgs(playCmd)
	if containsArg(got, "--json") {
		t.Fatalf("forwardedArgs(...) = %v, forwarded --json; the child would print metadata and never play", got)
	}
}

func TestDetachLogPathIsPerPID(t *testing.T) {
	hermeticCacheDir(t)

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

// createDetachLog must hand back distinct files on repeated calls (no reuse
// or truncation of a previous run's log) without needing to know any pid up
// front.
func TestCreateDetachLogIsUnique(t *testing.T) {
	hermeticCacheDir(t)

	f1, err := createDetachLog()
	if err != nil {
		t.Fatalf("createDetachLog: %v", err)
	}
	defer f1.Close()
	f2, err := createDetachLog()
	if err != nil {
		t.Fatalf("createDetachLog: %v", err)
	}
	defer f2.Close()

	if f1.Name() == f2.Name() {
		t.Fatalf("createDetachLog returned the same path twice: %q", f1.Name())
	}
}

// waitLiveness must reap the child via Wait, not merely poll its pid: an
// unwaited child becomes a zombie on Unix (kill(pid,0) still succeeds) and
// holds an open handle on Windows (reserving the pid) — either way a
// pid-only check always reports "alive". These tests drive a real child
// process (this same test binary, re-exec'd as a disposable helper via
// LOBSTER_TEST_HELPER_EXIT_CODE — see TestMain in fallback_providers_test.go)
// so the reap is exercised for real, not asserted by reading the source.
func TestWaitLivenessDetectsImmediateExit(t *testing.T) {
	c := exec.Command(os.Args[0])
	c.Env = append(os.Environ(), "LOBSTER_TEST_HELPER_EXIT_CODE=1")
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if alive := waitLiveness(c, 200*time.Millisecond); alive {
		t.Fatal("waitLiveness reported alive for a process that already exited")
	}
}

func TestWaitLivenessReportsAliveWhileChildStillRunning(t *testing.T) {
	c := exec.Command(os.Args[0])
	c.Env = append(os.Environ(),
		"LOBSTER_TEST_HELPER_EXIT_CODE=0",
		"LOBSTER_TEST_HELPER_SLEEP_MS=5000",
	)
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Process.Kill() })

	if alive := waitLiveness(c, 100*time.Millisecond); !alive {
		t.Fatal("waitLiveness reported dead for a process still running")
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
