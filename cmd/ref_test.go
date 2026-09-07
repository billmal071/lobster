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
	got, err := (playRef{Type: "tv"}).searchResult()
	if err != nil {
		t.Fatalf("tv: %v", err)
	}
	if got.Type != media.TV {
		t.Fatalf("tv mapped to %v, want media.TV", got.Type)
	}
	got, err = (playRef{Type: "movie"}).searchResult()
	if err != nil {
		t.Fatalf("movie: %v", err)
	}
	if got.Type != media.Movie {
		t.Fatalf("movie mapped to %v, want media.Movie", got.Type)
	}
}

func TestDecodeRefAcceptsLive(t *testing.T) {
	tok, err := encodeRef(playRef{
		ID: "bbc1.uk", Title: "BBC One", Type: liveRefType,
		TVGID: "bbc1.uk", Source: "https://example.invalid/uk.m3u",
	})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	got, err := decodeRef(tok)
	if err != nil {
		t.Fatalf("decodeRef rejected a live ref: %v", err)
	}
	if got.Type != liveRefType || got.TVGID != "bbc1.uk" ||
		got.Source != "https://example.invalid/uk.m3u" {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}

func TestSearchResultRefusesLive(t *testing.T) {
	// The structural guard. play's live branch runs before this converter,
	// but the safety property must not depend on branch ordering: a future
	// edit that reorders playRun would otherwise silently hand a live ref to
	// the title-search path, where "BBC One" can resolve to a documentary.
	_, err := playRef{ID: "bbc1.uk", Title: "BBC One", Type: liveRefType}.searchResult()
	if err == nil {
		t.Fatal("searchResult must refuse a live ref")
	}
}

func TestSearchResultStillConvertsMovieAndTV(t *testing.T) {
	got, err := playRef{ID: "x", Title: "Parasite", Year: "2019", Type: media.Movie.String()}.searchResult()
	if err != nil {
		t.Fatalf("movie ref: %v", err)
	}
	if got.Type != media.Movie || got.Title != "Parasite" || got.Year != "2019" {
		t.Fatalf("movie conversion = %+v", got)
	}
	got, err = playRef{ID: "y", Title: "Severance", Type: media.TV.String()}.searchResult()
	if err != nil {
		t.Fatalf("tv ref: %v", err)
	}
	if got.Type != media.TV {
		t.Fatalf("tv conversion = %+v", got)
	}
}

func TestDecodeRefStillRejectsUnknownTypes(t *testing.T) {
	tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: "Live"}) // wrong case
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	if _, err := decodeRef(tok); err == nil {
		t.Fatal("decodeRef must reject \"Live\": a ref is machine-produced, so case is corruption")
	}
}

// A ref whose Type is empty or unrecognised must be rejected at decode time.
//
// searchResult() maps everything that is not "tv" onto media.Movie, so a
// truncated or hand-edited ref for a series arrives at playRun looking like a
// film: the "--season and --episode are required" gate does not fire, and the
// selection is handed to resolveAndPlay with season 0, which is precisely the
// path that falls through to the interactive picker this whole command set
// exists to avoid. Failing loudly on the token is the only place the mistake
// is still cheap.
func TestDecodeRefRejectsUnknownType(t *testing.T) {
	for _, typ := range []string{"", "series", "TV", "Movie", "film"} {
		tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: typ})
		if err != nil {
			t.Fatalf("encodeRef(%q): %v", typ, err)
		}
		if _, err := decodeRef(tok); err == nil {
			t.Errorf("decodeRef accepted type %q, want an error", typ)
		}
	}
	// The two canonical values must still round-trip.
	for _, typ := range []string{media.Movie.String(), media.TV.String()} {
		tok, err := encodeRef(playRef{ID: "x", Title: "T", Type: typ})
		if err != nil {
			t.Fatalf("encodeRef(%q): %v", typ, err)
		}
		if _, err := decodeRef(tok); err != nil {
			t.Errorf("decodeRef rejected canonical type %q: %v", typ, err)
		}
	}
}
