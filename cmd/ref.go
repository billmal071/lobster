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
// Base names the provider that supplied the ID, as the base token that selects
// it (cmd/providerbase.go), or is empty when no base value selects that
// provider at all. It is carried because the primary provider is
// flag/config-selected and IDs are not portable between providers — an ID
// found on YTS is meaningless to MovieBox — so replaying the source that
// produced the ID is a far better starting point than whatever happens to be
// configured later.
//
// It is per-row, not per-run. find searches the primary provider *and* the
// fallback chain (gatherSearchResults, cmd/multisearch.go), so one response
// can carry rows from several providers; each row is stamped with its own
// producer (refBaseFor, cmd/find.go). Merged rows are attributed to whichever
// entry's ID survived deduplication, because it is the ID that Base explains:
// deduplicateResults pins the first arrival's ID and pins its attribution
// alongside it.
//
// An empty Base is the honest answer for a provider no base value reaches
// (AniPub, TBCPLEmbed, Consumet), and applyRefBase (cmd/play.go) treats it as
// "leave the configuration alone". config.BaseAuto is never stamped in its
// place: "auto" names no provider and instead licenses routing movies to YTS
// and opening a magnet (cmd/typeroute.go, cmd/root.go).
//
// Base still only decides where resolution *starts*; nothing downstream
// assumes the ID resolves against it. play re-searches by title through the
// whole chain (resolveAndPlay, cmd/search.go) and episodes does the same via
// seasonSource (cmd/episodes.go) when the base's provider cannot enumerate the
// ID. Refs minted by older versions carry the configured base instead, and are
// decoded and applied exactly as before — the change is in what find writes,
// not in how a ref is read.
//
// A live ref's identity story is different in kind, not degree. It is never
// re-searched by title: play re-matches it against freshly loaded playlists
// on every play, by TVGID when present; if that tvg-id is shared by more than
// one channel (real playlists are not always disciplined about tvg-id
// uniqueness), the result is narrowed further by exact folded Title; when no
// tvg-id is present at all, matching falls back to exact folded Title
// directly. Source narrows the match throughout. It fails closed if the
// result is zero or still more than one channel, rather than falling through
// to a search. Base is meaningless here (there is no provider chain to start
// a search on) and is left empty.
//
// Source and SrcKey are two views of one playlist and are not
// interchangeable. Source is sanitized for a human to read and is what error
// messages print; it deliberately discards the whole query string, which is
// where an Xtream source keeps its credentials — so two subscriptions on the
// same server collapse to one Source. SrcKey is the value matching compares:
// a digest of the raw source, so those two subscriptions stay distinct, while
// the digest itself discloses nothing. See sourceKey (cmd/channels.go).
type playRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Year   string `json:"year,omitempty"`
	Type   string `json:"type"`
	Base   string `json:"base,omitempty"`
	TVGID  string `json:"tvg_id,omitempty"`  // live only: stable upstream id, "" when the playlist omits it
	Source string `json:"source,omitempty"`  // live only: the playlist the channel was loaded from, sanitized for display
	SrcKey string `json:"src_key,omitempty"` // live only: collision-safe digest of the raw playlist source; the value matching compares
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
