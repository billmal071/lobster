package cmd

import (
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/playlist"
)

func gappedPlaybackSession(seasonIdx, episodeIdx int) *playlist.Session {
	p := &stubProvider{
		episodesWithErr: []media.Episode{{ID: "e2", Number: 2}, {ID: "e5", Number: 5}},
		episodesErr:     gapErr(),
	}
	seasons := []media.Season{{ID: "1", Number: 1}, {ID: "2", Number: 2}}
	eps := []media.Episode{{ID: "e1", Number: 1}, {ID: "e2", Number: 2}}
	sel := media.SearchResult{ID: "eztv/1", Title: "Show", Type: media.TV}
	return newPlaybackSession(p, sel, sel.ID, nil, seasons, eps, seasonIdx, episodeIdx, false, false)
}

func TestPlaybackSessionWarnsWhenNextCrossesIntoAGappedSeason(t *testing.T) {
	warnings := captureWarnings(t)
	sess := gappedPlaybackSession(0, 1)

	if _, err := sess.Next(); err != nil {
		t.Fatalf("crossing: %v", err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "no standalone release for some episodes of season 2") {
		t.Fatalf("warnings = %q, want one saying season 2 lacks standalone releases for some episodes", *warnings)
	}

	if _, err := sess.Next(); err != nil {
		t.Fatalf("Next within the season: %v", err)
	}
	if len(*warnings) != 1 {
		t.Fatalf("warnings = %q, want no second warning for a step inside the same season", *warnings)
	}
}

func TestPlaybackSessionWarnsWhenPreviousCrossesIntoAGappedSeason(t *testing.T) {
	warnings := captureWarnings(t)
	sess := gappedPlaybackSession(1, 0)

	if _, err := sess.Previous(); err != nil {
		t.Fatalf("crossing: %v", err)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "no standalone release for some episodes of season 1") {
		t.Fatalf("warnings = %q, want one saying season 1 lacks standalone releases for some episodes", *warnings)
	}
}

func TestPlaybackSessionDoesNotWarnOnACleanCrossing(t *testing.T) {
	warnings := captureWarnings(t)
	sess := gappedPlaybackSession(0, 1)
	sess.Provider = &stubProvider{episodes: []media.Episode{{ID: "e1", Number: 1}}}

	if _, err := sess.Next(); err != nil {
		t.Fatalf("crossing: %v", err)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %q, want none for a complete season list", *warnings)
	}
}
