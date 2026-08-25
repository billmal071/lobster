package resolver

import (
	"testing"

	"lobster/internal/media"
)

// The user picked the 2002 film, but a provider's own re-search for the bare
// title "Spider-Man" ranks the sequel that is currently in cinemas first — so
// trusting the provider's ordering played a cam/dub of the wrong movie. The
// requested ID and year must decide the ordering instead.
func TestRankCandidatesPrefersRequestedIDAndYear(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/969681", Title: "Spider-Man: Brand New Day", Type: media.Movie, Year: "2026"},
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002"},
		{ID: "movie/315635", Title: "Spider-Man: Homecoming", Type: media.Movie, Year: "2017"},
	}

	t.Run("exact ID wins", func(t *testing.T) {
		req := Request{ID: "movie/557", Title: "Spider-Man", Year: "2002", MediaType: media.Movie}
		got := candidatesFor(results, req)
		if got[0].ID != "movie/557" {
			t.Fatalf("first candidate = %s (%s), want movie/557", got[0].ID, got[0].Title)
		}
	})

	t.Run("year disambiguates when the ID is a foreign format", func(t *testing.T) {
		// A provider with its own slug IDs can't match on ID, so title+year must.
		req := Request{ID: "movie/watch-spider-man-1234", Title: "Spider-Man", Year: "2002", MediaType: media.Movie}
		got := candidatesFor(results, req)
		if got[0].ID != "movie/557" {
			t.Fatalf("first candidate = %s (%s), want movie/557", got[0].ID, got[0].Title)
		}
	})

	t.Run("exact title wins when no year is known", func(t *testing.T) {
		req := Request{Title: "Spider-Man", MediaType: media.Movie}
		got := candidatesFor(results, req)
		if got[0].ID != "movie/557" {
			t.Fatalf("first candidate = %s (%s), want movie/557", got[0].ID, got[0].Title)
		}
	})

	t.Run("all candidates are kept as fallbacks", func(t *testing.T) {
		req := Request{ID: "movie/557", Title: "Spider-Man", Year: "2002", MediaType: media.Movie}
		if got := candidatesFor(results, req); len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
	})
}

// A ±1 year drift between catalogs must not demote an otherwise exact match
// below an unrelated title.
func TestRankCandidatesToleratesYearDrift(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/2", Title: "Spider-Man: Brand New Day", Type: media.Movie, Year: "2026"},
		{ID: "movie/1", Title: "Spider-Man", Type: media.Movie, Year: "2003"},
	}
	req := Request{Title: "Spider-Man", Year: "2002", MediaType: media.Movie}
	if got := candidatesFor(results, req); got[0].ID != "movie/1" {
		t.Fatalf("first candidate = %s, want movie/1", got[0].ID)
	}
}

// Media type still filters first: a TV request never plays the film.
func TestCandidatesForKeepsMediaTypeFilter(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/557", Title: "Spider-Man", Type: media.Movie, Year: "2002"},
		{ID: "tv/888", Title: "Spider-Man", Type: media.TV, Year: "1994"},
	}
	req := Request{Title: "Spider-Man", Year: "1994", MediaType: media.TV}
	got := candidatesFor(results, req)
	if len(got) != 1 || got[0].ID != "tv/888" {
		t.Fatalf("got %+v, want only tv/888", got)
	}
}
