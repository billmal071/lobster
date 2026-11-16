package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/provider"
)

func indexOfProvider[T provider.Provider](ps []provider.Provider) int {
	for i, p := range ps {
		if _, ok := p.(T); ok {
			return i
		}
	}
	return -1
}

func TestVidrockIsAnHTTPFallbackAfterTheOtherHTTPSourcesAndBeforeTorrents(t *testing.T) {
	prev := cfg
	t.Cleanup(func() { cfg = prev })
	cfg = &config.Config{TorrentFallback: true}

	got := fallbackProviders(nil)
	v := indexOfProvider[*provider.Vidrock](got)
	if v < 0 {
		t.Fatalf("Vidrock missing from the chain: %v", providerNames(got))
	}
	for _, earlier := range []int{
		indexOfProvider[*provider.TBCPL](got),
		indexOfProvider[*provider.FlixHQWS](got),
		indexOfProvider[*provider.AnimeOnsen](got),
	} {
		if earlier < 0 || earlier > v {
			t.Fatalf("Vidrock at %d is not after the other HTTP sources: %v", v, providerNames(got))
		}
	}
	for _, later := range []int{
		indexOfProvider[*provider.YTS](got),
		indexOfProvider[*provider.X1337](got),
	} {
		if later < v {
			t.Fatalf("Vidrock at %d is not before the torrent sources: %v", v, providerNames(got))
		}
	}
}

func TestVidrockIsNotATorrentSourceAndSurvivesWithoutTorrentFallback(t *testing.T) {
	prev := cfg
	t.Cleanup(func() { cfg = prev })
	cfg = &config.Config{}

	got := fallbackProviders(nil)
	if !hasProvider[*provider.Vidrock](got) {
		t.Fatalf("Vidrock missing with torrent_fallback off: %v", providerNames(got))
	}
	if servesMagnets(provider.NewVidrock()) {
		t.Fatal("Vidrock serves HLS, not magnets")
	}
	if !hasProvider[*provider.Vidrock](withoutMagnetSources(got)) || hasProvider[*provider.Vidrock](onlyMagnetSources(got)) {
		t.Fatalf("Vidrock is in the torrent tier: %v", providerNames(got))
	}
	for _, p := range got {
		if servesMagnets(p) {
			t.Fatalf("torrent source %T in the chain with torrent_fallback off", p)
		}
	}
}

func TestVidrockPrimaryIsLeftOutOfItsOwnChain(t *testing.T) {
	prev := cfg
	t.Cleanup(func() { cfg = prev })
	cfg = &config.Config{TorrentFallback: true}
	if got := fallbackProviders(provider.NewVidrock()); hasProvider[*provider.Vidrock](got) {
		t.Fatalf("Vidrock primary is in its own chain: %v", providerNames(got))
	}
}

func TestVidrockBaseBuildsItsProviderAndRoundTrips(t *testing.T) {
	withOutputFlags(t, false, "")
	for _, base := range []string{"vidrock", "Vidrock", "vidrock.to"} {
		t.Run(base, func(t *testing.T) {
			hermeticProviderSelection(t, config.NormalizeBase(base))
			p := newProvider()
			if got := fmt.Sprintf("%T", p); got != "*provider.Vidrock" {
				t.Fatalf("newProvider(base=%q) = %s, want *provider.Vidrock", base, got)
			}
			token := providerBase(p)
			if token != "vidrock" {
				t.Fatalf("providerBase = %q, want vidrock", token)
			}
			hermeticProviderSelection(t, token)
			if got := fmt.Sprintf("%T", newProvider()); got != "*provider.Vidrock" {
				t.Fatalf("token %q selects %s", token, got)
			}
		})
	}
	if config.IsTorrentBase("vidrock") {
		t.Fatal("vidrock is not a torrent base")
	}
	if _, _, retired := config.RetiredBase("vidrock"); retired {
		t.Fatal("vidrock must not be a retired base")
	}
}

func TestDoctorProbesVidrockWithAMovieItSurelyHas(t *testing.T) {
	for _, n := range doctorProviders() {
		if n.Name != "Vidrock" {
			continue
		}
		if _, ok := n.Provider.(*provider.Vidrock); !ok {
			t.Fatalf("doctor entry Vidrock holds %T", n.Provider)
		}
		if n.Query != doctorVidrockQuery || doctorVidrockQuery == "" {
			t.Fatalf("doctor query = %q, want the fixed %q", n.Query, doctorVidrockQuery)
		}
		return
	}
	t.Fatal("doctor does not probe Vidrock")
}

func TestStreamChainAsksAVidrockPrimaryForTheEpisodeFirst(t *testing.T) {
	withDefaultConfig(t)
	got := streamChain(provider.NewVidrock())
	if indexOfProvider[*provider.Vidrock](got) != 0 {
		t.Fatalf("streamChain(Vidrock) = %v, want Vidrock first: it cannot list seasons, so the chain is the only way it is asked to Watch", providerNames(got))
	}
	if n := countProviders[*provider.Vidrock](got); n != 1 {
		t.Fatalf("Vidrock appears %d times in %v, want once", n, providerNames(got))
	}
}

func TestStreamChainKeepsASingleVidrockForOtherPrimaries(t *testing.T) {
	withDefaultConfig(t)
	if n := countProviders[*provider.Vidrock](streamChain(provider.NewTBCPL("tbcpl"))); n != 1 {
		t.Fatalf("Vidrock appears %d times for a TBCPL primary, want its single fallback slot", n)
	}
}

func countProviders[T provider.Provider](ps []provider.Provider) int {
	n := 0
	for _, p := range ps {
		if _, ok := p.(T); ok {
			n++
		}
	}
	return n
}
