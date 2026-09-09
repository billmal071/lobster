package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
)

func seedHistory(t *testing.T, entries ...media.HistoryEntry) {
	t.Helper()
	for _, e := range entries {
		if err := history.Save(e); err != nil {
			t.Fatalf("seeding history: %v", err)
		}
	}
}

// The owner's live bug: IDs are provider-specific, every history row on disk
// was written under the old flixhq.ws default, and the default source is now
// Soap2Day. The exact-ID lookup therefore misses on every pre-existing row,
// so a film watched half-way restarts from zero — the third time watch
// positions have been lost to a source change.
//
// The fixture is the one that violates the guarantee: the same film, the same
// title, an ID minted by a different provider.
func TestPlayStreamResumesAcrossASourceChange(t *testing.T) {
	// No argv: --continue takes its registered default, which is on, and the
	// resume lookup only runs then.
	rec := resumeHarness(t)

	seedHistory(t, media.HistoryEntry{
		ID: "movie/free-the-matrix-hd-75043", Title: "The Matrix", Type: media.Movie,
		Position: 2109, Duration: 8160,
	})

	sel := media.SearchResult{ID: "soap2day/the-matrix", Title: "The Matrix", Year: "1999", Type: media.Movie}
	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	if err := playStream(stream, "The Matrix", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}
	if rec.startPos != 2109 {
		t.Fatalf("resumed at %.0fs, want 2109 — the row was written under another provider's ID", rec.startPos)
	}
}

// The false positive the fallback must not produce. Two distinct works can
// share a title, history rows carry no year to tell them apart, and resuming
// the wrong one drops the viewer into the middle of a film they have not
// watched. When the title alone cannot single out one row, the fallback must
// decline and let the watch start from the beginning.
func TestPlayStreamDoesNotResumeAnAmbiguousTitle(t *testing.T) {
	// No argv: --continue takes its registered default, which is on, and the
	// resume lookup only runs then.
	rec := resumeHarness(t)

	seedHistory(t,
		media.HistoryEntry{ID: "movie/dune-1984", Title: "Dune", Type: media.Movie, Position: 3600, Duration: 8220},
		media.HistoryEntry{ID: "movie/dune-2021", Title: "Dune", Type: media.Movie, Position: 1200, Duration: 9300},
	)

	sel := media.SearchResult{ID: "soap2day/dune", Title: "Dune", Year: "2021", Type: media.Movie}
	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	if err := playStream(stream, "Dune", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}
	if rec.startPos != 0 {
		t.Fatalf("resumed at %.0fs; two rows are titled Dune and nothing on disk says which one this is", rec.startPos)
	}
}

// A series row and a film row can share a title (a remake, an adaptation), and
// episode numbers are what a series resumes on. Neither may be crossed.
func TestPlayStreamResumeFallbackRespectsTypeAndEpisode(t *testing.T) {
	for _, c := range []struct {
		name  string
		entry media.HistoryEntry
		sel   media.SearchResult
		s, e  int
	}{
		{
			name:  "a series row must not resume a film",
			entry: media.HistoryEntry{ID: "tv/fargo", Title: "Fargo", Type: media.TV, Season: 1, Episode: 3, Position: 900},
			sel:   media.SearchResult{ID: "soap2day/fargo", Title: "Fargo", Type: media.Movie},
		},
		{
			name:  "another episode's position is not this episode's",
			entry: media.HistoryEntry{ID: "tv/fargo", Title: "Fargo", Type: media.TV, Season: 1, Episode: 3, Position: 900},
			sel:   media.SearchResult{ID: "soap2day/fargo", Title: "Fargo", Type: media.TV},
			s:     1, e: 4,
		},
		{
			name:  "another season's position is not this season's",
			entry: media.HistoryEntry{ID: "tv/fargo", Title: "Fargo", Type: media.TV, Season: 1, Episode: 3, Position: 900},
			sel:   media.SearchResult{ID: "soap2day/fargo", Title: "Fargo", Type: media.TV},
			s:     2, e: 3,
		},
		{
			name:  "a different film that merely starts the same way",
			entry: media.HistoryEntry{ID: "movie/dune", Title: "Dune", Type: media.Movie, Position: 3600},
			sel:   media.SearchResult{ID: "soap2day/dune-part-two", Title: "Dune: Part Two", Type: media.Movie},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := resumeHarness(t)
			seedHistory(t, c.entry)

			stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
			if err := playStream(stream, c.sel.Title, c.sel, c.s, c.e); err != nil {
				t.Fatalf("playStream: %v", err)
			}
			if rec.startPos != 0 {
				t.Fatalf("resumed at %.0fs from a row that is not this watch", rec.startPos)
			}
		})
	}
}

