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

// supervisorArgs builds the argv for the background lobster that actually
// plays. It carries --supervised so the child does not spawn a child of its
// own, and drops --detach for the same reason.
//
// base is the value of an explicit --base the caller passed on the
// foreground invocation, or "" if they did not pass one. playRun applies the
// ref's own Base unless --base was explicit (cmd/play.go), so an explicit
// --base must be forwarded here too — otherwise the supervised child would
// fall back to the ref's Base and silently ignore the caller's override.
func supervisorArgs(exe, ref string, season, episode int, base string) []string {
	args := []string{exe, "play", "--ref", ref, "--supervised"}
	if season > 0 {
		args = append(args, "--season", strconv.Itoa(season))
	}
	if episode > 0 {
		args = append(args, "--episode", strconv.Itoa(episode))
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	return args
}

// detachLogPath is where a detached play writes its output. Per-pid so
// concurrent plays do not interleave, and so the JSON payload can point at a
// specific run.
func detachLogPath(pid int) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache dir: %w", err)
	}
	dir = filepath.Join(dir, "lobster")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, fmt.Sprintf("play-%d.log", pid)), nil
}

// detachLiveness is how long the foreground waits before declaring success.
// cmd.Start succeeding only means the binary exec'd; mpv's own failure
// detection is "exited within 5s with no playback" (internal/player/mpv.go).
// This does not close the race, it removes the common case of reporting
// success for a stream that never started.
var detachLiveness = 1 * time.Second

// playDetached re-executes lobster as a background supervisor and returns as
// soon as it looks alive. The child performs an ordinary attached play, so the
// HLS proxy, torrent server, subtitle temp dir and mpv IPC all behave exactly
// as they do interactively.
func playDetached(cmd *cobra.Command, r playRef) error {
	exe, err := os.Executable()
	if err != nil {
		return emitErr("internal", 1, "locating lobster binary: %v", err)
	}

	// Propagate an explicit --base the same way playRun honors it: only when
	// the caller passed it on this invocation, so the child falls back to the
	// ref's own Base otherwise.
	base := ""
	if cmd.Flags().Changed("base") {
		base = flagBase
	}

	argv := supervisorArgs(exe, flagRef, flagSeason, flagEpisode, base)

	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = detachSpawnAttr()
	c.Stdin = nil // no TTY to inherit

	// The child's output must never reach the pipe the caller is parsing: all
	// three players set cmd.Stdout = os.Stdout, which would interleave with the
	// JSON envelope and corrupt it.
	tmpLog, err := detachLogPath(os.Getpid())
	if err != nil {
		return emitErr("internal", 1, "%v", err)
	}
	lf, err := os.Create(tmpLog)
	if err != nil {
		return emitErr("internal", 1, "creating log %s: %v", tmpLog, err)
	}
	defer lf.Close()
	c.Stdout = lf
	c.Stderr = lf

	if err := c.Start(); err != nil {
		return emitErr("player_unavailable", exitPlayerUnavailable, "starting background player: %v", err)
	}

	// Rename the log to the child's pid now that we know it, so the payload
	// points at a file the user can find from the pid alone.
	finalLog, err := detachLogPath(c.Process.Pid)
	if err == nil && os.Rename(tmpLog, finalLog) == nil {
		tmpLog = finalLog
	}

	time.Sleep(detachLiveness)
	if !processAlive(c.Process.Pid) {
		return emitErr("providers_failed", exitProvidersFailed,
			"playback exited immediately; see %s", tmpLog)
	}

	return emitJSON(map[string]any{
		"status":          "playing",
		"pid":             c.Process.Pid,
		"title":           r.Title,
		"log":             tmpLog,
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
