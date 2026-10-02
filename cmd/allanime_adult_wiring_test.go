package cmd

import (
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

// The config setting actually reaches the provider.
//
// It is a setter rather than a constructor argument, which means a call site
// can simply forget it — and the symptom would be a documented setting that
// silently does nothing, which is the failure this repo keeps re-learning.
// newProvider is the CLI's call site; internal/tui has its own
// (newTUIAllAnime) with the same reason written down there.
func TestNewProviderCarriesTheAdultAnimeSettingIntoAllAnime(t *testing.T) {
	for _, want := range []bool{false, true} {
		hermeticProviderSelection(t, "allanime")
		cfg.AllowAdultAnime = want

		p := newProvider()
		aa, ok := p.(*provider.AllAnime)
		if !ok {
			t.Fatalf("newProvider(base=allanime) = %T, want *provider.AllAnime", p)
		}
		if got := aa.AllowAdult(); got != want {
			t.Fatalf("allow_adult_anime = %v, but the provider has AllowAdult() = %v; the setting does nothing", want, got)
		}
	}
}

// The default is off, from the shipped defaults rather than from a test's own
// zero value.
func TestAllowAdultAnimeDefaultsOff(t *testing.T) {
	if config.Default().AllowAdultAnime {
		t.Fatal("config.Default() lifts AllAnime's adult filter; it must be opt-in")
	}
}