// The exact ID still wins outright: when both an exact row and a same-titled
// one are on disk, the fallback must not get a vote.
func TestPlayStreamPrefersTheExactIDOverATitleMatch(t *testing.T) {
	// No argv: --continue takes its registered default, which is on, and the
	// resume lookup only runs then.
	rec := resumeHarness(t)

	seedHistory(t,
		media.HistoryEntry{ID: "movie/old-the-matrix", Title: "The Matrix", Type: media.Movie, Position: 60},
		media.HistoryEntry{ID: "soap2day/the-matrix", Title: "The Matrix", Type: media.Movie, Position: 2109},
	)

	sel := media.SearchResult{ID: "soap2day/the-matrix", Title: "The Matrix", Type: media.Movie}
	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	if err := playStream(stream, "The Matrix", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}
	if rec.startPos != 2109 {
		t.Fatalf("resumed at %.0fs, want the exact ID's 2109s", rec.startPos)
	}
}

// `lobster history` re-searches the picked title with the current source and
// then looks for its saved ID in the results. After a source change that ID
// cannot appear, so the command dropped the user into a second picker for the
// title they had just picked. The same title/type rule that resumes the
// position can carry the selection through — and must decline in the same
// ambiguous case, where the second picker is the right answer.
func TestResultForHistoryEntryMatchesAcrossASourceChange(t *testing.T) {
	entry := media.HistoryEntry{ID: "movie/free-the-matrix-hd-75043", Title: "The Matrix", Type: media.Movie}
	results := []media.SearchResult{
		{ID: "soap2day/the-matrix-reloaded", Title: "The Matrix Reloaded", Type: media.Movie},
		{ID: "soap2day/the-matrix", Title: "The Matrix", Type: media.Movie},
	}

	got, ok := resultForHistoryEntry(results, entry)
	if !ok {
		t.Fatalf("resultForHistoryEntry found nothing; the current source lists the film under its own ID")
	}
	if got.ID != "soap2day/the-matrix" {
		t.Fatalf("matched %q, want soap2day/the-matrix", got.ID)
	}
}

func TestResultForHistoryEntryDeclinesWhenTheTitleIsAmbiguous(t *testing.T) {
	entry := media.HistoryEntry{ID: "movie/dune-old", Title: "Dune", Type: media.Movie}
	results := []media.SearchResult{
		{ID: "soap2day/dune-1984", Title: "Dune", Year: "1984", Type: media.Movie},
		{ID: "soap2day/dune-2021", Title: "Dune", Year: "2021", Type: media.Movie},
	}

	if got, ok := resultForHistoryEntry(results, entry); ok {
		t.Fatalf("resultForHistoryEntry chose %q; two films are titled Dune and the saved row carries no year", got.ID)
	}
}

func TestResultForHistoryEntryRefusesTheWrongMediaType(t *testing.T) {
	entry := media.HistoryEntry{ID: "tv/fargo", Title: "Fargo", Type: media.TV}
	results := []media.SearchResult{
		{ID: "soap2day/fargo", Title: "Fargo", Year: "1996", Type: media.Movie},
	}

	if got, ok := resultForHistoryEntry(results, entry); ok {
		t.Fatalf("resultForHistoryEntry matched %q, a film, for a series row", got.ID)
	}
}
