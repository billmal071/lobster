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
