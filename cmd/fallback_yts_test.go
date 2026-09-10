package cmd

import (
	"fmt"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

func ytsProviderNames(ps []provider.Provider) []string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, fmt.Sprintf("%T", p))
	}
	return names
}

func hasYTS(ps []provider.Provider) bool {
	for _, p := range ps {
		if _, ok := p.(*provider.YTS); ok {
			return true
		}
	}
	return false
}

// YTS resolves to a magnet, so falling back to it makes lobster join a
// BitTorrent swarm and expose the user's IP to its peers. That is a real
// consequence of a search that would otherwise just fail, so it must never
// happen unless the user asked for it.
func TestFallbackProvidersOmitsYTSByDefault(t *testing.T) {
	prev := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = prev })

	if got := fallbackProviders(nil); hasYTS(got) {
		t.Fatalf("YTS in chain without opt-in: %v", ytsProviderNames(got))
	}
}

func TestFallbackProvidersOmitsYTSWithNilConfig(t *testing.T) {
	prev := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prev })

	if got := fallbackProviders(nil); hasYTS(got) {
		t.Fatalf("YTS in chain with nil cfg: %v", ytsProviderNames(got))
	}
}

func TestFallbackProvidersIncludesYTSWhenOptedIn(t *testing.T) {
	prev := cfg
	cfg = &config.Config{TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	if got := fallbackProviders(nil); !hasYTS(got) {
		t.Fatalf("YTS missing after opt-in: %v", ytsProviderNames(got))
	}
}

// Naming a source stops the per-type route reaching YTS, but it does not keep
// the run out of a swarm on its own: this chain appends YTS on torrent_fallback
// alone, without consulting Base or APIURL. Three docs said or implied
// otherwise — README's api_url block claimed such a run "never joins a swarm" —
// so pin the behaviour the wording now has to match.
func TestFallbackProvidersIncludesYTSEvenWithAnExplicitSource(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  *config.Config
	}{
		{"an explicit non-YTS base", &config.Config{Base: "soap2day", TorrentFallback: true}},
		{"a configured api_url", &config.Config{APIURL: "https://api.consumet.example", TorrentFallback: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			prev := cfg
			cfg = c.cfg
			t.Cleanup(func() { cfg = prev })

			if got := fallbackProviders(nil); !hasYTS(got) {
				t.Fatalf("YTS missing from the chain under %+v: %v; torrent_fallback puts it back whatever source is named", c.cfg, ytsProviderNames(got))
			}
		})
	}
}

// The download engine cannot open a magnet. streamToResult classifies by
// substring, so without a guard a magnet is labelled "http" and handed
// straight to an engine that will fail on it well after the user walked away.
func TestStreamToResultRejectsMagnet(t *testing.T) {
	_, err := streamToResultChecked(&media.Stream{
		URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Movie",
	})
	if err == nil {
		t.Fatal("magnet accepted for download; want an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "torrent") {
		t.Fatalf("error should name the cause, got: %v", err)
	}
}

func TestStreamToResultAcceptsHLS(t *testing.T) {
	got, err := streamToResultChecked(&media.Stream{URL: "https://example.com/x.m3u8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StreamType != "hls" {
		t.Fatalf("StreamType = %q, want hls", got.StreamType)
	}
}
