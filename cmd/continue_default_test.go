package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
)

// posRecordingPlayer records the resume position playback was started at.
// That argument is the whole observable effect of the --continue gate, so it
// is what these tests assert on.
type posRecordingPlayer struct {
	startPos float64
	result   player.PlayResult
}

func (p *posRecordingPlayer) Play(_ *media.Stream, _ string, startPos float64, _ []string) (player.PlayResult, error) {
	p.startPos = startPos
	return p.result, nil
}
func (p *posRecordingPlayer) Name() string    { return "stub" }
func (p *posRecordingPlayer) Available() bool { return true }

// continueFromCLI returns the value flagContinue holds after a real `lobster`
// invocation with these arguments has been parsed. It binds the persistent
// flags onto a throwaway command so the flag's registered default is applied
// afresh — reading rootCmd's flag instead would only report whatever the
// package globals already happen to hold, which is precisely what a test of
// the default must not trust.
//
// The globals are restored before returning, so callers may pin flags
// (playStreamHarness) either side of this call without interference.
func continueFromCLI(t *testing.T, args ...string) bool {
	t.Helper()

	dl, lang, alang := flagDownload, flagLanguage, flagAudioLang
	prov, qual, plr, base := flagProvider, flagQuality, flagPlayer, flagBase
	nosubs, cont, js, dbg := flagNoSubs, flagContinue, flagJSON, flagDebug
	restore := func() {
		flagDownload, flagLanguage, flagAudioLang = dl, lang, alang
		flagProvider, flagQuality, flagPlayer, flagBase = prov, qual, plr, base
		flagNoSubs, flagContinue, flagJSON, flagDebug = nosubs, cont, js, dbg
	}
	t.Cleanup(restore)

	tmp := &cobra.Command{Use: "tmp"}
	registerPersistentFlags(tmp)
	err := tmp.PersistentFlags().Parse(args)
	got := flagContinue
	restore()
	if err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	return got
}

// resumeHarness installs a position-recording player and a hermetic history
// dir, then sets flagContinue to whatever the given argv would produce.
// Ordering matters: playStreamHarness pins --no-subs on and --json off so no
// subtitle lookup or metadata short-circuit runs, and it also zeroes
// flagContinue, so the CLI-derived value is applied last.
func resumeHarness(t *testing.T, args ...string) *posRecordingPlayer {
	t.Helper()
	cont := continueFromCLI(t, args...)
	rec := &posRecordingPlayer{}
	playStreamHarness(t, rec)
	flagContinue = cont
	return rec
}

// The reported bug: `lobster --base yts "captain america: winter soldier"`
// restarted a film history held a good position for, because --continue was
// registered false and only the agent `play` command opted back in. Resuming
// is the default on every playback entry point, so the interactive search
// path must start the player at the stored position.
func TestSearchPathResumesFromHistoryByDefault(t *testing.T) {
	rec := resumeHarness(t, "--base", "yts", "captain america: winter soldier")

	if err := history.Save(media.HistoryEntry{
		ID: "movie/ca2", Title: "CA2", Type: media.Movie,
		Position: 3123, Duration: 8000,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/ca2", Title: "CA2", Type: media.Movie}
	if err := playStream(stream, "CA2", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	if rec.startPos != 3123 {
		t.Fatalf("player started at %g, want 3123: an interactive search must resume from history by default", rec.startPos)
	}
}

// --continue=false is a deliberate fresh start and must still win on the
// search path. The history entry here is deliberately a resumable one: a test
// that fed an empty history could not tell the gate from its absence.
func TestSearchPathExplicitContinueFalseStartsFromZero(t *testing.T) {
	rec := resumeHarness(t, "--continue=false", "captain america: winter soldier")

	if err := history.Save(media.HistoryEntry{
		ID: "movie/ca2", Title: "CA2", Type: media.Movie,
		Position: 3123, Duration: 8000,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/ca2", Title: "CA2", Type: media.Movie}
	if err := playStream(stream, "CA2", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	if rec.startPos != 0 {
		t.Fatalf("player started at %g, want 0: --continue=false must not resume", rec.startPos)
	}
}

// The TV/session path has its own copy of the resume gate
// (playCurrentEpisode), reached by picking a show interactively. It must
// default to resuming too, or a part-watched episode restarts.
func TestSessionPathResumesFromHistoryByDefault(t *testing.T) {
	rec := resumeHarness(t)

	if err := history.Save(media.HistoryEntry{
		ID: "tv/s", Title: "S", Type: media.TV,
		Season: 1, Episode: 3, Position: 900, Duration: 2400,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	if err := playCurrentEpisode(sessionForTest(prov)); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}

	if rec.startPos != 900 {
		t.Fatalf("episode started at %g, want 900: the session path must resume from history by default", rec.startPos)
	}
}

// ...and must still honour an explicit --continue=false, with a resumable
// entry present for it to ignore.
func TestSessionPathExplicitContinueFalseStartsFromZero(t *testing.T) {
	rec := resumeHarness(t, "--continue=false")

	if err := history.Save(media.HistoryEntry{
		ID: "tv/s", Title: "S", Type: media.TV,
		Season: 1, Episode: 3, Position: 900, Duration: 2400,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	if err := playCurrentEpisode(sessionForTest(prov)); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}

	if rec.startPos != 0 {
		t.Fatalf("episode started at %g, want 0: --continue=false must not resume on the session path", rec.startPos)
	}
}

// The detach interaction, half one: forwardedArgs omits a flag the caller did
// not pass, so the supervised child is launched with no --continue at all and
// falls back to its own registered default. That is only a resume if the
// registered default is itself "resume" — the argv check alone would pass just
// as happily with the old false default, so both halves are asserted here.
func TestDetachedChildResumesWithoutExplicitContinue(t *testing.T) {
	withInheritedFlags(t, playCmd, "continue")
	playCmd.Flags().Lookup("continue").Changed = false // the caller passed nothing

	argv := detachChildArgv(playCmd, "/usr/bin/lobster")
	for _, a := range argv {
		if strings.HasPrefix(a, "--continue") {
			t.Fatalf("detachChildArgv(...) = %v, forwarded %q though --continue was not passed", argv, a)
		}
	}

	if !continueFromCLI(t) {
		t.Fatal("a supervised child launched without --continue does not resume; a detached play would start from the beginning")
	}
}

// The detach interaction, half two: an explicit --continue=false must reach
// the child as --continue=false (not a bare --continue, which cobra reads as
// true) and must still mean "do not resume" once the child parses it.
func TestDetachedChildHonoursExplicitContinueFalse(t *testing.T) {
	withInheritedFlags(t, playCmd, "continue")
	if err := playCmd.Flags().Set("continue", "false"); err != nil {
		t.Fatalf("Set --continue=false: %v", err)
	}

	argv := detachChildArgv(playCmd, "/usr/bin/lobster")
	if !containsArg(argv, "--continue=false") {
		t.Fatalf("detachChildArgv(...) = %v, missing --continue=false", argv)
	}

	if continueFromCLI(t, "--continue=false") {
		t.Fatal("a child parsing --continue=false still has continue on; it would silently resume")
	}
}
