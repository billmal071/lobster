package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"lobster/internal/config"
	"lobster/internal/provider"
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
//
// Binding the flags overwrites all eleven flag globals with their registered
// defaults (saveFlagGlobals), so anything the caller pinned first — notably
// withOutputFlags' flagJSON and flagDownload, which mayStreamTorrent reads —
// is restored the moment parsing is done.
func refCmd(t *testing.T, argv ...string) *cobra.Command {
	t.Helper()
	restoreFlags := saveFlagGlobals(t)
	defer restoreFlags()
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

// A ref carries a third `base` input, and it never passes through
// config.Validate: decodeRef validates Type strictly and Base not at all, and
// applyRefBase assigns it into cfg long after the last Validate() call. So the
// canonicalisation that made mayStreamTorrent, baseIsAuto and newProvider
// agree about `base = "AUTO"` covered the file and the flag but not the token,
// and every disagreement it closed was reachable again through `play --ref`.
//
// The worst of them is a ref base of " yts ": newProvider matches by substring
// so the run really does get YTS and a magnet, while mayStreamTorrent compares
// with EqualFold and answers false — so WarnLateStorageRisk stays silent and
// the magnet is served on the memory-mapped backend, which is the exact case
// this file's first test exists for.
//
// This asserts the agreement across all three readers, on the ref path, rather
// than any one of them: the round-7 fixture asked only config.Validate, which
// is why the token path shipped broken underneath it.
func TestARefSuppliedBaseIsCanonicalForEveryReaderOfBase(t *testing.T) {
	for _, tc := range []struct {
		refBase  string
		wantBase string
		wantAuto bool
		wantYTS  bool
	}{
		{refBase: " YTS ", wantBase: "yts", wantAuto: false, wantYTS: true},
		{refBase: "AUTO", wantBase: config.BaseAuto, wantAuto: true, wantYTS: false},
	} {
		t.Run(tc.refBase, func(t *testing.T) {
			unsetStorageEnv(t)
			withOutputFlags(t, false, "")
			prev := cfg
			c := config.Default()
			// newProvider's domain overrides fetch a catalog feed over the
			// network; nothing here needs it.
			c.TBCPLFeed = false
			c.Base = "soap2day"
			cfg = c
			t.Cleanup(func() { cfg = prev })

			warnings := captureWarnings(t)
			applyRefBase(refCmd(t), playRef{Base: tc.refBase})

			if cfg.Base != tc.wantBase {
				t.Errorf("applyRefBase(ref base %q) left base %q, want %q", tc.refBase, cfg.Base, tc.wantBase)
			}
			if got := baseIsAuto(); got != tc.wantAuto {
				t.Errorf("baseIsAuto() after ref base %q = %v, want %v", tc.refBase, got, tc.wantAuto)
			}
			if _, isYTS := newProvider().(*provider.YTS); isYTS != tc.wantYTS {
				t.Errorf("newProvider() after ref base %q = %T, YTS=%v, want YTS=%v", tc.refBase, newProvider(), isYTS, tc.wantYTS)
			}
			// Both bases can reach a magnet, and both changed the base, so
			// the late-arrival notice has to fire for each of them.
			if !mayStreamTorrent(cfg) {
				t.Errorf("mayStreamTorrent() after ref base %q = false; this run can open a magnet, so the backend risk is real", tc.refBase)
			}
			if joined := strings.Join(*warnings, "\n"); !strings.Contains(joined, "SIGBUS") {
				t.Errorf("applyRefBase(ref base %q) said nothing about the storage backend (warnings: %q)", tc.refBase, *warnings)
			}
		})
	}
}

// The `changed` comparison decides whether the notice fires, and it compared
// raw strings: a run already configured for yts that replays a ref stamped
// " YTS " read as a change and warned about a backend that was chosen
// correctly at startup. Canonicalising before the comparison is what makes the
// notice mean "this run moved".
func TestApplyRefBaseStaysQuietWhenOnlyTheSpellingOfTheBaseDiffers(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	prev := cfg
	cfg = &config.Config{Base: "yts", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	warnings := captureWarnings(t)
	applyRefBase(refCmd(t), playRef{Base: " YTS "})

	// Without this, silence would prove nothing: an un-normalised " YTS "
	// also reads as "not a torrent base" to mayStreamTorrent, so the notice
	// would be suppressed by the risk gate rather than by `changed`, and the
	// fixture could not see its own subject.
	if !mayStreamTorrent(cfg) {
		t.Fatalf("mayStreamTorrent() = false for base %q; this run can open a magnet, so silence here must come from the base not having changed", cfg.Base)
	}
	if len(*warnings) != 0 {
		t.Fatalf("applyRefBase warned on a ref naming the base this run already had, spelled differently: %q", *warnings)
	}
}

// A ref whose base is nothing but whitespace names no source. Assigning it
// verbatim replaces a working base with "", which newProvider matches against
// nothing and falls through to MovieBox — the provider that answers a
// 22-episode season with 10 fabricated rows.
func TestApplyRefBaseIgnoresAWhitespaceOnlyRefBase(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	prev := cfg
	cfg = &config.Config{Base: "soap2day", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	warnings := captureWarnings(t)
	applyRefBase(refCmd(t), playRef{Base: "   "})

	if cfg.Base != "soap2day" {
		t.Fatalf("applyRefBase(ref base %q) left base %q, want soap2day untouched", "   ", cfg.Base)
	}
	if len(*warnings) != 0 {
		t.Fatalf("applyRefBase warned for a ref that named no base at all: %q", *warnings)
	}
}

// registerPersistentFlags binds every persistent flag to a package global, and
// pflag writes each flag's registered default into its target at registration
// time. So building a throwaway command resets all eleven of them, and refCmd
// used to restore only its playbackCommands entry — a test that pinned
// flagBase or flagQuality had it silently cleared, and nothing red only
// because the values it happened to pin equalled the defaults.
//
// continueFromCLI (continue_default_test.go) already saved and restored the
// full set for the same reason; this asserts the shared helper both now use.
func TestRefCmdRestoresTheFlagGlobalsItRegistrationClobbers(t *testing.T) {
	dl, lang, alang := flagDownload, flagLanguage, flagAudioLang
	prov, qual, plr, base := flagProvider, flagQuality, flagPlayer, flagBase
	nosubs, cont, js, dbg := flagNoSubs, flagContinue, flagJSON, flagDebug
	t.Cleanup(func() {
		flagDownload, flagLanguage, flagAudioLang = dl, lang, alang
		flagProvider, flagQuality, flagPlayer, flagBase = prov, qual, plr, base
		flagNoSubs, flagContinue, flagJSON, flagDebug = nosubs, cont, js, dbg
	})

	// Pinned to values that differ from every registered default, so a reset
	// is visible. flagContinue's default is true, so false is the tell.
	flagDownload, flagLanguage, flagAudioLang = "/tmp/dl", "spanish", "japanese"
	flagProvider, flagQuality, flagPlayer, flagBase = "upcloud", "720", "vlc", "soap2day"
	flagNoSubs, flagContinue, flagJSON, flagDebug = true, false, true, true

	refCmd(t)

	for _, tc := range []struct{ name, got, want string }{
		{"flagDownload", flagDownload, "/tmp/dl"},
		{"flagLanguage", flagLanguage, "spanish"},
		{"flagAudioLang", flagAudioLang, "japanese"},
		{"flagProvider", flagProvider, "upcloud"},
		{"flagQuality", flagQuality, "720"},
		{"flagPlayer", flagPlayer, "vlc"},
		{"flagBase", flagBase, "soap2day"},
	} {
		if tc.got != tc.want {
			t.Errorf("refCmd left %s = %q, want %q — registering the persistent flags overwrote it", tc.name, tc.got, tc.want)
		}
	}
	for _, tc := range []struct {
		name      string
		got, want bool
	}{
		{"flagNoSubs", flagNoSubs, true},
		{"flagContinue", flagContinue, false},
		{"flagJSON", flagJSON, true},
		{"flagDebug", flagDebug, true},
	} {
		if tc.got != tc.want {
			t.Errorf("refCmd left %s = %v, want %v — registering the persistent flags overwrote it", tc.name, tc.got, tc.want)
		}
	}
}
