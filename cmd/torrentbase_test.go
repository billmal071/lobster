package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

// Nyaa and EZTV are the two magnet sources that joined YTS. Each has to be
// reachable by name, round-trip through providerBase (so a ref stamped
// "nyaa" or "eztv" replays against the source that produced the row), and
// be recognised by mayStreamTorrent — a run whose primary can only answer
// with a magnet must land on the storage backend that cannot SIGBUS, exactly
// as `--base yts` does.
func TestTorrentBasesBuildTheirProviderAndRoundTrip(t *testing.T) {
	withOutputFlags(t, false, "")
	for _, c := range []struct {
		base string
		want string
	}{
		{"nyaa", "*provider.Nyaa"},
		{"NYAA", "*provider.Nyaa"},
		{"nyaa.si", "*provider.Nyaa"},
		{"eztv", "*provider.EZTV"},
		{"eztvx.to", "*provider.EZTV"},
	} {
		t.Run(c.base, func(t *testing.T) {
			normalized := config.NormalizeBase(c.base)
			hermeticProviderSelection(t, normalized)

			p := newProvider()
			if got := fmt.Sprintf("%T", p); got != c.want {
				t.Fatalf("newProvider(base=%q) = %s, want %s", normalized, got, c.want)
			}
			token := providerBase(p)
			if token == "" {
				t.Fatalf("providerBase(%T) = \"\"; a ref from --base %s would lose its source", p, c.base)
			}
			hermeticProviderSelection(t, token)
			if got := fmt.Sprintf("%T", newProvider()); got != c.want {
				t.Fatalf("newProvider(base=%q) = %s, want %s; the stamped token does not round-trip", token, got, c.want)
			}
			if !config.IsTorrentBase(c.base) {
				t.Errorf("config.IsTorrentBase(%q) = false; mayStreamTorrent would leave this magnet-only run on the mmap backend", c.base)
			}
			withBase(t, normalized)
			if !mayStreamTorrent(cfg) {
				t.Errorf("mayStreamTorrent(base=%q) = false, want true: the primary resolves nothing but magnets", normalized)
			}
		})
	}
}

// The non-torrent bases must stay out of IsTorrentBase, or every scraping run
// would pay a needless re-exec.
func TestIsTorrentBaseIsFalseForEveryScrapingBase(t *testing.T) {
	for _, base := range []string{"auto", "flixhq.ws", "tbcpl", "1shows.org", "animeonsen"} {
		if config.IsTorrentBase(base) {
			t.Errorf("config.IsTorrentBase(%q) = true, want false", base)
		}
	}
	for _, base := range []string{"yts", "yts.mx", "nyaa", "eztv"} {
		if !config.IsTorrentBase(base) {
			t.Errorf("config.IsTorrentBase(%q) = false, want true", base)
		}
	}
}

// Nyaa and EZTV never enter the chain on their own: a magnet joins a swarm and
// exposes the user's IP, which is a choice the user makes with
// torrent_fallback (or by naming the source), not one a broken scraper makes
// for them. Same gate as YTS.
func TestFallbackProvidersGatesNyaaAndEZTVOnTorrentFallback(t *testing.T) {
	prev := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = prev })

	got := fallbackProviders(nil)
	if hasProvider[*provider.Nyaa](got) || hasProvider[*provider.EZTV](got) || hasProvider[*provider.YTS](got) {
		t.Fatalf("torrent sources present with torrent_fallback off: %v", providerNames(got))
	}
}

// With torrent_fallback on, the whole chain in order: EZTV after TBCPL and
// before FlixHQWS (a series' complete encode ahead of the remaining scrapers),
// Nyaa right after AnimeOnsen (anime the streaming anime source lacks), YTS
// last.
func TestFallbackProvidersOrderWithTorrentFallback(t *testing.T) {
	prev := cfg
	cfg = &config.Config{TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	want := []string{
		"*provider.TBCPL", "*provider.EZTV", "*provider.FlixHQWS",
		"*provider.AnimeOnsen", "*provider.Nyaa", "*provider.YTS",
	}
	got := providerNames(fallbackProviders(nil))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("fallbackProviders(nil) = %v, want %v", got, want)
	}
}

// The primary is left out of its own chain, for the torrent sources as for
// every other.
func TestFallbackProvidersExcludesATorrentPrimary(t *testing.T) {
	prev := cfg
	cfg = &config.Config{TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	if got := fallbackProviders(provider.NewNyaa()); hasProvider[*provider.Nyaa](got) {
		t.Errorf("Nyaa primary is in its own chain: %v", providerNames(got))
	}
	if got := fallbackProviders(provider.NewEZTV()); hasProvider[*provider.EZTV](got) {
		t.Errorf("EZTV primary is in its own chain: %v", providerNames(got))
	}
}
