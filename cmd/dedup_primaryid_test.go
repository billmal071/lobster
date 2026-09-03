package cmd

import (
	"testing"

	"lobster/internal/media"
)

// Playback resolves against the PRIMARY provider, so the merged entry has to
// keep the primary's ID. YTS is the first provider with its own ID namespace
// (yts/3024 rather than a TMDB movie/1930), and the merge was handing playback
// a TMDB ID that YTS cannot resolve — so --base yts silently fell through to
// another provider and played its dub instead.
func TestDeduplicateKeepsPrimaryProviderID(t *testing.T) {
	primary := []media.SearchResult{{
		ID: "yts/3024", Title: "The Amazing Spider-Man", Year: "2012",
		Type: media.Movie, Poster: "http://img/y.jpg",
	}}
	// A TMDB-sourced duplicate: same work, richer metadata, different namespace.
	fallback := [][]media.SearchResult{{{
		ID: "movie/1930", Title: "The Amazing Spider-Man", Year: "2012",
		Type: media.Movie, Poster: "http://img/t.jpg",
		URL:  "https://tmdb/movie/1930", Duration: "136 min",
	}}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 1 {
		t.Fatalf("expected the duplicate to merge, got %d entries", len(merged))
	}
	if merged[0].ID != "yts/3024" {
		t.Errorf("merged ID = %q, want the primary's yts/3024", merged[0].ID)
	}
	// The richer metadata should still be absorbed — this is a merge, not a
	// rejection of the fallback entry.
	if merged[0].Duration != "136 min" {
		t.Errorf("fallback metadata was dropped: %+v", merged[0])
	}
}

// A fallback-only work has no primary ID to protect and must keep its own.
func TestDeduplicateKeepsFallbackIDWhenPrimaryHasNoSuchTitle(t *testing.T) {
	merged := deduplicateResults(nil, [][]media.SearchResult{{{
		ID: "movie/999", Title: "Other Film", Year: "2001", Type: media.Movie,
	}}})
	if len(merged) != 1 || merged[0].ID != "movie/999" {
		t.Errorf("fallback-only entry lost its ID: %+v", merged)
	}
}

// Two fallback providers merging with each other keep the first one's ID,
// matching the existing provider-order preference.
func TestDeduplicateFallbackMergeKeepsFirstID(t *testing.T) {
	merged := deduplicateResults(nil, [][]media.SearchResult{
		{{ID: "a/1", Title: "Film", Year: "2001", Type: media.Movie}},
		{{ID: "b/1", Title: "Film", Year: "2001", Type: media.Movie, URL: "u", Duration: "1h"}},
	})
	if len(merged) != 1 {
		t.Fatalf("expected a merge, got %d", len(merged))
	}
	if merged[0].ID != "a/1" {
		t.Errorf("merged ID = %q, want a/1 (first provider wins)", merged[0].ID)
	}
}
