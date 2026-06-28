package tui

import (
	"testing"

	"lobster/internal/media"
)

func TestPosterURLForItem(t *testing.T) {
	tmdbHit := func(string, string, bool) string { return "https://image.tmdb.org/t/p/w500/x.jpg" }
	tmdbMiss := func(string, string, bool) string { return "" }

	flix := media.SearchResult{Title: "Foo", Year: "2020", Type: media.TV, Poster: "https://flixhq/thumb.jpg"}
	if got := posterURLForItem(flix, tmdbHit); got != "https://image.tmdb.org/t/p/w500/x.jpg" {
		t.Fatalf("hit should upgrade to TMDB, got %q", got)
	}
	if got := posterURLForItem(flix, tmdbMiss); got != "https://flixhq/thumb.jpg" {
		t.Fatalf("miss should keep FlixHQ thumb, got %q", got)
	}
	already := media.SearchResult{Title: "Foo", Poster: "https://image.tmdb.org/t/p/w500/y.jpg"}
	called := false
	if got := posterURLForItem(already, func(string, string, bool) string { called = true; return "z" }); got != already.Poster || called {
		t.Fatalf("already-TMDB poster must be kept without a lookup, got %q called=%v", got, called)
	}
	empty := media.SearchResult{Title: "Foo"}
	if got := posterURLForItem(empty, tmdbMiss); got != "" {
		t.Fatalf("empty+miss should be empty, got %q", got)
	}
}
