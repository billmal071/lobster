package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// isolateConfig points config loading at an empty directory so loadConfig reads
// the shipped defaults rather than the developer's own config.toml, and
// restores the global cfg afterwards.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // unix (config.configDir)
	t.Setenv("APPDATA", dir)         // windows
	prev := cfg
	t.Cleanup(func() { cfg = prev })
}

// observeStorageChoice replaces the storage-backend seam with a recorder and
// returns the slice the willStream arguments land in. Nil entries mean the
// call was never made.
func observeStorageChoice(t *testing.T) *[]bool {
	t.Helper()
	calls := new([]bool)
	prev := ensureSafeStorage
	ensureSafeStorage = func(willStream bool, _ func(string, ...any)) {
		*calls = append(*calls, willStream)
	}
	t.Cleanup(func() { ensureSafeStorage = prev })
	return calls
}

// loadConfig is the root's PersistentPreRunE, so it runs for every subcommand,
// and the torrent storage decision it makes is not free: on a platform that can
// re-exec it restarts the process, and on Windows (canExec is false) it prints
// an ungated SIGBUS warning to stderr. Neither belongs on `lobster version`,
// `find`, `episodes`, `doctor` or `channels`: the per-type route to YTS runs
// from resolveAndPlay only, so none of them can reach a magnet however the run
// ends.
//
// Gating on the output flags was tried and is not enough — `version` passes
// neither --json nor --download, so it re-execed anyway (2 execve against
// main's 1, measured with strace). The gate has to be the command.
//
// The table is exhaustive by construction: every command registered on rootCmd
// must appear, so adding one forces an answer to "can this reach playback?".
func TestOnlyCommandsThatCanPlayChooseTheStorageBackend(t *testing.T) {
	// Whether the *decision* is yes is a separate question (mayStreamTorrent);
	// this asks only whether the decision is made at all.
	canPlay := map[string]bool{
		"lobster":  true, // the interactive picker, straight into resolveAndPlay
		"play":     true, // agentResolveAndPlay
		"history":  true, // historyRun -> resolveAndPlay
		"trending": true,
		"recent":   true,

		"version":  false,
		"doctor":   false,
		"find":     false,
		"episodes": false,
		"channels": false,
		// Cobra's own commands, if they have been materialised.
		"help":       false,
		"completion": false,
	}

	cmds := append([]*cobra.Command{rootCmd}, rootCmd.Commands()...)
	for _, c := range cmds {
		name := c.Name()
		want, listed := canPlay[name]
		if !listed {
			t.Errorf("command %q is not in this table: say whether it can reach resolveAndPlay, because loadConfig decides the torrent storage backend for it", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			isolateConfig(t)
			withOutputFlags(t, false, "")
			calls := observeStorageChoice(t)

			if err := loadConfig(c, nil); err != nil {
				t.Fatalf("loadConfig(%q) = %v, want nil", name, err)
			}
			if got := len(*calls) > 0; got != want {
				if want {
					t.Fatalf("loadConfig(%q) did not choose a torrent storage backend; this command reaches resolveAndPlay, so it can open a magnet and needs the backend that cannot SIGBUS", name)
				}
				t.Fatalf("loadConfig(%q) chose a torrent storage backend (willStream=%v); this command never reaches resolveAndPlay, so it re-execs for nothing and warns for nothing on Windows", name, *calls)
			}
		})
	}
}
