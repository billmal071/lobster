package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

// hermeticProviderSelection makes newProvider answer without touching the
// network: the domain probe becomes the identity function and the TBCPL
// catalog feed (which fetches a mirror list) is off.
func hermeticProviderSelection(t *testing.T, base string) {
	t.Helper()
	prevResolve := resolveDomain
	resolveDomain = func(configured, _ string, _ map[string][]string) string { return configured }
	t.Cleanup(func() { resolveDomain = prevResolve })

	prevCfg := cfg
	c := config.Default()
	c.TBCPLFeed = false
	c.Base = base
	cfg = c
	t.Cleanup(func() { cfg = prevCfg })
}

// The round trip providerBase promises: whatever token it returns for a
// provider, newProvider must build that same concrete provider back from it.
//
// This is the property the whole ref-provenance change rests on. find stamps
// the token into a ref, applyRefBase (cmd/play.go) copies a ref's base into
// cfg.Base, and newProvider re-derives a provider from it by substring match.
// If the token does not round-trip, the ref names a source that did not
// produce the row — the exact dishonesty providerBase exists to end — and it
// does so silently, because newProvider has no "unknown base" arm and falls
// through to MovieBox.
//
// Substring matching is why this cannot be checked by reading: newProvider
// tests "flixhq.ws" before "flixhq", so the tokens of those two providers are
// order-dependent, and the type name resolver.ProviderName would give
// ("FlixHQWS" -> lowercased "flixhqws") does *not* contain "flixhq.ws" and
// would select FlixHQ instead. That is why this table exists rather than a
// reuse of ProviderName.
func TestProviderBaseRoundTripsThroughNewProvider(t *testing.T) {
	for _, p := range []provider.Provider{
		provider.NewSoap2Day(),
		provider.NewKimCartoon("kimcartoon.com.co"),
		provider.NewFlixHQWS("flixhq.ws"),
		provider.NewFlixHQ("flixhq.to"),
		provider.NewTBCPL("tbcpl"),
		provider.NewVidNest(),
		provider.NewVaPlayer(),
		provider.NewYTS(),
		provider.NewAllAnime(false),
		provider.NewMovieBox(),
	} {
		want := fmt.Sprintf("%T", p)
		t.Run(want, func(t *testing.T) {
			token := providerBase(p)
			if token == "" {
				t.Fatalf("providerBase(%s) = %q; this table is for providers a base value selects", want, token)
			}
			hermeticProviderSelection(t, token)
			if got := fmt.Sprintf("%T", newProvider()); got != want {
				t.Fatalf("newProvider(base=%q) = %s, want %s; the token providerBase emits does not round-trip, so a ref stamped with it names a provider that did not produce the row", token, got, want)
			}
		})
	}
}

// The other half of the contract, and the drift guard. Every provider find can
// emit a row from is either named by a token that round-trips, or deliberately
// unnamed — and "deliberately" has to be written down here, not inferred from
// providerBase's switch falling through.
//
// A new provider added to the fallback chain therefore fails this test until
// someone decides which of the two it is. Silent fall-through would stamp ""
// on its rows, which is safe but loses the attribution for good.
func TestProviderBaseCoversEveryProviderInTheSearchChain(t *testing.T) {
	// Deliberately unnamed: no base value reaches these. AniPub and
	// TBCPLEmbed are not in newProvider's chain at all, and Consumet is
	// selected by api_url rather than by base, so any token would name a
	// different provider than the one that answered.
	unnamed := map[string]bool{
		"*provider.AniPub":     true,
		"*provider.TBCPLEmbed": true,
		"*provider.Consumet":   true,
	}

	hermeticProviderSelection(t, config.BaseAuto)
	prevFlix := flixhqDomain
	flixhqDomain = func(string, map[string][]string) string { return "flixhq.to" }
	t.Cleanup(func() { flixhqDomain = prevFlix })

	primary := newProvider()
	chain := append([]provider.Provider{primary}, fallbackSearchProviders(primary)...)
	if len(chain) < 5 {
		t.Fatalf("search chain has only %d providers (%v); the fixture is not exercising the real chain", len(chain), providerNames(chain))
	}

	for _, p := range chain {
		name := fmt.Sprintf("%T", p)
		token := providerBase(p)
		if unnamed[name] {
			if token != "" {
				t.Errorf("providerBase(%s) = %q, want \"\": no base value selects it, so a token would name another provider", name, token)
			}
			continue
		}
		if token == "" {
			t.Errorf("providerBase(%s) = \"\"; it is in the search chain, so either give it a token that round-trips or add it to this test's `unnamed` set with the reason", name)
		}
	}
}

// The drift guard for fallThroughBase. It records which provider newProvider
// builds when no arm matches, and baseNamedThePrimary reads it to tell a base
// that selected nothing from one that legitimately selected that provider. A
// second hand-written table would be the defect this file exists to prevent,
// so the value is checked against newProvider itself.
func TestFallThroughBaseIsWhatAnUnrecognisedBaseActuallySelects(t *testing.T) {
	for _, base := range []string{"sopa2day", "zzz-not-a-source"} {
		t.Run(base, func(t *testing.T) {
			hermeticProviderSelection(t, base)
			got := providerBase(newProvider())
			if got != fallThroughBase {
				t.Fatalf("newProvider(base=%q) is %q, but fallThroughBase says %q; find would then treat a base that selected nothing as one that answered", base, got, fallThroughBase)
			}
		})
	}
}
