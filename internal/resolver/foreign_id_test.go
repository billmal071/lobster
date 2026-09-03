package resolver

import (
	"testing"

	"lobster/internal/media"
)

// A fallback provider indexes its own catalogue, so the ID the user's
// selection came from means nothing to it. What still identifies the work is
// the title and year — which is why the agent-facing ref carries them rather
// than an ID alone.
//
// Without Title and Year the request degenerates and a franchise query returns
// whichever sequel the provider happens to rank first.
func TestCandidatesForPicksRightWorkWhenIDsAreForeign(t *testing.T) {
	// None of these IDs match the request: this provider uses its own scheme.
	results := []media.SearchResult{
		{ID: "yts/9001", Title: "The Matrix Resurrections", Year: "2021", Type: media.Movie},
		{ID: "yts/9002", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		{ID: "yts/9003", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}

	req := Request{
		ID:        "movie/watch-the-matrix-19724", // from a different provider
		Title:     "The Matrix",
		Year:      "1999",
		MediaType: media.Movie,
	}

	got := candidatesFor(results, req)
	if len(got) == 0 {
		t.Fatal("no candidates returned")
	}
	if got[0].Title != "The Matrix" || got[0].Year != "1999" {
		t.Fatalf("top candidate = %q (%s), want The Matrix (1999); "+
			"title+year ranking is what rescues a foreign ID", got[0].Title, got[0].Year)
	}
}

// The counter-case: strip Title and Year, as a bare --id would, and the
// ranking has nothing to work with. This documents why the ref carries more
// than an ID: if this ever starts passing, the ref could be simplified.
func TestCandidatesForCannotDisambiguateWithoutTitleOrYear(t *testing.T) {
	results := []media.SearchResult{
		{ID: "yts/9001", Title: "The Matrix Resurrections", Year: "2021", Type: media.Movie},
		{ID: "yts/9003", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}

	req := Request{ID: "movie/watch-the-matrix-19724", MediaType: media.Movie}

	got := candidatesFor(results, req)
	if len(got) == 0 {
		t.Fatal("no candidates returned")
	}
	if got[0].Title == "The Matrix" && got[0].Year == "1999" {
		t.Fatal("an ID-only request picked the right film; if this is now " +
			"reliable, revisit whether playRef still needs Title and Year")
	}
}
