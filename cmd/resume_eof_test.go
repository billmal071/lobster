package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
)

func TestResumePositionTreatsEndOfFileAsFinished(t *testing.T) {
	cases := []struct {
		name string
		e    media.HistoryEntry
		want float64
	}{
		{"last second of film", media.HistoryEntry{Position: 8016, Duration: 8017}, 0},
		{"at duration", media.HistoryEntry{Position: 8017, Duration: 8017}, 0},
		{"inside the closing tail", media.HistoryEntry{Position: 7990, Duration: 8017}, 0},
		{"past the 98 percent mark but outside the closing seconds", media.HistoryEntry{Position: 7900, Duration: 8017}, 0},
		{"mid-film resumes", media.HistoryEntry{Position: 3123, Duration: 8000}, 3123},
		{"just before the tail resumes", media.HistoryEntry{Position: 7800, Duration: 8017}, 7800},
		{"unknown duration resumes", media.HistoryEntry{Position: 3123, Duration: 0}, 3123},
		{"short clip stopped near end", media.HistoryEntry{Position: 55, Duration: 60}, 0},
		{"short clip stopped early resumes", media.HistoryEntry{Position: 1, Duration: 60}, 1},
		{"short clip mid-way resumes", media.HistoryEntry{Position: 30, Duration: 60}, 30},
		{"nothing stored", media.HistoryEntry{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumePosition(tc.e); got != tc.want {
				t.Fatalf("resumePosition(pos=%g dur=%g) = %g, want %g", tc.e.Position, tc.e.Duration, got, tc.want)
			}
		})
	}
}

func TestSearchPathDoesNotResumeAtEndOfFile(t *testing.T) {
	rec := resumeHarness(t, "--base", "yts", "underworld")

	if err := history.Save(media.HistoryEntry{
		ID: "yts/4003", Title: "Underworld", Type: media.Movie,
		Position: 8016, Duration: 8017,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	stream := &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}
	sel := media.SearchResult{ID: "yts/4003", Title: "Underworld", Type: media.Movie}
	if err := playStream(stream, "Underworld", sel, 0, 0); err != nil {
		t.Fatalf("playStream: %v", err)
	}

	if rec.startPos != 0 {
		t.Fatalf("player started at %g, want 0: a watch stored at EOF must start over", rec.startPos)
	}
}

func TestSessionPathDoesNotResumeAtEndOfFile(t *testing.T) {
	rec := resumeHarness(t)

	if err := history.Save(media.HistoryEntry{
		ID: "tv/s", Title: "S", Type: media.TV,
		Season: 1, Episode: 3, Position: 2399, Duration: 2400,
	}); err != nil {
		t.Fatalf("seeding history: %v", err)
	}

	prov := &stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}}
	if err := playCurrentEpisode(sessionForTest(prov)); err != nil {
		t.Fatalf("playCurrentEpisode: %v", err)
	}

	if rec.startPos != 0 {
		t.Fatalf("episode started at %g, want 0: an episode stored at EOF must start over", rec.startPos)
	}
}
