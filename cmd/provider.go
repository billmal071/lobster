package cmd

import "lobster/internal/provider"

// newProvider returns the configured content provider.
// When cfg.APIURL is set, it returns a Consumet provider (which supports
// direct stream resolution via the StreamProvider interface).
// Otherwise, it falls back to the default FlixHQ scraper.
func newProvider() provider.Provider {
	if cfg.APIURL != "" {
		return provider.NewConsumet(cfg.APIURL)
	}
	return provider.NewFlixHQ(cfg.Base)
}
