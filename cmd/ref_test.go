package cmd

import (
	"testing"

	"lobster/internal/media"
)

// A ref must survive the round trip intact. It is the only handle the agent
// has on a selection the user confirmed, so any field loss means playing
// something other than what was agreed.
func TestRefRoundTrip(t *testing.T) {
	want := playRef{
		ID:    "movie/watch-the-matrix-19724",
		Title: "The Matrix",
		Year:  "1999",
		Type:  "movie",
		Base:  "flixhq.ws",
	}
	tok, err := encodeRef(want)
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef: %v", err)
	}
	if got != want {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, want)
	}
}

// A hand-mangled ref must produce a clean error, not a panic.
func TestDecodeRefRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "!!!not-base64!!!", "YWJj"} { // "abc" is valid b64, invalid JSON
		if _, err := decodeRef(in); err == nil {
			t.Fatalf("decodeRef(%q) succeeded, want an error", in)
		}
	}
}

// Year is a string throughout media.SearchResult and is frequently empty;
// the ref must preserve that rather than inventing a zero year.
func TestRefPreservesEmptyYear(t *testing.T) {
	tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: "tv"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef: %v", err)
	}
	if got.Year != "" {
		t.Fatalf("Year = %q, want empty", got.Year)
	}
}

func TestRefSearchResultMapsType(t *testing.T) {
	if got := (playRef{Type: "tv"}).searchResult().Type; got != media.TV {
		t.Fatalf("tv mapped to %v, want media.TV", got)
	}
	if got := (playRef{Type: "movie"}).searchResult().Type; got != media.Movie {
		t.Fatalf("movie mapped to %v, want media.Movie", got)
	}
}
