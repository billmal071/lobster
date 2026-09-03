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
// Base is carried because the primary provider is flag/config-selected, and an
// ID found under --base yts is meaningless under the default base.
type playRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Year  string `json:"year,omitempty"`
	Type  string `json:"type"`
	Base  string `json:"base,omitempty"`
}

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
	return r, nil
}

// searchResult converts a ref back into the value the playback path expects.
func (r playRef) searchResult() media.SearchResult {
	t := media.Movie
	if r.Type == "tv" {
		t = media.TV
	}
	return media.SearchResult{
		ID:    r.ID,
		Title: r.Title,
		Year:  r.Year,
		Type:  t,
	}
}
