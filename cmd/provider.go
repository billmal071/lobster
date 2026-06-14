package cmd

import (
	"strings"

	"lobster/internal/provider"
)

// newProvider returns the configured content provider.
// Default is TBCPL. Legacy/category providers are available via base config
// and selected stream-capable providers are included as fallbacks.
func newProvider() provider.Provider {
	if cfg.APIURL != "" {
		return provider.NewConsumet(cfg.APIURL)
	}

	if strings.Contains(cfg.Base, "aniwatch") {
		return provider.NewAniWatch(cfg.Base)
	}
	if strings.Contains(cfg.Base, "soap2day") {
		return provider.NewSoap2Day()
	}
	if strings.Contains(cfg.Base, "kimcartoon") {
		base := provider.ResolveDomain(cfg.Base, "kimcartoon", cfg.DomainOverrides)
		return provider.NewKimCartoon(base)
	}
	if strings.Contains(cfg.Base, "flixhq.ws") {
		base := provider.ResolveDomain(cfg.Base, "flixhqws", cfg.DomainOverrides)
		return provider.NewFlixHQWS(base)
	}
	if strings.Contains(cfg.Base, "flixhq") {
		base := provider.ResolveDomain(cfg.Base, "flixhq", cfg.DomainOverrides)
		return provider.NewFlixHQ(base)
	}
	if strings.Contains(cfg.Base, "tbcpl") || strings.Contains(cfg.Base, "1shows") {
		return provider.NewTBCPL(cfg.Base)
	}
	// Default: TBCPL (TMDB-backed search, Vidzee streaming)
	return provider.NewTBCPL("tbcpl")
}
