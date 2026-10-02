package cmd

import (
	"lobster/internal/provider"
)

// providerBase returns the --base token that selects p, or "" when no base
// value selects p at all.
//
// The token is not decorative. find stamps it into every ref it emits, and
// applyRefBase (cmd/play.go) copies a ref's base straight into cfg.Base, from
// where newProvider re-derives the provider by substring match. So every
// non-empty token returned here has to round-trip — newProvider must build the
// same concrete provider from it — or the ref names a source that did not
// produce the row, which is the dishonesty this function exists to end.
// TestProviderBaseRoundTripsThroughNewProvider pins that property.
//
// "" is the honest answer for a provider newProvider cannot select, and it is
// also the safe one: applyRefBase ignores an empty base and leaves the caller's
// configuration exactly as it found it (cmd/play.go). config.BaseAuto would
// not be safe in its place — "auto" is not a neutral value but a licence to
// route movies to YTS and to open a magnet (cmd/typeroute.go, cmd/root.go), so
// stamping it on a row a scraper produced would quietly move a later play onto
// BitTorrent.
func providerBase(p provider.Provider) string {
	switch p.(type) {
	case *provider.Soap2Day:
		return "soap2day"
	case *provider.KimCartoon:
		return "kimcartoon"
	case *provider.FlixHQWS:
		return "flixhq.ws"
	case *provider.FlixHQ:
		return "flixhq"
	case *provider.TBCPL:
		return "tbcpl"
	case *provider.VidNest:
		return "vidnest"
	case *provider.VaPlayer:
		return "vaplayer"
	case *provider.YTS:
		return "yts"
	case *provider.AllAnime:
		return "allanime"
	case *provider.AnimeOnsen:
		return "animeonsen"
	case *provider.MovieBox:
		return "moviebox"
	}
	// AniPub, TBCPLEmbed and Consumet fall through deliberately. No base value
	// reaches them: they are not in newProvider's chain (Consumet is selected
	// by api_url instead), so any token would name a different provider than
	// the one that answered.
	return ""
}

// searchProviderBase is providerBase behind a package var, so a test can map
// its own stub providers onto tokens without the stub having to be one of the
// concrete provider types.
var searchProviderBase = providerBase

// fallThroughBase is the token of the provider newProvider builds when no arm
// matches the configured base. newProvider has no unknown-base arm: it ends in
// an unconditional MovieBox, so `--base sopa2day` silently searches MovieBox.
//
// It is derived from providerBase rather than written out, so the spelling
// cannot drift from the table above, and
// TestFallThroughBaseIsWhatAnUnrecognisedBaseActuallySelects pins the
// remaining half — that MovieBox is still what an unrecognised base reaches —
// against newProvider itself. A second switch mirroring newProvider's arms is
// what that test exists to make unnecessary.
var fallThroughBase = providerBase(provider.NewMovieBox())

// baseNamedThePrimary reports whether the configured base token actually
// selected the provider that answered, rather than being a value newProvider
// did not recognise.
//
// Every arm of newProvider but the last is driven by the base, so a primary
// that is not the fall-through can only have come from an arm the configured
// value satisfied: it named that provider, however it was spelled. That is
// what keeps "1shows.org" — a real TBCPL base whose spelling contains no
// "tbcpl" — a named base, and "flixhq.xx" a named FlixHQ one, without
// mirroring newProvider's aliases here.
//
// The fall-through is the one primary that cannot be read that way, because
// it is where every unrecognised value lands, and there the configured value
// has to be that exact token. Containment is not enough: "notmoviebox"
// reaches MovieBox through the same default branch "sopa2day" does, so
// matching it by substring would call a base that selected nothing an
// explicit MovieBox base — and would then stamp "notmoviebox", a source that
// does not exist, onto rows MovieBox produced.
func baseNamedThePrimary(configured, primaryBase string) bool {
	if primaryBase != fallThroughBase {
		return true
	}
	return configured == fallThroughBase
}
