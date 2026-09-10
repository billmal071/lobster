package cmd

import (
	"strings"

	"lobster/internal/config"
	"lobster/internal/provider"
	"lobster/internal/tbcpl"
)

// autoBase is the general-purpose source config.BaseAuto maps to: the primary
// used to search, and — for a series — to enumerate seasons and episodes.
//
// Soap2Day, on measurements taken 2026-09-09. Search for "the matrix" returned
// 20 results, and `episodes --season 1` for Marvel's Agents of S.H.I.E.L.D.
// returned the true 22. Of the alternatives measured the same day: flixhq.ws
// (the previous default) answered 404, flixhq.to was dead, MovieBox and VidNest
// answered 10 and 50 placeholder episodes against that true 22, 1shows.org was
// correct once and HTTP 451 minutes later, and YTS has no TV catalogue. VaPlayer
// and tbcpl also returned the correct 22 and are equally defensible picks;
// Soap2Day is chosen because it is a StreamProvider, so the primary can resolve
// a stream itself rather than always deferring to the fallback chain.
const autoBase = "soap2day"

// newProvider returns the configured content provider.
// If APIURL is set, it overrides Base entirely and uses the Consumet API.
// Otherwise Base selects the scraping provider (config.BaseAuto -> autoBase;
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

	base := cfg.Base
	if base == config.BaseAuto {
		base = autoBase
	}

	overrides := cfg.DomainOverrides
	if cat := tbcplCatalog(); cat != nil {
		overrides = provider.MergeOverrides(cfg.DomainOverrides, tbcpl.MirrorDomains(cat.EligibleSites(cfg.TBCPLIncludeUntrusted)))
	}

	if strings.Contains(base, "soap2day") {
		return provider.NewSoap2Day()
	}
	if strings.Contains(base, "kimcartoon") {
		domain := provider.ResolveDomain(base, "kimcartoon", overrides)
		return provider.NewKimCartoon(domain)
	}
	if strings.Contains(base, "flixhq.ws") {
		domain := provider.ResolveDomain(base, "flixhqws", overrides)
		return provider.NewFlixHQWS(domain)
	}
	if strings.Contains(base, "flixhq") {
		domain := provider.ResolveDomain(base, "flixhq", overrides)
		return provider.NewFlixHQ(domain)
	}
	if strings.Contains(base, "tbcpl") || strings.Contains(base, "1shows") {
		tb := provider.NewTBCPL(base)
		tb.SetAudioLanguage(cfg.AudioLanguage)
		return tb
	}
	if strings.Contains(base, "vidnest") {
		return provider.NewVidNest()
	}
	if strings.Contains(base, "vaplayer") {
		return provider.NewVaPlayer()
	}
	// config.IsYTSBase, shared with mayStreamTorrent (cmd/root.go), so the
	// reader that decides "this run may open a magnet" and the reader that
	// actually builds the magnet source can never disagree about a spelling.
	if config.IsYTSBase(base) {
		return provider.NewYTS()
	}
	if strings.Contains(base, "allanime") {
		return provider.NewAllAnime(cfg.AnimeDub)
	}
	// Default: MovieBox
	return provider.NewMovieBox()
}
