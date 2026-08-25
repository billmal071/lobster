package cmd

import (
	"testing"

	"lobster/internal/media"
)

// Distinct works that happen to share a title (the 2002 film, the 1994 cartoon,
// the 1967 cartoon) must survive dedup — collapsing them on title alone hid the
// whole franchise behind a single entry.
func TestDeduplicateResultsKeepsSameTitleDifferentWorks(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002"},
		{ID: "tv/888", Title: "Spider-Man", Type: media.TV, Year: "1994"},
		{ID: "tv/1482", Title: "Spider-Man", Type: media.TV, Year: "1967"},
	}
	// A second provider returns the same three works, plus richer metadata for
	// the 2002 film — those must merge, not stack up.
	fallback := [][]media.SearchResult{{
		{ID: "movie/557", Title: "spider-man", Type: media.Movie, Year: "2002", Poster: "p.jpg", URL: "u"},
		{ID: "tv/888", Title: "Spider-Man", Type: media.TV, Year: "1994"},
		{ID: "tv/1482", Title: "Spider-Man", Type: media.TV, Year: "1967"},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 3 {
		t.Fatalf("len(merged) = %d, want 3: %+v", len(merged), merged)
	}
	if merged[0].Poster != "p.jpg" {
		t.Errorf("richer duplicate did not replace the sparse one: %+v", merged[0])
	}
}

// A provider that omits the year still merges into the year-bearing entry for
// the same title and media type, rather than producing a near-duplicate row.
func TestDeduplicateResultsMergesMissingYear(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002", Poster: "p.jpg"},
	}
	fallback := [][]media.SearchResult{{
		{ID: "x", Title: "Spider-Man", Type: media.Movie},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
	if merged[0].Year != "2002" {
		t.Errorf("merged entry lost its year: %+v", merged[0])
	}
}

// The reverse order: the year-less entry arrives first and is upgraded when the
// richer, year-bearing one shows up.
func TestDeduplicateResultsUpgradesYearlessEntry(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "x", Title: "Spider-Man", Type: media.Movie},
	}
	fallback := [][]media.SearchResult{{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002", Poster: "p.jpg"},
		{ID: "tv/888", Title: "Spider-Man", Type: media.TV, Year: "1994"},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2: %+v", len(merged), merged)
	}
	if merged[0].Year != "2002" || merged[0].ID != "movie/557" {
		t.Errorf("year-less entry was not upgraded: %+v", merged[0])
	}
}

// When a dated entry merges into a year-less one, the year must survive even if
// the year-less entry carries more metadata overall — an empty year propagates
// into resolver.Request and disables year-based candidate ranking.
func TestDeduplicateResultsKeepsYearWhenYearlessEntryIsRicher(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "x", Title: "Spider-Man", Type: media.Movie, Poster: "p.jpg", Duration: "121m", URL: "u"},
	}
	fallback := [][]media.SearchResult{{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002"},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1: %+v", len(merged), merged)
	}
	if merged[0].Year != "2002" {
		t.Errorf("Year = %q, want 2002: %+v", merged[0].Year, merged[0])
	}
	// The richer entry's metadata must not be lost either.
	if merged[0].Poster != "p.jpg" || merged[0].Duration != "121m" || merged[0].URL != "u" {
		t.Errorf("metadata lost while merging: %+v", merged[0])
	}
}
