// Package extract resolves embed URLs into playable stream URLs
// by communicating directly with MegaCloud/VidCloud endpoints.
package extract

import "lobster/internal/media"

// Extractor resolves embed URLs into playable streams.
type Extractor interface {
	Extract(embedURL string, preferredQuality string) (*media.Stream, error)
}

// New returns the appropriate extractor for the given embed URL.
func New() Extractor {
	return NewMegaCloud()
}
