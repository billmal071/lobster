package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
)

// stubCheckpointPlayer is a stubPlayerImpl that also implements
// player.Checkpointer. If fire is set, Play invokes the installed checkpoint
// callback with mid (position, duration) — simulating trackPlayback's periodic
// checkpoint — and snapshots the history file right after, so tests can assert
// what a hard shutdown at that moment would have found on disk.
type stubCheckpointPlayer struct {
	stubPlayerImpl
	fn         func(position, duration float64)
	fire       bool
	mid        [2]float64
	midEntries []media.HistoryEntry
}

func (s *stubCheckpointPlayer) SetCheckpoint(fn func(position, duration float64)) { s.fn = fn }

func (s *stubCheckpointPlayer) Play(st *media.Stream, title string, startPos float64, subFiles []string) (player.PlayResult, error) {
	if s.fire && s.fn != nil {
		s.fn(s.mid[0], s.mid[1])
		s.midEntries, _ = history.Load()
	}
	return s.stubPlayerImpl.Play(st, title, startPos, subFiles)
}

// A checkpoint during playback must land in history immediately (that row is
// all a power cut leaves behind), and the exit-time save must still win
// afterwards: the final position differs from the checkpoint, and it is the
// final one that must be on disk when playStream returns.
func TestPlayStreamCheckpointPersistsMidPlaybackAndExitSaveWins(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 1234, Duration: 5400}},
		fire:           true,
		mid:            [2]float64{600, 5400},
	}
	playStreamHarness(t, stub)

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "tv/z", Title: "Z", Type: media.TV}

	if err := playStream(stream, "Z", sel, 2, 5); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	// The state a hard shutdown mid-watch would have left behind.
	var midFound bool
	for _, e := range stub.midEntries {
		if e.ID == "tv/z" && e.Season == 2 && e.Episode == 5 {
			midFound = true
			if e.Position != 600 || e.Duration != 5400 {
				t.Fatalf("mid-playback history = pos %g dur %g, want 600/5400 from the checkpoint", e.Position, e.Duration)
			}
			if e.Title != "Z" || e.Type != media.TV {
				t.Fatalf("mid-playback entry metadata = %+v, want title Z type tv (checkpoint rows must be structurally identical to final rows)", e)
			}
		}
	}
	if !midFound {
		t.Fatalf("no history entry existed during playback; a hard shutdown would have lost the position (mid entries: %+v)", stub.midEntries)
	}

	// The exit-time save is more precise and must overwrite the checkpoint.
	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	var rows int
	for _, e := range entries {
		if e.ID == "tv/z" && e.Season == 2 && e.Episode == 5 {
			rows++
			if e.Position != 1234 {
				t.Fatalf("final history position = %g, want 1234 (the exit-time save must win over the 600s checkpoint)", e.Position)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("found %d rows for tv/z S02E05, want exactly 1 (checkpoint must update in place, not append)", rows)
	}
}

// A zero-position checkpoint must not write: recording 0 would clobber a real
// resume position from an earlier watch of the same title.
func TestPlayStreamCheckpointSkipsZeroPosition(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 42, Duration: 100}},
		fire:           true,
		mid:            [2]float64{0, 100},
	}
	playStreamHarness(t, stub)

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/zero", Title: "Zero", Type: media.Movie}

	if err := playStream(stream, "Zero", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}
	for _, e := range stub.midEntries {
		if e.ID == "movie/zero" {
			t.Fatalf("zero-position checkpoint wrote a history entry: %+v", e)
		}
	}
}

// With history disabled no checkpoint callback may be installed at all — the
// player must not be handed a writer that the config says must never write.
func TestPlayStreamNoCheckpointWhenHistoryDisabled(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 99, Duration: 100}},
	}
	playStreamHarness(t, stub)
	cfg.History = false

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/nohist", Title: "NoHist", Type: media.Movie}

	if err := playStream(stream, "NoHist", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}
	if stub.fn != nil {
		t.Fatal("checkpoint callback installed with cfg.History disabled")
	}
}
