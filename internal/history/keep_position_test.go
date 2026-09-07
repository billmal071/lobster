package history

import (
	"testing"

	"lobster/internal/media"
)

// A player that cannot report a position hands the save path an entry whose
// Position and Duration are zero by default rather than by measurement.
// Writing that entry as-is replaces the resume point an earlier, tracked watch
// recorded, which is exactly the bug: watch ten minutes of a film in VLC and
// the mpv resume point for it is gone.
func TestSaveKeepingPositionKeepsTheStoredPosition(t *testing.T) {
	setDataHome(t, t.TempDir())

	prior := media.HistoryEntry{
		ID: "movie/keep", Title: "Keep", Type: media.Movie,
		Position: 2109, Duration: 5400,
	}
	if err := Save(prior); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	untracked := prior
	untracked.Position, untracked.Duration = 0, 0
	if err := SaveKeepingPosition(untracked); err != nil {
		t.Fatalf("SaveKeepingPosition: %v", err)
	}

	entries, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("history holds %d entries, want 1 updated in place: %+v", len(entries), entries)
	}
	if entries[0].Position != 2109 {
		t.Fatalf("position = %g, want 2109 kept: an untracked watch must not write its default 0 over a real resume point", entries[0].Position)
	}
	if entries[0].Duration != 5400 {
		t.Fatalf("duration = %g, want 5400 kept alongside the position", entries[0].Duration)
	}
}

// The other half of the same guarantee: with nothing stored there is no resume
// point to protect, so the watch itself must still be recorded — otherwise a
// title only ever watched in VLC would never appear in `lobster history`.
func TestSaveKeepingPositionRecordsAnUnseenTitle(t *testing.T) {
	setDataHome(t, t.TempDir())

	entry := media.HistoryEntry{
		ID: "tv/new", Title: "New", Type: media.TV,
		Season: 2, Episode: 5,
	}
	if err := SaveKeepingPosition(entry); err != nil {
		t.Fatalf("SaveKeepingPosition: %v", err)
	}

	entries, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("history holds %d entries, want the new watch recorded: %+v", len(entries), entries)
	}
	if entries[0].ID != "tv/new" || entries[0].Season != 2 || entries[0].Episode != 5 {
		t.Fatalf("recorded entry = %+v, want tv/new S02E05", entries[0])
	}
	if entries[0].Position != 0 {
		t.Fatalf("position = %g, want 0 for a title with no stored resume point", entries[0].Position)
	}
}

// Only the entry for the same (ID, Season, Episode) may donate a position:
// episode 3's resume point must never leak onto episode 4.
func TestSaveKeepingPositionMatchesOnEpisode(t *testing.T) {
	setDataHome(t, t.TempDir())

	other := media.HistoryEntry{
		ID: "tv/s", Title: "S", Type: media.TV,
		Season: 1, Episode: 3, Position: 1500, Duration: 2400,
	}
	if err := Save(other); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	next := media.HistoryEntry{ID: "tv/s", Title: "S", Type: media.TV, Season: 1, Episode: 4}
	if err := SaveKeepingPosition(next); err != nil {
		t.Fatalf("SaveKeepingPosition: %v", err)
	}

	entries, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, e := range entries {
		if e.Season == 1 && e.Episode == 4 && e.Position != 0 {
			t.Fatalf("S01E04 position = %g, want 0: a different episode's position must not be carried over", e.Position)
		}
	}
	if len(entries) != 2 {
		t.Fatalf("history holds %d entries, want 2 (E03 kept, E04 added): %+v", len(entries), entries)
	}
}
