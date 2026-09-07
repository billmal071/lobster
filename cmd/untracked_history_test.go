package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
)

// untrackedResult is what vlc, iina and celluloid return: the watch happened,
// but no position was ever measured, so the zero Position is a default.
func untrackedResult() player.PlayResult {
	return player.PlayResult{PositionUnknown: true, PositionUntracked: true}
}

// The owner's live bug: watching a title in VLC wrote position 0 over the
// resume point an earlier mpv watch had recorded, so the next launch restarted
// from the beginning. VLC cannot measure a position, so it must leave the
// stored one alone.
func TestPlayStreamKeepsStoredPositionForAnUntrackedPlayer(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: untrackedResult()})

	prior := media.HistoryEntry{
		ID: "movie/v", Title: "V", Type: media.Movie,
		Position: 2109, Duration: 5400,
	}
	if err := history.Save(prior); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/v", Title: "V", Type: media.Movie}
	if err := playStream(stream, "V", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "movie/v" {
			if e.Position != 2109 || e.Duration != 5400 {
				t.Fatalf("history entry = %+v, want position 2109 / duration 5400 kept: an untracked player must not overwrite a real resume point", e)
			}
			return
		}
	}
	t.Fatalf("the pre-existing history entry for movie/v is gone; entries: %+v", entries)
}

// The other half: an untracked player must still record the watch. Skipping
// the save outright (which is what a tracked-but-blind session does) would
// mean a title only ever watched in VLC never appears in `lobster history`.
func TestPlayStreamRecordsAnUntrackedWatchOfANewTitle(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: untrackedResult()})

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/w", Title: "W", Type: media.Movie}
	if err := playStream(stream, "W", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "movie/w" {
			if e.Position != 0 {
				t.Fatalf("history position = %g, want 0: there was no resume point to keep", e.Position)
			}
			return
		}
	}
	t.Fatalf("no history entry for movie/w: an untracked watch must still be recorded; entries: %+v", entries)
}

// mpv's behaviour must not move. A tracked session that never observed a
// position makes no claim that the watch reached the player at all — the IPC
// socket never came up — so it writes nothing, not even a new row. That is the
// #52 contract, and the untracked-player change must not widen it.
func TestPlayStreamWritesNothingWhenTrackedPlayerNeverObserved(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{
		result: player.PlayResult{PositionUnknown: true},
	})

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "movie/u", Title: "U", Type: media.Movie}
	if err := playStream(stream, "U", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "movie/u" {
			t.Fatalf("history row created for a tracked session that observed nothing: %+v", e)
		}
	}
}

// The session (playlist) path saves through saveHistory rather than
// playStream, so it needs the same guarantee proved separately.
func TestSessionKeepsStoredPositionForAnUntrackedPlayer(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: untrackedResult()})

	prior := media.HistoryEntry{
		ID: "tv/s", Title: "S", Type: media.TV,
		Season: 1, Episode: 3, Position: 1500, Duration: 2400,
	}
	if err := history.Save(prior); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	sess := sessionForTest(prov)
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}
	saveHistory(sess)

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "tv/s" && e.Season == 1 && e.Episode == 3 {
			if e.Position != 1500 || e.Duration != 2400 {
				t.Fatalf("history entry = %+v, want position 1500 / duration 2400 kept for an untracked player", e)
			}
			return
		}
	}
	t.Fatalf("the pre-existing history entry for tv/s S01E03 is gone; entries: %+v", entries)
}

// And the session path must record an untracked episode nobody has watched
// before, for the same reason playStream must.
func TestSessionRecordsAnUntrackedWatchOfANewEpisode(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: untrackedResult()})

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	sess := sessionForTest(prov)
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}
	saveHistory(sess)

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "tv/s" && e.Season == 1 && e.Episode == 3 {
			if e.Position != 0 {
				t.Fatalf("history position = %g, want 0: there was no resume point to keep", e.Position)
			}
			return
		}
	}
	t.Fatalf("no history entry for tv/s S01E03: an untracked watch must still be recorded; entries: %+v", entries)
}

// mpv's session-path behaviour must not move either: a tracked-but-blind
// episode writes no row at all.
func TestSessionWritesNothingWhenTrackedPlayerNeverObserved(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{
		result: player.PlayResult{PositionUnknown: true},
	})

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	sess := sessionForTest(prov)
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}
	saveHistory(sess)

	entries, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load: %v", err)
	}
	for _, e := range entries {
		if e.ID == "tv/s" {
			t.Fatalf("history row created for a tracked session that observed nothing: %+v", e)
		}
	}
}
