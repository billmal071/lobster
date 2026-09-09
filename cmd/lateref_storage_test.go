package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"lobster/internal/config"
)

// unsetStorageEnv removes the backend selection for one test and puts it back
// afterwards. TestMain sets it so the suite can never re-exec itself, but that
// same setting is what makes the whole storage mechanism a no-op — a test of
// the mechanism has to clear it. Nothing here can re-exec: the late path only
// ever warns.
func unsetStorageEnv(t *testing.T) {
	t.Helper()
	const key = "TORRENT_STORAGE_DEFAULT_FILE_IO"
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetting %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, prev)
			return
		}
		os.Unsetenv(key)
	})
}

// captureWarnings redirects warnf into a slice for the duration of one test.
func captureWarnings(t *testing.T) *[]string {
	t.Helper()
	got := new([]string)
	prev := warnf
	warnf = func(format string, args ...any) { *got = append(*got, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = prev })
	return got
}

// refCmd builds a command with the persistent flags bound, so
// cmd.Flags().Changed("base") answers about this invocation rather than about
// whatever rootCmd's globals hold.
func refCmd(t *testing.T, argv ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "play", RunE: func(*cobra.Command, []string) error { return nil }}
	registerPersistentFlags(c)
	c.SetArgs(argv)
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := c.Execute(); err != nil {
		t.Fatalf("parsing %v: %v", argv, err)
	}
	markPlaybackCommand(c)
	t.Cleanup(func() { delete(playbackCommands, c) })
	return c
}

// The storage backend is chosen in PersistentPreRunE, but applyRefBase changes
// the base afterwards, inside RunE: a ref minted under `--base yts` overwrites
// a configured non-torrent base, and playStream then stands up torrentstream on
// whichever backend the earlier decision left in place. With `base =
// "soap2day"` and torrent_fallback off, that decision was "no torrent", so the
// run streams the magnet on the memory-mapped backend — the SIGBUS the
// mechanism exists to avoid.
//
// A re-exec is not available that late: it replays argv, and by this point the
// player check has run and the process is committed to this invocation. So the
// honest answer is to say so, in the same words planFileIo uses on Windows.
func TestApplyRefBaseWarnsWhenARefArrivesOnATorrentSourceTooLate(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	prev := cfg
	cfg = &config.Config{Base: "soap2day", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	warnings := captureWarnings(t)
	c := refCmd(t)

	applyRefBase(c, playRef{Base: "yts"})

	if cfg.Base != "yts" {
		t.Fatalf("applyRefBase left base = %q, want yts", cfg.Base)
	}
	joined := strings.Join(*warnings, "\n")
	if !strings.Contains(joined, "SIGBUS") {
		t.Fatalf("applyRefBase said nothing about the storage backend (warnings: %q); the ref moved this run onto a torrent source after the backend was already chosen for a run that would not stream one", *warnings)
	}
}

// The warning is about a base that arrived too late, not about torrents in
// general. A run already configured for YTS had its backend chosen correctly
// at startup, so repeating the notice here would train the user to ignore it.
func TestApplyRefBaseStaysQuietWhenTheBaseDoesNotChange(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	prev := cfg
	cfg = &config.Config{Base: "yts", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	warnings := captureWarnings(t)
	applyRefBase(refCmd(t), playRef{Base: "yts"})

	if len(*warnings) != 0 {
		t.Fatalf("applyRefBase warned on a run whose base did not change: %q", *warnings)
	}
}

// episodes shares applyRefBase with play and never streams anything, so a ref
// that names a torrent source there is not a storage problem.
func TestApplyRefBaseStaysQuietForACommandThatCannotPlay(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	prev := cfg
	cfg = &config.Config{Base: "soap2day", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	warnings := captureWarnings(t)
	c := refCmd(t)
	delete(playbackCommands, c) // as `episodes` is: registered, but never playing

	applyRefBase(c, playRef{Base: "yts"})

	if len(*warnings) != 0 {
		t.Fatalf("applyRefBase warned for a non-playback command: %q", *warnings)
	}
}
