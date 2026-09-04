package resolver

import (
	"testing"

	"lobster/internal/media"
)

// Matches is the gate that stops a fallback provider's *different* show being
// printed under a ref's title (cmd.probeSeasons). These cases pin both halves
// of that: what a ref must still recognise across catalogues that punctuate
// titles differently, and what it must refuse however plausible it ranks.
func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		result media.SearchResult
		req    Request
		want   bool
	}{
		// --- identity by ID ---
		{
			name:   "identical ID beats a differing title",
			result: media.SearchResult{ID: "tv/1396", Title: "Breaking Bad (2008)"},
			req:    Request{ID: "tv/1396", Title: "Breaking Bad"},
			want:   true,
		},
		{
			name:   "identical ID with a wholly unrelated title still matches",
			result: media.SearchResult{ID: "tv/1396", Title: "Bryzgun Belyi"},
			req:    Request{ID: "tv/1396", Title: "Breaking Bad"},
			want:   true,
		},
		{
			name:   "different ID falls through to the title test",
			result: media.SearchResult{ID: "fb/9", Title: "Breaking Bad"},
			req:    Request{ID: "tv/1396", Title: "Breaking Bad"},
			want:   true,
		},

		// --- titles that are the same work spelled differently ---
		{
			name:   "exact title",
			result: media.SearchResult{ID: "fb/1", Title: "Breaking Bad"},
			req:    Request{ID: "tv/1396", Title: "Breaking Bad"},
			want:   true,
		},
		{
			name:   "case and surrounding space",
			result: media.SearchResult{ID: "fb/1", Title: "  BREAKING BAD "},
			req:    Request{ID: "tv/1396", Title: "breaking bad"},
			want:   true,
		},
		{
			name:   "ampersand against the spelled-out and",
			result: media.SearchResult{ID: "fb/1", Title: "Rick and Morty"},
			req:    Request{ID: "tv/1", Title: "Rick & Morty"},
			want:   true,
		},
		{
			name:   "ampersand in the ref's direction too",
			result: media.SearchResult{ID: "fb/1", Title: "Rick & Morty"},
			req:    Request{ID: "tv/1", Title: "Rick and Morty"},
			want:   true,
		},
		{
			name:   "hyphen against a space",
			result: media.SearchResult{ID: "fb/1", Title: "Spider Man"},
			req:    Request{ID: "tv/1", Title: "Spider-Man"},
			want:   true,
		},
		{
			name:   "accented letter against its ASCII fold",
			result: media.SearchResult{ID: "fb/1", Title: "Pokemon"},
			req:    Request{ID: "tv/1", Title: "Pokémon"},
			want:   true,
		},
		{
			name:   "apostrophe and period noise",
			result: media.SearchResult{ID: "fb/1", Title: "Marvels Agents of S H I E L D"},
			req:    Request{ID: "tv/1", Title: "Marvel's Agents of S.H.I.E.L.D."},
			want:   true,
		},
		{
			name:   "ref carries a parenthetical region qualifier the provider omits",
			result: media.SearchResult{ID: "fb/1", Title: "The Office"},
			req:    Request{ID: "tv/1", Title: "The Office (US)"},
			want:   true,
		},
		{
			name:   "provider carries the parenthetical the ref omits",
			result: media.SearchResult{ID: "fb/1", Title: "The Office (US)"},
			req:    Request{ID: "tv/1", Title: "The Office"},
			want:   true,
		},
		{
			name:   "ampersand jammed against its neighbours",
			result: media.SearchResult{ID: "fb/1", Title: "Rick&Morty"},
			req:    Request{ID: "tv/1", Title: "Rick and Morty"},
			want:   true,
		},
		{
			name:   "ref carries a colon-separated season qualifier the provider omits",
			result: media.SearchResult{ID: "fb/1", Title: "Attack on Titan"},
			req:    Request{ID: "tv/1", Title: "Attack on Titan: Final Season"},
			want:   true,
		},

		// --- different works that must never be admitted ---
		{
			name:   "provider title merely begins with the ref's",
			result: media.SearchResult{ID: "fb/1", Title: "Lost in Space"},
			req:    Request{ID: "tv/1", Title: "Lost"},
			want:   false,
		},
		{
			name:   "provider title is the ref's franchise plus a subtitle",
			result: media.SearchResult{ID: "fb/1", Title: "Star Trek: Discovery"},
			req:    Request{ID: "tv/1", Title: "Star Trek"},
			want:   false,
		},
		{
			name:   "ref's subtitle names a distinct work, not a season of the provider's",
			result: media.SearchResult{ID: "fb/1", Title: "Star Trek"},
			req:    Request{ID: "tv/1", Title: "Star Trek: Discovery"},
			want:   false,
		},
		{
			name:   "ref title merely begins with the provider's, with no qualifier separator",
			result: media.SearchResult{ID: "fb/1", Title: "Lost"},
			req:    Request{ID: "tv/1", Title: "Lost in Space"},
			want:   false,
		},
		{
			name:   "sibling regional editions are not each other",
			result: media.SearchResult{ID: "fb/1", Title: "The Office (UK)"},
			req:    Request{ID: "tv/1", Title: "The Office (US)"},
			want:   false,
		},
		{
			name:   "wholly unrelated title",
			result: media.SearchResult{ID: "fb/1", Title: "Completely Different Show"},
			req:    Request{ID: "tv/1", Title: "Breaking Bad"},
			want:   false,
		},
		{
			name:   "empty ref title cannot admit anything by title",
			result: media.SearchResult{ID: "fb/1", Title: "Breaking Bad"},
			req:    Request{ID: "tv/1", Title: ""},
			want:   false,
		},
		{
			name:   "empty ref title with an empty provider title is still not a match",
			result: media.SearchResult{ID: "fb/1", Title: ""},
			req:    Request{ID: "tv/1", Title: ""},
			want:   false,
		},
		{
			name:   "empty provider title cannot match a real ref",
			result: media.SearchResult{ID: "fb/1", Title: ""},
			req:    Request{ID: "tv/1", Title: "Breaking Bad"},
			want:   false,
		},
		{
			name:   "a ref title that is only punctuation matches nothing",
			result: media.SearchResult{ID: "fb/1", Title: "Breaking Bad"},
			req:    Request{ID: "tv/1", Title: ":-"},
			want:   false,
		},
		{
			name:   "an empty ref ID does not admit an empty provider ID",
			result: media.SearchResult{ID: "", Title: "Completely Different Show"},
			req:    Request{ID: "", Title: "Breaking Bad"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.result, tt.req); got != tt.want {
				t.Fatalf("Matches(%q, ref %q) = %v, want %v", tt.result.Title, tt.req.Title, got, tt.want)
			}
		})
	}
}
