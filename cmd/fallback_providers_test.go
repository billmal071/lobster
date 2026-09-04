package cmd

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/provider"
)

// providerNames lists the concrete Go types in a provider slice, which is what
// these tests assert on — the chain is defined by which providers are in it,
// not by their configuration. Provider has no Name method, so use %T.
func providerNames(ps []provider.Provider) []string {
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, fmt.Sprintf("%T", p))
	}
	return names
}

func hasProvider[T provider.Provider](ps []provider.Provider) bool {
	for _, p := range ps {
		if _, ok := p.(T); ok {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	// waitLiveness (detach_test.go) needs a real child process whose exit it
	// can observe, without launching a real media player or depending on a
	// shell being on PATH (this test binary also has to build and run on
	// windows-latest CI). The trick: re-exec this same test binary with this
	// env var set, and short-circuit straight to os.Exit before the testing
	// package ever gets involved, so it behaves as a plain, portable,
	// disposable helper process rather than another test run.
	if code := os.Getenv("LOBSTER_TEST_HELPER_EXIT_CODE"); code != "" {
		if ms := os.Getenv("LOBSTER_TEST_HELPER_SLEEP_MS"); ms != "" {
			if n, err := strconv.Atoi(ms); err == nil {
				time.Sleep(time.Duration(n) * time.Millisecond)
			}
		}
		n, err := strconv.Atoi(code)
		if err != nil {
			n = 1
		}
		os.Exit(n)
	}

	// Stub flixhqDomain to prevent live network probes in tests.
	// Tests that need a healthy or dead result override this with t.Cleanup restore.
	prev := flixhqDomain
	flixhqDomain = func(string, map[string][]string) string { return "" }
	defer func() { flixhqDomain = prev }()
	m.Run()
}

// Consumet is a full aggregator that was implemented but never reachable from
// the fallback chain — it was only ever built as the primary, and only when
// api_url was set. Wiring it in is the cheapest provider we can add.
func TestFallbackProvidersIncludesConsumetWhenAPIURLSet(t *testing.T) {
	prev := cfg
	cfg = &config.Config{APIURL: "https://api.consumet.example"}
	t.Cleanup(func() { cfg = prev })

	got := fallbackProviders(nil)
	if !hasProvider[*provider.Consumet](got) {
		t.Fatalf("Consumet missing from fallback chain: %v", providerNames(got))
	}
}

// Without a base URL there is nothing for Consumet to talk to, so it must stay
// out rather than join the chain and fail every request.
func TestFallbackProvidersOmitsConsumetWithoutAPIURL(t *testing.T) {
	prev := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = prev })

	got := fallbackProviders(nil)
	if hasProvider[*provider.Consumet](got) {
		t.Fatalf("Consumet present without api_url: %v", providerNames(got))
	}
}

// A nil cfg must not panic and must not produce a Consumet with an empty base.
func TestFallbackProvidersOmitsConsumetWithNilConfig(t *testing.T) {
	prev := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prev })

	got := fallbackProviders(nil)
	if hasProvider[*provider.Consumet](got) {
		t.Fatalf("Consumet present with nil cfg: %v", providerNames(got))
	}
}

// FlixHQ (the flixhq.to-engine scraper) is gated on a live health probe: it
// joins the chain only when some mirror in knownDomains/overrides answers, so
// a dead provider costs one parallel probe per session instead of a full
// request timeout on every search.
func TestFallbackProvidersIncludesFlixHQWhenDomainHealthy(t *testing.T) {
	prev := flixhqDomain
	flixhqDomain = func(name string, overrides map[string][]string) string { return "flixhq.to" }
	t.Cleanup(func() { flixhqDomain = prev })

	if !hasProvider[*provider.FlixHQ](fallbackProviders(nil)) {
		t.Fatal("FlixHQ missing from chain despite a healthy domain")
	}
}

func TestFallbackProvidersOmitsFlixHQWhenAllDomainsDead(t *testing.T) {
	prev := flixhqDomain
	flixhqDomain = func(name string, overrides map[string][]string) string { return "" }
	t.Cleanup(func() { flixhqDomain = prev })

	got := fallbackProviders(nil)
	if hasProvider[*provider.FlixHQ](got) {
		t.Fatal("FlixHQ present in chain although no domain is healthy")
	}
	if !hasProvider[*provider.FlixHQWS](got) {
		t.Fatal("FlixHQWS should stay in the chain even when the flixhq gate is closed")
	}
}

// AllAnime is retired: its API sits behind a Cloudflare bot challenge and its
// watch path has been crypto-gated since mid-2026. AniPub covers anime.
func TestFallbackProvidersNeverIncludesAllAnime(t *testing.T) {
	if hasProvider[*provider.AllAnime](fallbackProviders(nil)) {
		t.Fatal("AllAnime should be retired from the fallback chain")
	}
}
