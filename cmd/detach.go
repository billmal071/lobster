package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagDetach     bool
	flagSupervised bool
)

const browserHandoffWait = 20 * time.Second

// forwardedStringFlags are persistent flags whose value config.Load folds
// into cfg (root.go's loadConfig) and that must therefore be forwarded to a
// supervised child when the caller passed them explicitly on this
// invocation. Without this, the child re-derives cfg from the config
// file/defaults on its own and silently drops the override — the same hole
// --base had. --download is deliberately absent: playRun rejects it before
// playDetached is ever reached, so detach never needs to carry it.
var forwardedStringFlags = []struct {
	name string
	val  *string
}{
	{"base", &flagBase},
	{"quality", &flagQuality},
	{"player", &flagPlayer},
	{"provider", &flagProvider},
	{"language", &flagLanguage},
	{"audio-language", &flagAudioLang},
}

// forwardedBoolFlags is forwardedStringFlags' counterpart for boolean flags.
//
// --json is deliberately absent, and this is a decision, not an oversight:
// flagJSON is read inside playStream (cmd/search.go:470), where it prints
// stream metadata and returns *before playing*. Forwarding it would make the
// supervised child print JSON into its log and exit without ever starting
// playback, while the parent had already reported "status":"started".
var forwardedBoolFlags = []struct {
	name string
	val  *bool
}{
	{"no-subs", &flagNoSubs},
	{"debug", &flagDebug},
	{"continue", &flagContinue},
}

// forwardedArgs returns argv fragments for every persistent flag the caller
// passed explicitly on this invocation (cmd.Flags().Changed), so the
// supervised child sees the same overrides instead of falling back to
// config/defaults. This is the same detection playRun already uses for
// --base (cmd/play.go), generalized over a table instead of one flag at a
// time.
func forwardedArgs(cmd *cobra.Command) []string {
	var args []string
	for _, f := range forwardedStringFlags {
		if cmd.Flags().Changed(f.name) {
			args = append(args, "--"+f.name, *f.val)
		}
	}
	for _, f := range forwardedBoolFlags {
		if cmd.Flags().Changed(f.name) {
			// Changed is also true for an explicit "--flag=false": the caller
			// set it, they just set it to false. A bare "--"+name always means
			// true to cobra, so an explicit --continue=false would forward as
			// a bare --continue and the child would resume when the caller
			// asked it not to — the opposite of what was requested. Emitting
			// the value explicitly is required, not cosmetic.
			args = append(args, "--"+f.name+"="+strconv.FormatBool(*f.val))
		}
	}
	return args
}

// supervisorArgs builds the argv for the background lobster that actually
// plays. It carries --supervised so the child does not spawn a child of its
// own, and drops --detach for the same reason. extra carries any explicitly
// passed flags to forward (see forwardedArgs); nil/empty means none.
func supervisorArgs(exe, ref string, season, episode int, extra []string) []string {
	args := []string{exe, "play", "--ref", ref, "--supervised"}
	if season > 0 {
		args = append(args, "--season", strconv.Itoa(season))
	}
	if episode > 0 {
		args = append(args, "--episode", strconv.Itoa(episode))
	}
	args = append(args, extra...)
	return args
}

// detachChildArgv computes the full argv for the supervised child from the
// current invocation. It is the decision point: forwardedArgs decides which
// explicit flags travel with the child, using cmd's actual Changed() state
// rather than a value threaded in by hand, so a change that hardcodes "no
// override" or unconditionally forwards a value cannot pass a test written
// against this function.
func detachChildArgv(cmd *cobra.Command, exe string) []string {
	return supervisorArgs(exe, flagRef, flagSeason, flagEpisode, forwardedArgs(cmd))
}

// lobsterCacheDir is the directory detached play artifacts (logs) live in.
func lobsterCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache dir: %w", err)
	}
	dir = filepath.Join(dir, "lobster")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return dir, nil
}

