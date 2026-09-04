package cmd

import (
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
// playback, while the parent had already reported "status":"playing".
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
			args = append(args, "--"+f.name)
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

// detachLogPath is a deterministic per-pid path under lobsterCacheDir.
// playDetached itself does not use it — it uses createDetachLog below, which
// sidesteps a rename-after-Start scheme that turned out to be unreliable on
// Windows — but it is kept as a small, independently useful, independently
// testable building block (e.g. for locating a specific run's log by pid
// from outside the process).
func detachLogPath(pid int) (string, error) {
	dir, err := lobsterCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("play-%d.log", pid)), nil
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

// waitLiveness reports whether c is still running once liveness elapses.
// It must reap via c.Wait rather than merely poll the pid: an un-waited
// child becomes a zombie on Unix, where kill(pid, 0) — the only pid-based
// liveness check available — still succeeds against a zombie, and holds an
// open process handle on Windows that reserves the pid regardless of exit
// status. Either way a pid-only check always reports "alive", which is
// exactly the bug this replaces. Waiting is what makes failure detectable at
// all; the blocked goroutine in the "still alive" case costs nothing, since
// the caller returns immediately after and the whole process exits.
func waitLiveness(c *exec.Cmd, liveness time.Duration) bool {
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case <-done:
		return false
	case <-time.After(liveness):
		return true
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

	if err := c.Start(); err != nil {
		return emitErr("player_unavailable", exitPlayerUnavailable, "starting background player: %v", err)
	}

	if !waitLiveness(c, detachLiveness) {
		return emitErr("providers_failed", exitProvidersFailed,
			"playback exited immediately; see %s", lf.Name())
	}

	return emitJSON(map[string]any{
		"status":          "playing",
		"pid":             c.Process.Pid,
		"title":           r.Title,
		"log":             lf.Name(),
		"resume_tracking": playerTracksPosition(),
	})
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
