package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"lobster/internal/media"
)

// playRef is everything needed to replay a selection the user confirmed.
//
// An ID alone is not enough. media.SearchResult.ID is provider-specific
// (internal/media/types.go), and the resolver re-searches by title on every
// fallback provider — resolveWithProvider calls p.Search(req.Title), and its
// doc comment states that IDs are not portable across providers. Without Title
// and Year the provider is asked to Search("") and ranking collapses, which
// does not fail loudly: it plays the wrong film.
//
// Base records the base that was *configured* when the ref was produced, not
// necessarily the provider that supplied the ID. It is carried because the
// primary provider is flag/config-selected, and an ID found under --base yts
// is meaningless under the default base — so replaying the same base is a much
// better starting point than whatever happens to be configured later.
//
// It is a hint, not a guarantee. find searches the primary provider *and* the
// fallback chain (gatherSearchResults), and stamps cfg.Base on every result it
// prints, so a result that actually came from a fallback provider carries the
// primary's base. Making it exact would not help. A base is a config-time
// choice of *primary* provider, not a per-row attribution, and by the time
// find prints a row that row may not come from one provider at all:
// deduplicateResults (cmd/multisearch.go) merges duplicates across providers,
// keeping the first arrival's ID while filling its metadata gaps from the
// others. There is no single honest base to stamp on the merged row.
//
// So nothing downstream may assume the ID resolves against the base, and
// nothing does. play re-searches by title through the whole chain
// (resolveAndPlay, cmd/search.go) and episodes does the same via seasonSource
// (cmd/episodes.go) when the base's provider cannot enumerate the ID. The base
// only decides where that search starts.
//
// A live ref's identity story is different in kind, not degree. It is never
// re-searched by title: play re-matches it against freshly loaded playlists
// on every play, by TVGID when present (narrowed further by exact folded
// Title if that tvg-id turns out to be shared by more than one channel — real
// playlists are not always disciplined about tvg-id uniqueness) and otherwise
// by exact folded Title, narrowed to Source when set, and it fails closed on
// both absence and ambiguity rather than falling through to a search. Base is
// meaningless here (there is no provider chain to start a search on) and is
// left empty.
type playRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Year   string `json:"year,omitempty"`
	Type   string `json:"type"`
	Base   string `json:"base,omitempty"`
	TVGID  string `json:"tvg_id,omitempty"` // live only: stable upstream id, "" when the playlist omits it
	Source string `json:"source,omitempty"` // live only: the playlist the channel was loaded from
}

// liveRefType is the playRef.Type of a live channel. Lowercase and exact:
// a ref is machine-produced and opaque, so any other spelling is corruption.
const liveRefType = "live"

// encodeRef renders a ref as a base64url token. Opaque by contract, but plain
// base64 so it can be decoded by hand during support.
func encodeRef(r playRef) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding ref: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeRef parses a token produced by encodeRef.
func decodeRef(s string) (playRef, error) {
	if s == "" {
		return playRef{}, fmt.Errorf("empty ref")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return playRef{}, fmt.Errorf("ref is not valid base64url: %w", err)
	}
	var r playRef
	if err := json.Unmarshal(b, &r); err != nil {
		return playRef{}, fmt.Errorf("ref is not valid JSON: %w", err)
	}
	if r.ID == "" || r.Title == "" {
		return playRef{}, fmt.Errorf("ref is missing id or title")
	}
	// Type decides whether play requires --season/--episode, and searchResult
	// maps everything that is not "tv" or "live" onto media.Movie. So an empty
	// or misspelled type does not fail: a series reads as a film, the
	// season/episode gate in playRun does not fire, and resolveAndPlay is
	// entered with season 0 — the interactive-picker path these commands exist
	// to avoid. Only the two canonical MediaType.String() values and
	// liveRefType are accepted, and exactly as encodeRef writes them: a ref is
	// machine-produced and opaque, so "TV" is a corrupted token, not a human
	// typing.
	if r.Type != media.Movie.String() && r.Type != media.TV.String() && r.Type != liveRefType {
		return playRef{}, fmt.Errorf("ref has unknown type %q (want %q, %q or %q)",
			r.Type, media.Movie.String(), media.TV.String(), liveRefType)
	}
	return r, nil
}

// searchResult converts a ref back into the value the playback path expects.
//
// It refuses a live ref. media.MediaType has no Live, so a live channel could
// only be converted by mislabelling it Movie — and resolveAndPlay re-searches
// by title on every fallback provider, so "BBC One" would be handed to FlixHQ
// as a title query and could play a documentary instead. play's live branch
// runs before this function, but the refusal is what makes that property
// independent of branch ordering.
func (r playRef) searchResult() (media.SearchResult, error) {
	if r.Type == liveRefType {
		return media.SearchResult{}, fmt.Errorf("live ref %q cannot be converted to a search result", r.Title)
	}
	t := media.Movie
	if r.Type == media.TV.String() {
		t = media.TV
	}
	return media.SearchResult{ID: r.ID, Title: r.Title, Year: r.Year, Type: t}, nil
}
