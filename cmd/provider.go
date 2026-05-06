package cmd

import (
	"strings"

	"lobster/internal/provider"
)

// newProvider returns the configured content provider.
// Default is MovieBox (direct API, no scraping required).
// Legacy providers (FlixHQ, Soap2Day) are available via base config
// and are always included as fallbacks.
func newProvider() provider.Provider {
	if cfg.APIURL != "" {
		return provider.NewConsumet(cfg.APIURL)
	}
	if strings.Contains(cfg.Base, "soap2day") {
		return provider.NewSoap2Day()
	}
	if strings.Contains(cfg.Base, "kimcartoon") {
		return provider.NewKimCartoon(cfg.Base)
	}
	if strings.Contains(cfg.Base, "flixhq.ws") {
		return provider.NewFlixHQWS(cfg.Base)
	}
	if strings.Contains(cfg.Base, "flixhq") {
		return provider.NewFlixHQ(cfg.Base)
	}
	// Default: MovieBox
	return provider.NewMovieBox()
}
