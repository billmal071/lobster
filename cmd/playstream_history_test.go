package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
)

// stubPlayerImpl satisfies player.Player without launching anything.
type stubPlayerImpl struct {
	result player.PlayResult
	err    error
}

func (s *stubPlayerImpl) Play(*media.Stream, string, float64, []string) (player.PlayResult, error) {
	return s.result, s.err
}
func (s *stubPlayerImpl) Name() string    { return "stub" }
func (s *stubPlayerImpl) Available() bool { return true }

// playStreamHarness points history at a temp dir, installs a stub player and
// a minimal cfg, and pins the playStream-relevant flags so no subtitle
// search, JSON mode or download path runs.
func playStreamHarness(t *testing.T, stub *stubPlayerImpl) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp) // history location on unix (config.dataDir)
	t.Setenv("LOCALAPPDATA", tmp)  // and on windows

	prevCfg := cfg
	cfg = &config.Config{History: true, Player: "stub"}
	t.Cleanup(func() { cfg = prevCfg })

	prevNew := newPlayer
	newPlayer = func(name, audioLang string) player.Player { return stub }
	t.Cleanup(func() { newPlayer = prevNew })

	prevJSON, prevNoSubs, prevDL, prevCont := flagJSON, flagNoSubs, flagDownload, flagContinue
	flagJSON, flagNoSubs, flagDownload, flagContinue = false, true, "", false
	t.Cleanup(func() {
		flagJSON, flagNoSubs, flagDownload, flagContinue = prevJSON, prevNoSubs, prevDL, prevCont
	})
}

// A position tracked before an abnormal player exit (killed, crashed) must
// still reach history — Play returns the populated result alongside the
// error, and discarding it loses the resume point for exactly the watches
// that end uncleanly. The error itself must still surface to the caller.
func TestPlayStreamPersistsPositionOnAbnormalExit(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{
		result: player.PlayResult{Position: 1234, Duration: 5400},
		err:    errors.New("mpv exited: signal: killed"),
	})

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/x", Title: "X", Type: media.Movie}

	err := playStream(stream, "X", sel, 0, 0)
	if err == nil {
		t.Fatal("playStream returned nil; the player error must still reach the caller")
	}
	if !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("playStream error = %v, want the player error wrapped", err)
	}

	entries, loadErr := history.Load()
	if loadErr != nil {
		t.Fatalf("history.Load: %v", loadErr)
	}
	for _, e := range entries {
		if e.ID == "movie/x" {
			if e.Position != 1234 {
				t.Fatalf("history position = %g, want 1234", e.Position)
			}
			if e.Duration != 5400 {
				t.Fatalf("history duration = %g, want 5400", e.Duration)
			}
			return
		}
	}
	t.Fatalf("no history entry for movie/x after abnormal exit; entries: %+v", entries)
}

// An abnormal exit with nothing tracked (position 0) must not write an entry:
// there is no resume point to keep, and recording 0 would overwrite a real
// position from an earlier watch of the same title.
func TestPlayStreamSkipsHistoryOnAbnormalExitWithoutPosition(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{
		result: player.PlayResult{},
		err:    errors.New("stream failed to load"),
	})

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/x", Title: "X", Type: media.Movie}

	if err := playStream(stream, "X", sel, 0, 0); err == nil {
		t.Fatal("playStream returned nil, want the player error")
	}

	entries, loadErr := history.Load()
	if loadErr != nil {
		t.Fatalf("history.Load: %v", loadErr)
	}
	for _, e := range entries {
		if e.ID == "movie/x" {
			t.Fatalf("history entry written for a failed play with position 0: %+v", e)
		}
	}
}

// The clean-exit path must keep saving exactly as before, including a
// position of 0 (a finished watch mpv reports as 0 still records the entry).
func TestPlayStreamStillSavesOnCleanExit(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{
		result: player.PlayResult{Position: 42, Duration: 100},
	})

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/y", Title: "Y", Type: media.Movie}

	if err := playStream(stream, "Y", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	entries, loadErr := history.Load()
	if loadErr != nil {
		t.Fatalf("history.Load: %v", loadErr)
	}
	for _, e := range entries {
		if e.ID == "movie/y" && e.Position == 42 {
			return
		}
	}
	t.Fatalf("no history entry for movie/y after clean exit; entries: %+v", entries)
}
