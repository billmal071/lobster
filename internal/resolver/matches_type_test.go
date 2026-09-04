package resolver

import (
	"testing"

	"lobster/internal/media"
)

// Matches ignored media type entirely, so a TV ref was admitted by a
// same-titled *film*. cmd.probeSeasons then called GetSeasons on a movie ID
// and, when the provider answered with anything at all, printed it as that
// show's seasons under the ref's own title, exit 0.
//
// "Spider-Man" is the real shape of this: the 2002 film and the 1994 animated
// series share a title exactly, so every title-based test in Matches passes.
func TestMatchesRejectsACandidateOfADifferentMediaType(t *testing.T) {
	tests := []struct {
		name   string
		result media.SearchResult
		req    Request
		want   bool
	}{
		{
			name:   "a film is not the series a TV ref asked for",
			result: media.SearchResult{ID: "fb/557", Title: "Spider-Man", Type: media.Movie},
			req:    Request{ID: "tv/888", Title: "Spider-Man", MediaType: media.TV},
			want:   false,
		},
		{
			name:   "a series is not the film a movie ref asked for",
			result: media.SearchResult{ID: "fb/888", Title: "Spider-Man", Type: media.TV},
			req:    Request{ID: "movie/557", Title: "Spider-Man", MediaType: media.Movie},
			want:   false,
		},
		{
			name:   "the qualifier rules do not smuggle a film past the type gate",
			result: media.SearchResult{ID: "fb/1", Title: "The Office (US)", Type: media.Movie},
			req:    Request{ID: "tv/2", Title: "The Office", MediaType: media.TV},
			want:   false,
		},
		{
			name:   "the right type still matches on title alone",
			result: media.SearchResult{ID: "fb/888", Title: "Spider-Man", Type: media.TV},
			req:    Request{ID: "tv/888-other", Title: "Spider-Man", MediaType: media.TV},
			want:   true,
		},
		{
			name:   "a movie ref still matches a movie by title",
			result: media.SearchResult{ID: "fb/557", Title: "Spider-Man", Type: media.Movie},
			req:    Request{ID: "movie/557-other", Title: "Spider-Man", MediaType: media.Movie},
			want:   true,
		},
		{
			// Decision (a): the type check sits BELOW the identical-ID return.
			// IDs in this codebase carry their own type — "tv/1396",
			// "movie/557", "series/watch-breaking-bad-39516" — so a candidate
			// sharing a ref's ID while disagreeing about its type has
			// contradicted itself, and the ID is the half that came from the
			// catalogue rather than from a scraper's guess at a URL path.
			// Letting the label win here would reject the one conclusive
			// match there is.
			name:   "an identical ID outranks a provider's own type label",
			result: media.SearchResult{ID: "tv/1396", Title: "Breaking Bad", Type: media.Movie},
			req:    Request{ID: "tv/1396", Title: "Breaking Bad", MediaType: media.TV},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.result, tt.req); got != tt.want {
				t.Fatalf("Matches(%q %v, ref %q %v) = %v, want %v",
					tt.result.Title, tt.result.Type, tt.req.Title, tt.req.MediaType, got, tt.want)
			}
		})
	}
}

// Decision (b), pinned rather than left as a comment: media.Movie is the zero
// value of media.MediaType, so a Request that never sets MediaType is
// indistinguishable from one asking for a film, and the gate will refuse every
// TV candidate. That is a live regression risk — a narrow fix on this path has
// already made a fallback unreachable once — so the contract is asserted here
// and both production construction sites are named.
//
// The two sites: cmd/episodes.go:seasonSource sets media.TV explicitly, and
// cmd/fallback.go:tryFallbackStream copies content.Type off the selected
// search result. Neither can leave it unset. Any third caller must set it too;
// this test is what will tell them.
func TestMatchesTreatsAnUnsetRequestMediaTypeAsMovie(t *testing.T) {
	tv := media.SearchResult{ID: "fb/888", Title: "Spider-Man", Type: media.TV}
	film := media.SearchResult{ID: "fb/557", Title: "Spider-Man", Type: media.Movie}

	// MediaType deliberately omitted.
	req := Request{Title: "Spider-Man"}

	if Matches(tv, req) {
		t.Fatal("a Request with no MediaType admitted a TV candidate; the zero value is media.Movie, so callers must set MediaType and this is the contract that says so")
	}
	if !Matches(film, req) {
		t.Fatal("a Request with no MediaType must still match a movie — the zero value is media.Movie")
	}
}
