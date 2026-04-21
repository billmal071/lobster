package cmd

import (
	"strings"

	"lobster/internal/provider"
)

// newProvider returns the configured content provider.
// When cfg.APIURL is set, it returns a Consumet provider (which supports
// direct stream resolution via the StreamProvider interface).
// When base is "flixhq.ws", uses the FlixHQWS scraper.
// Otherwise, uses the default FlixHQ scraper.
func newProvider() provider.Provider {
	if cfg.APIURL != "" {
		return provider.NewConsumet(cfg.APIURL)
	}
	if strings.Contains(cfg.Base, "flixhq.ws") {
		return provider.NewFlixHQWS(cfg.Base)
	}
	return provider.NewFlixHQ(cfg.Base)
}
