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
// VaPlayer since 2026-11-07 (Soap2Day before, now retired): a StreamProvider
// that enumerates seasons truthfully. As the primary it is excluded from its
// own fallback chain, which therefore starts at TBCPL (cmd/fallback.go).
const autoBase = "vaplayer"

// resolveDomain is provider.ResolveDomain behind a package var. It probes the
// configured domain over the network and falls over to a known mirror, which
// is right in production and wrong in a test: a hermetic test that only asks
// which *provider type* a base selects would otherwise spend a probe timeout
// per domain-checked provider. Stub it to return the configured value.
var resolveDomain = provider.ResolveDomain

// newProvider returns the configured content provider.
// If APIURL is set, it overrides Base entirely and uses the Consumet API.
// Otherwise Base selects the scraping provider (config.BaseAuto -> autoBase;
// FlixHQWS, TBCPL, etc. by their Base value); an unrecognized Base falls
// back to VaPlayer, the same source autoBase names.
//
// A base naming a retired source never reaches here: config.Load downgrades
// the file's value (to vaplayer, or auto under torrent_fallback), applyConfig
// rejects the flag, and applyRefBase
// (cmd/play.go) ignores a ref's stamped base with a warning (config.RetiredBase).
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

	if config.IsFlixHQWSBase(base) {
		domain := resolveDomain(base, "flixhqws", overrides)
		return provider.NewFlixHQWS(domain)
	}
	if strings.Contains(base, "tbcpl") || strings.Contains(base, "1shows") {
		tb := provider.NewTBCPL(base)
		tb.SetAudioLanguage(cfg.AudioLanguage)
		return tb
	}
	// Torrent sources: config.IsTorrentBase (mayStreamTorrent, cmd/root.go) must recognise every spelling matched here.
	if config.IsYTSBase(base) {
		return provider.NewYTS()
	}
	if strings.Contains(base, "nyaa") {
		return provider.NewNyaa()
	}
	if strings.Contains(base, "eztv") {
		return provider.NewEZTV()
	}
	if strings.Contains(base, "1337x") {
		return provider.NewX1337()
	}
	// These are substring tests, so an arm that matched a longer token first
	// would shadow a later one; none of the tokens above contain each other
	// or "animeonsen", so the order among them is not load-bearing today.
	if strings.Contains(base, "animeonsen") {
		return provider.NewAnimeOnsen()
	}
	// Default: VaPlayer. This arm doubles as the explicit `--base vaplayer`
	// arm; fallThroughBase (cmd/providerbase.go) is derived from it.
	return provider.NewVaPlayer()
}
