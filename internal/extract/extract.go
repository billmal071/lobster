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

// NewForURL returns the appropriate extractor based on the embed URL.
func NewForURL(embedURL string) Extractor {
	if strings.Contains(embedURL, "vidcdn.co") || strings.Contains(embedURL, "weneverbeenfree.com") {
		return NewByse()
	}
	return NewMegaCloud()
}