// createDetachLog opens a fresh, uniquely named log file for a detached
// play. Named once, up front, via os.CreateTemp rather than created under
// our own pid and renamed to the child's pid once it is known: on Windows,
// os.Create opens without FILE_SHARE_DELETE, the child inherits that same
// handle once started, and a later os.Rename then fails with
// ERROR_SHARING_VIOLATION while both handles are open — which the original
// code tolerated silently, breaking "find the log from its pid" on exactly
// the platform that needs it spelled out. The exact path is returned so the
// caller can put it straight into the JSON payload; the pid is reported
// there separately, so nothing depends on the log's name matching it.
func createDetachLog() (*os.File, error) {
	dir, err := lobsterCacheDir()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "play-*.log")
	if err != nil {
		return nil, fmt.Errorf("creating log in %s: %w", dir, err)
	}
	return f, nil
}

// detachLiveness is how long the foreground waits before declaring success.
// cmd.Start succeeding only means the binary exec'd; mpv's own failure
// detection is "exited within 5s with no playback" (internal/player/mpv.go).
// This does not close the race, it removes the common case of reporting
// success for a stream that never started.
var detachLiveness = 1 * time.Second

// waitLiveness reports whether c is still running once liveness elapses and
// returns the child's exit error when it exited before the window.
// It must reap via c.Wait rather than merely poll the pid: an un-waited
// child becomes a zombie on Unix, where kill(pid, 0) — the only pid-based
// liveness check available — still succeeds against a zombie, and holds an
// open process handle on Windows that reserves the pid regardless of exit
// status. Either way a pid-only check always reports "alive", which is
// exactly the bug this replaces. Waiting is what makes failure detectable at
// all; the blocked goroutine in the "still alive" case costs nothing, since
// the caller returns immediately after and the whole process exits.
func waitLiveness(c *exec.Cmd, liveness time.Duration) (bool, error) {
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-time.After(liveness):
		return true, nil
	}
}

// detachedBrowserHandoff reports whether the supervised child successfully
// opened a browser URL and exited with the internal handoff status.
func detachedBrowserHandoff(alive bool, exitErr error) bool {
	if alive || exitErr == nil {
		return false
	}
	var ee *exec.ExitError
	return errors.As(exitErr, &ee) && ee.ExitCode() == exitBrowserHandoff
}

