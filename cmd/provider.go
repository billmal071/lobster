package cmd

import (
	"strings"

	"lobster/internal/provider"
)

// newProvider returns the configured content provider.
// If APIURL is set, it overrides Base entirely and uses the Consumet API.
// Otherwise Base selects the scraping provider (default "flixhq.to" -> FlixHQ;
// Soap2Day, KimCartoon, etc. by their Base value); an unrecognized Base falls
// back to MovieBox (direct API, no scraping).
//
// For providers that use a base domain, the domain is checked for health
// at startup. If unreachable, known alternative domains are tried in order
// (config overrides first, then built-in fallbacks).
func newProvider() provider.Provider {
	if cfg.APIURL != "" {
		return provider.NewConsumet(cfg.APIURL)
	}

	overrides := cfg.DomainOverrides

	if strings.Contains(cfg.Base, "soap2day") {
		return provider.NewSoap2Day()
	}
	if strings.Contains(cfg.Base, "kimcartoon") {
		base := provider.ResolveDomain(cfg.Base, "kimcartoon", overrides)
		return provider.NewKimCartoon(base)
	}
	if strings.Contains(cfg.Base, "flixhq.ws") {
		base := provider.ResolveDomain(cfg.Base, "flixhqws", overrides)
		return provider.NewFlixHQWS(base)
	}
	if strings.Contains(cfg.Base, "flixhq") {
		base := provider.ResolveDomain(cfg.Base, "flixhq", overrides)
		return provider.NewFlixHQ(base)
	}
	if strings.Contains(cfg.Base, "tbcpl") || strings.Contains(cfg.Base, "1shows") {
		return provider.NewTBCPL(cfg.Base)
	}
	if strings.Contains(cfg.Base, "vidnest") {
		return provider.NewVidNest()
	}
	if strings.Contains(cfg.Base, "vaplayer") {
		return provider.NewVaPlayer()
	}
	// Default: MovieBox
	return provider.NewMovieBox()
}
