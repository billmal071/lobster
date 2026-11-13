package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

func TestX1337BaseBuildsItsProviderAndRoundTrips(t *testing.T) {
	withOutputFlags(t, false, "")
	for _, base := range []string{"1337x", "1337X", "1337x.to", "1337xx.to", "x1337x.eu"} {
		t.Run(base, func(t *testing.T) {
			normalized := config.NormalizeBase(base)
			hermeticProviderSelection(t, normalized)
			p := newProvider()
			if got := fmt.Sprintf("%T", p); got != "*provider.X1337" {
				t.Fatalf("newProvider(base=%q) = %s, want *provider.X1337", normalized, got)
			}
			token := providerBase(p)
			if token != "1337x" {
				t.Fatalf("providerBase = %q, want 1337x", token)
			}
			hermeticProviderSelection(t, token)
			if got := fmt.Sprintf("%T", newProvider()); got != "*provider.X1337" {
				t.Fatalf("stamped token %q does not round-trip: %s", token, got)
			}
			if !config.IsTorrentBase(base) {
				t.Errorf("config.IsTorrentBase(%q) = false; the swarm re-exec path would be skipped", base)
			}
			withBase(t, normalized)
			if !mayStreamTorrent(cfg) {
				t.Errorf("mayStreamTorrent(base=%q) = false, want true", normalized)
			}
		})
	}
}

func TestX1337IsAMagnetSourceForTheDownloadQueue(t *testing.T) {
	if !servesMagnets(provider.NewX1337()) {
		t.Fatal("servesMagnets(X1337) = false; the download queue would be handed a magnet it cannot open")
	}
	chain := []provider.Provider{provider.NewVaPlayer(), provider.NewX1337()}
	if got := withoutMagnetSources(chain); len(got) != 1 || hasProvider[*provider.X1337](got) {
		t.Fatalf("withoutMagnetSources kept X1337: %v", providerNames(got))
	}
}

// 1337x joins a swarm: it must be absent from the chain unless torrent_fallback
// is set, and then belongs to the torrent tier.
func TestX1337IsReachableOnlyThroughTorrentFallbackInTheTorrentTier(t *testing.T) {
	prev := cfg
	t.Cleanup(func() { cfg = prev })

	cfg = &config.Config{}
	if got := fallbackProviders(nil); hasProvider[*provider.X1337](got) {
		t.Fatalf("1337x in the chain with torrent_fallback off: %v", providerNames(got))
	}
	cfg = nil
	if got := fallbackProviders(nil); hasProvider[*provider.X1337](got) {
		t.Fatalf("1337x in the chain with no config: %v", providerNames(got))
	}

	cfg = &config.Config{TorrentFallback: true}
	got := fallbackProviders(nil)
	if !hasProvider[*provider.X1337](got) {
		t.Fatalf("1337x missing under torrent_fallback: %v", providerNames(got))
	}
	if !hasProvider[*provider.X1337](onlyMagnetSources(got)) || hasProvider[*provider.X1337](withoutMagnetSources(got)) {
		t.Fatalf("1337x is not in the torrent tier, which tryFallbackStreamFrom races only after every streaming source fails: %v", providerNames(got))
	}
	if got := fallbackProviders(provider.NewX1337()); hasProvider[*provider.X1337](got) {
		t.Errorf("1337x primary is in its own chain: %v", providerNames(got))
	}
}
