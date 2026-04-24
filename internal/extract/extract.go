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
func ResolveForURL(embedURL string) (Extractor, string) {
	target := embedURL

	// Resolve vidcdn.co redirect to find the actual backend
	if strings.Contains(embedURL, "vidcdn.co") {
		if resolved, err := followRedirect(embedURL); err == nil {
			target = resolved
		}
	}

	switch {
	case strings.Contains(target, "weneverbeenfree.com"):
		return NewByse(), target
	case strings.Contains(target, "strcdn.org") || strings.Contains(target, "netu"):
		return NewNetu(), target
	default:
		return NewMegaCloud(), target
	}
}