func detachedExitCode(exitErr error) int {
	var ee *exec.ExitError
	if errors.As(exitErr, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// waitBrowserOutcome waits for a supervised browser-intent ref to resolve its
// actual playback mode. Browser mode is then followed until the child reports
// success/failure or the bound expires; player mode returns immediately so
// ordinary media playback retains the short detach response time.
func waitBrowserOutcome(c *exec.Cmd, modeFile string, timeout time.Duration) (mode string, alive bool, exitErr error) {
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	startedAt := time.Now()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	mode = readDetachPlaybackMode(modeFile)
	for {
		select {
		case err := <-done:
			return readDetachPlaybackMode(modeFile), false, err
		case <-ticker.C:
			if observed := readDetachPlaybackMode(modeFile); observed != "" {
				mode = observed
				if mode == "player" {
					remaining := detachLiveness - time.Since(startedAt)
					if remaining > 0 {
						mediaTimer := time.NewTimer(remaining)
						defer mediaTimer.Stop()
						select {
						case err := <-done:
							return mode, false, err
						case <-mediaTimer.C:
						}
					}
					select {
					case err := <-done:
						return mode, false, err
					default:
						return mode, true, nil
					}
				}
			}
		case <-timer.C:
			select {
			case err := <-done:
				return readDetachPlaybackMode(modeFile), false, err
			default:
				return mode, true, nil
			}
		}
	}
}

func readDetachPlaybackMode(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(data)) {
	case "browser", "player":
		return strings.TrimSpace(string(data))
	default:
		return ""
	}
}

// playDetached re-executes lobster as a background supervisor and returns as
// soon as it looks alive. The child performs an ordinary attached play, so the
// HLS proxy, torrent server, subtitle temp dir and mpv IPC all behave exactly
// as they do interactively.
func playDetached(cmd *cobra.Command, r playRef) error {
	exe, err := os.Executable()
	if err != nil {
		return emitErr("internal", 1, "locating lobster binary: %v", err)
	}

	argv := detachChildArgv(cmd, exe)

	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = detachSpawnAttr()
	c.Stdin = nil // no TTY to inherit

	// The child's output must never reach the pipe the caller is parsing: all
	// three players set cmd.Stdout = os.Stdout, which would interleave with the
	// JSON envelope and corrupt it.
	lf, err := createDetachLog()
	if err != nil {
		return emitErr("internal", 1, "%v", err)
	}
	defer lf.Close()
	c.Stdout = lf
	c.Stderr = lf
	modeFile := ""
	cleanupModeFile := true
	defer func() {
		if modeFile != "" && cleanupModeFile {
			_ = os.Remove(modeFile)
		}
	}()
	if r.BrowserBacked || r.Source == "https://1ball.pk/api/matches" {
		mf, createErr := os.CreateTemp(filepath.Dir(lf.Name()), "play-mode-*.signal")
		if createErr != nil {
			return emitErr("internal", 1, "creating playback mode signal: %v", createErr)
		}
		modeFile = mf.Name()
		if closeErr := mf.Close(); closeErr != nil {
			_ = os.Remove(modeFile)
			return emitErr("internal", 1, "closing playback mode signal: %v", closeErr)
		}
		childEnv := make([]string, 0, len(os.Environ())+1)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, detachModeFileEnv+"=") {
				childEnv = append(childEnv, entry)
			}
		}
		c.Env = append(childEnv, detachModeFileEnv+"="+modeFile)
	}

	if err := c.Start(); err != nil {
		return emitErr("player_unavailable", exitPlayerUnavailable, "starting background player: %v", err)
	}

	var mode string
	var alive bool
	var exitErr error
	if modeFile != "" {
		mode, alive, exitErr = waitBrowserOutcome(c, modeFile, browserHandoffWait)
	} else {
		alive, exitErr = waitLiveness(c, detachLiveness)
	}
	if detachedBrowserHandoff(alive, exitErr) {
		return emitJSON(browserHandoffPayload(r.Title, lf.Name()))
	}
	if !alive {
		if detachedExitCode(exitErr) == exitPlayerUnavailable {
			return emitErr("player_unavailable", exitPlayerUnavailable,
				"the configured player is unavailable; see %s", lf.Name())
		}
		return emitErr("providers_failed", exitProvidersFailed,
			"playback exited immediately; see %s", lf.Name())
	}
	if mode == "browser" {
		return emitJSON(browserPendingPayload(c.Process.Pid, r.Title, lf.Name()))
	}
	if modeFile != "" && mode == "" {
		// The child may still be resolving the channel. Report the live
		// supervisor as pending; a missing marker later must not prevent the
		// child from completing the requested playback.
		return emitJSON(browserResolutionPendingPayload(c.Process.Pid, r.Title, lf.Name()))
	}

	return emitJSON(detachPayload(c.Process.Pid, r.Title, lf.Name()))
}

func browserPendingPayload(pid int, title, log string) map[string]any {
	return map[string]any{
		"status":          "pending",
		"pid":             pid,
		"title":           title,
		"log":             log,
		"resume_tracking": false,
	}
}

func browserResolutionPendingPayload(pid int, title, log string) map[string]any {
	payload := browserPendingPayload(pid, title, log)
	payload["playback_mode"] = "resolving"
	return payload
}

// browserHandoffPayload reports a completed browser launch without a stale
// child PID: Chrome owns the page after lobster exits.
func browserHandoffPayload(title, log string) map[string]any {
	return map[string]any{
		"status":          "opened",
		"title":           title,
		"log":             log,
		"resume_tracking": false,
	}
}

// detachPayload is the success envelope for a detached play.
//
// The status is "started", not "playing", and the difference is not cosmetic.
// detachLiveness is one second, while provider search and stream extraction
// routinely take five to thirty; the common failure therefore lands well after
// the parent has already exited 0. All this envelope can honestly claim is
// that a player process was started — which is why "log" is part of the
// contract: it is the only way to find out what happened next.
func detachPayload(pid int, title, log string) map[string]any {
	return map[string]any{
		"status":          "started",
		"pid":             pid,
		"title":           title,
		"log":             log,
		"resume_tracking": playerTracksPosition(),
	}
}

// playerTracksPosition reports whether the configured player reports playback
// position. Only mpv does: vlc.go and generic.go both return an empty
// PlayResult regardless of detach.
func playerTracksPosition() bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(cfg.Player, "mpv")
}
