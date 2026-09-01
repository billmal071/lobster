package cmd

import (
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

func TestFallbackProvidersAllAnimeRetired(t *testing.T) {
	// AllAnime is retired: its API now sits behind a Cloudflare bot challenge
	// on top of the crypto-gated sources endpoint (AA_CRYPTO_MISSING, mid-2026).
	// It should never appear in the fallback chain, even when another provider
	// is primary.
	for _, p := range fallbackProviders(provider.NewFlixHQ("flixhq.to")) {
		if _, ok := p.(*provider.AllAnime); ok {
			t.Fatal("AllAnime should be retired from the fallback chain")
		}
	}

	for _, p := range fallbackProviders(provider.NewAllAnime(false)) {
		if _, ok := p.(*provider.AllAnime); ok {
			t.Fatal("AllAnime should be retired from the fallback chain")
		}
	}
}

func TestFallbackProvidersIncludeAniPub(t *testing.T) {
	found := false
	for _, p := range fallbackProviders(provider.NewFlixHQ("flixhq.to")) {
		if _, ok := p.(*provider.AniPub); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("AniPub missing from fallback chain")
	}

	for _, p := range fallbackProviders(provider.NewAniPub()) {
		if _, ok := p.(*provider.AniPub); ok {
			t.Fatal("AniPub duplicated when it is the primary")
		}
	}
}

func TestNewProviderAllAnimeBase(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg = &config.Config{Base: "allanime"}
	if p := newProvider(); func() bool { _, ok := p.(*provider.AllAnime); return !ok }() {
		t.Fatalf("newProvider with Base=allanime returned %T, want *provider.AllAnime", p)
	}
}
