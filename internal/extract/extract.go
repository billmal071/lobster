// Package extract resolves embed URLs into playable stream URLs
// by communicating with MegaCloud/VidCloud or Byse endpoints.
package extract

import (
	"strings"

	"lobster/internal/media"
)

// Extractor resolves embed URLs into playable streams.
type Extractor interface {
	Extract(embedURL string, preferredQuality string) (*media.Stream, error)
}

// New returns the appropriate extractor for the given embed URL.
func New() Extractor {
	return NewMegaCloud()
}

// ResolveForURL follows vidcdn.co redirects and returns the appropriate
// extractor and resolved embed URL for the actual backend.
// The referer is the origin domain (e.g. "https://flixhq.ws/") that the
// embed page expects as its Referer header; pass "" to use the extractor's
// built-in default.
func ResolveForURL(embedURL, referer string) (Extractor, string) {
	target := embedURL

	// Resolve vidcdn.co redirect to find the actual backend
	if strings.Contains(embedURL, "vidcdn.co") {
		if resolved, err := followRedirectWithReferer(embedURL, referer); err == nil {
			target = resolved
		}
	}

	switch {
	case strings.Contains(target, "megaplay.buzz") || strings.Contains(target, "mewstream"):
		return NewMegaPlay(), target
	case strings.Contains(target, "weneverbeenfree.com"):
		e := NewByse()
		if referer != "" {
			e.Referer = referer
		}
		return e, target
	case strings.Contains(target, "strcdn.org") || strings.Contains(target, "netu"):
		e := NewNetu()
		if referer != "" {
			e.Referer = referer
		}
		return e, target
	case strings.Contains(target, "vidwish.live"):
		return NewVidWish(), target
	default:
		e := NewMegaCloud()
		if referer != "" {
			e.Referer = referer
		}
		return e, target
	}
}
