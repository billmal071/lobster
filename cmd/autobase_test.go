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

// Base has three readers that do not compare it the same way — mayStreamTorrent
// case-insensitively (cmd/root.go), baseIsAuto exactly (cmd/typeroute.go), and
// newProvider by substring (cmd/provider.go) — so an un-normalised value can be
// auto for one and unrecognised for the others. `base = "AUTO"` did exactly
// that: it re-execed for a torrent that would never be reached, skipped the
// per-type route, and fell through newProvider's chain to MovieBox, the
// provider that answers a 22-episode season with 10 fabricated rows.
//
// config.Validate is the single place both of those inputs pass through (the
// file at Load, the flag at applyConfig's re-validation), so normalising there
// is what makes the three agree. The third input, a ref's stamped base, does
// not pass through Validate and normalises in applyRefBase instead; see
// TestARefSuppliedBaseIsCanonicalForEveryReaderOfBase. This asserts the agreement rather than any one
// reader: a fixture that only asked mayStreamTorrent could not have seen the
// bug, which is how it shipped.
func TestALoudlySpelledAutoIsAutoForEveryReaderOfBase(t *testing.T) {
	withOutputFlags(t, false, "")
	prev := cfg
	t.Cleanup(func() { cfg = prev })

	c := config.Default()
	// The catalog feed behind newProvider's domain overrides fetches over the
	// network; nothing here needs it.
	c.TBCPLFeed = false
	c.Base = "  AUTO  "
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	cfg = c

	if !mayStreamTorrent(c) {
		t.Errorf("mayStreamTorrent(base=%q) = false; under auto every movie is routed to YTS, so the run may open a magnet", c.Base)
	}
	if !baseIsAuto() {
		t.Errorf("baseIsAuto(base=%q) = false; a loudly spelled auto is still no preference, so movies must route to YTS", c.Base)
	}
	if p := newProvider(); func() bool { _, ok := p.(*provider.Soap2Day); return !ok }() {
		t.Errorf("newProvider(base=%q) = %T, want *provider.Soap2Day; the fall-through is MovieBox, which fabricates episode lists", c.Base, p)
	}
}

// newProvider matches by substring and has no "unknown base" arm, so a typo
// does not fail — it falls through to MovieBox, which reports a 22-episode
// season as 10 fabricated rows. GUIDE.md's "Content sources" table warns about
// exactly this, so pin the behaviour the warning describes.
func TestAnUnrecognisedBaseFallsThroughToMovieBox(t *testing.T) {
	// Chosen not to contain any recognised name as a substring: "soap2days"
	// and "flixhq.xx" would both still match, because the test is Contains,
	// not equality.
	for _, base := range []string{"sopa2day", "flixq.to", "nonesuch"} {
		withBase(t, base)
		if _, ok := newProvider().(*provider.MovieBox); !ok {
			t.Fatalf("newProvider(base=%q) = %T, want *provider.MovieBox", base, newProvider())
		}
	}
}

// Two readers decide "is this base YTS": newProvider, which builds the
// provider that resolves magnets, and mayStreamTorrent, which decides whether
// the run re-execs onto the storage backend that cannot SIGBUS. They used
// different tests — substring against equality — so `base = "yts.mx"`, which
// GUIDE.md documents as valid under the source table ("Values are matched by
// substring, so anything still containing a known name works"), got a real YTS
// primary while mayStreamTorrent answered false: the mmap backend for a
// magnet, and applyRefBase's late-ref warning silenced too.
//
// config.IsYTSBase is now the single predicate both call, and this asserts the
// agreement rather than either reader on its own — a fixture that only asked
// mayStreamTorrent is what let the disagreement ship. The non-YTS rows are
// every other base GUIDE.md's table documents, which is what makes
// "substring" safe: none of them contains "yts", so nothing newProvider
// matches ahead of YTS can be misread as one.
func TestEveryReaderOfBaseAgreesAboutYTS(t *testing.T) {
	withOutputFlags(t, false, "")
	for _, c := range []struct {
		base    string
		wantYTS bool
		// newProvider resolves a domain over the network for the
		// domain-checked providers (provider.ResolveDomain, called
		// unstubbed from cmd/provider.go), so those rows assert the two
		// predicates only. Building one here made this test take 9.6s and
		// probe flixhq.to and kimcartoon for real.
		probesNetwork bool
	}{
		{base: "yts", wantYTS: true},
		{base: "YTS", wantYTS: true},
		{base: "  yts  ", wantYTS: true},
		{base: "yts.mx", wantYTS: true},
		{base: "  YTS.MX  ", wantYTS: true},
		{base: "soap2day"},
		{base: "vaplayer"},
		{base: "flixhq.to", probesNetwork: true},
		{base: "flixhq.ws", probesNetwork: true},
		{base: "tbcpl"},
		{base: "1shows.org"},
		{base: "kimcartoon", probesNetwork: true},
		{base: "allanime"},
		{base: "moviebox"},
		{base: "vidnest"},
		// Not a base anyone types; the point is that a value merely reading
		// like a typo of a YTS domain is still treated as YTS by both, rather
		// than by one of them.
		{base: "yts.lt", wantYTS: true},
	} {
		t.Run(c.base, func(t *testing.T) {
			if got := config.IsYTSBase(c.base); got != c.wantYTS {
				t.Errorf("config.IsYTSBase(%q) = %v, want %v", c.base, got, c.wantYTS)
			}
			// What a real run holds by the time either reader looks: Validate
			// canonicalises Base once, at load (internal/config/config.go).
			normalized := config.NormalizeBase(c.base)
			withBase(t, normalized)

			if !c.probesNetwork {
				p := newProvider()
				_, isYTS := p.(*provider.YTS)
				if isYTS != c.wantYTS {
					t.Errorf("newProvider(base=%q) = %T; YTS primary = %v, want %v", normalized, p, isYTS, c.wantYTS)
				}
			}
			// cfg carries no torrent_fallback and no api_url, and neither
			// --json nor --download is set (withOutputFlags), so the base arm
			// is the only one that can answer — and "auto" is deliberately not
			// in the table above, because it answers true for a reason that
			// has nothing to do with the base naming YTS.
			if got := mayStreamTorrent(cfg); got != c.wantYTS {
				t.Errorf("mayStreamTorrent(base=%q) = %v, want %v; it must agree with newProvider about whether this run can open a magnet", normalized, got, c.wantYTS)
			}
		})
	}
}
