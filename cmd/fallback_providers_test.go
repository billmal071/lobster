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
		if path := os.Getenv("LOBSTER_TEST_HELPER_MODE_FILE"); path != "" {
			if err := os.WriteFile(path, []byte(os.Getenv("LOBSTER_TEST_HELPER_MODE")+"\n"), 0o600); err != nil {
				os.Exit(74)
			}
		}
		n, err := strconv.Atoi(code)
		if err != nil {
			n = 1
		}
		os.Exit(n)
	}

	// Keep the per-installation ref key (config.RefKeyPath, under dataDir)
	// out of the developer's real ~/.local/share. sourceKey creates it on
	// first use, and the live-TV tests mint refs, so without this a plain
	// `go test ./cmd/` would leave state behind on the machine it ran on.
	// Set here rather than per-test because any test that mints a ref
	// reaches it, including ones that have no other reason to isolate HOME.
	// Failing loudly, not silently: if this directory cannot be made and the
	// suite runs anyway, the first ref a test mints writes a refkey into the
	// developer's real data directory. A test run must not leave state on the
	// machine it ran on, so an unusable temp dir aborts the run instead.
	dataDir, err := os.MkdirTemp("", "lobster-test-data-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: cannot isolate the test data directory: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("XDG_DATA_HOME", dataDir) // unix (config.dataDir)
	os.Setenv("LOCALAPPDATA", dataDir)  // windows
	defer os.RemoveAll(dataDir)

	// applyConfig re-execs this process onto the torrent library's classic
	// storage backend when a run could open a magnet and the backend has not
	// been chosen. A test binary must never do that — it would replace itself
	// with a fresh `go test` run, repeatedly. Choosing the backend here makes
	// the check a no-op, and classic is what the re-exec would have selected.
	os.Setenv("TORRENT_STORAGE_DEFAULT_FILE_IO", "classic")

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

// The chain is exactly the sources verified working on 2026-11-07, in
// priority order: VaPlayer, TBCPL, FlixHQWS, AnimeOnsen. (TBCPLEmbed joins
// when the catalog feed is on, Consumet when api_url is set, YTS when
// torrent_fallback is set — none of which this fixture enables.)
func TestFallbackProvidersIsExactlyTheWorkingSources(t *testing.T) {
	prev := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = prev })

	want := []string{"*provider.VaPlayer", "*provider.TBCPL", "*provider.FlixHQWS", "*provider.AnimeOnsen"}
	got := providerNames(fallbackProviders(nil))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("fallbackProviders(nil) = %v, want %v", got, want)
	}
}

// The primary is excluded from its own chain, so under the default
// (autoBase = VaPlayer) the resolver's chain starts at TBCPL.
func TestFallbackProvidersExcludesTheVaPlayerPrimary(t *testing.T) {
	prev := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = prev })

	want := []string{"*provider.TBCPL", "*provider.FlixHQWS", "*provider.AnimeOnsen"}
	got := providerNames(fallbackProviders(provider.NewVaPlayer()))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("fallbackProviders(VaPlayer) = %v, want %v", got, want)
	}
}

// The retired sources (config.retiredBases) must stay out of the chain: each
// one costs a probe timeout per resolve for an answer that can only be a failure.
func TestFallbackProvidersNeverIncludesARetiredSource(t *testing.T) {
	prev := cfg
	cfg = &config.Config{TorrentFallback: true, APIURL: "https://api.consumet.example"}
	t.Cleanup(func() { cfg = prev })

	got := fallbackProviders(nil)
	for name, present := range map[string]bool{
		"Soap2Day":   hasProvider[*provider.Soap2Day](got),
		"MovieBox":   hasProvider[*provider.MovieBox](got),
		"VidNest":    hasProvider[*provider.VidNest](got),
		"FlixHQ":     hasProvider[*provider.FlixHQ](got),
		"KimCartoon": hasProvider[*provider.KimCartoon](got),
		"AllAnime":   hasProvider[*provider.AllAnime](got),
		"AniPub":     hasProvider[*provider.AniPub](got),
	} {
		if present {
			t.Errorf("%s is retired and must not be in the fallback chain: %v", name, providerNames(got))
		}
	}
	if !hasProvider[*provider.FlixHQWS](got) {
		t.Errorf("FlixHQWS is a different, working provider and must stay: %v", providerNames(got))
	}
}
