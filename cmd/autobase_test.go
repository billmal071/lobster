package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

// withBase installs a cfg whose Base is b for the duration of one test.
func withBase(t *testing.T, b string) {
	t.Helper()
	prev := cfg
	cfg = &config.Config{Base: b, Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })
}

// "auto" is the default Base, so whatever newProvider returns for it is the
// primary every plain `lobster <query>` uses to search and, for a series, to
// enumerate seasons. Falling through newProvider's chain to MovieBox is not
// good enough: measured 2026-09-09, MovieBox answered `episodes --season 1`
// for Marvel's Agents of S.H.I.E.L.D. with 10 fabricated placeholder episodes
// against a true 22, while Soap2Day returned the correct 22 and 20 search
// results for "the matrix".
func TestNewProviderMapsAutoBaseToALiveGeneralSource(t *testing.T) {
	withBase(t, "auto")

	p := newProvider()
	if _, ok := p.(*provider.Soap2Day); !ok {
		t.Fatalf("newProvider() under base=auto = %T, want *provider.Soap2Day", p)
	}
}

// An explicit base is still honoured verbatim — "auto" adds a case, it does
// not replace the mapping.
func TestNewProviderStillHonoursAnExplicitBase(t *testing.T) {
	for base, want := range map[string]string{
		"yts":        "*provider.YTS",
		"vaplayer":   "*provider.VaPlayer",
		"moviebox":   "*provider.MovieBox",
		"flixhq.ws":  "*provider.FlixHQWS",
		"soap2day":   "*provider.Soap2Day",
		"1shows.org": "*provider.TBCPL",
	} {
		withBase(t, base)
		if got := fmt.Sprintf("%T", newProvider()); got != want {
			t.Fatalf("newProvider() under base=%q = %s, want %s", base, got, want)
		}
	}
}
