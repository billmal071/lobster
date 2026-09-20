package cmd

import (
	"encoding/json"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// decodeFindRefs runs findRun over stubbed results and returns the decoded
// refs, in emitted order.
func decodeFindRefs(t *testing.T, rows []media.SearchResult) []playRef {
	t.Helper()
	buf := captureAgentOut(t)

	prevSearch := agentSearch
	agentSearch = func(provider.Provider, []provider.Provider, string) ([]media.SearchResult, error) {
		return rows, nil
	}
	t.Cleanup(func() { agentSearch = prevSearch })

	if err := findRun(findCmd, []string{"the matrix"}); err != nil {
		t.Fatalf("findRun: %v", err)
	}

	var got struct {
		Results []struct {
			Ref string `json:"ref"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	refs := make([]playRef, 0, len(got.Results))
	for i, r := range got.Results {
		ref, err := decodeRef(r.Ref)
		if err != nil {
			t.Fatalf("result %d: ref does not decode: %v", i, err)
		}
		refs = append(refs, ref)
	}
	return refs
}

// The guarantee: an emitted ref's base names the provider that produced that
// row, never one that did not.
//
// The input is the shape that violates it. `--base yts` is asked for, YTS is
// the primary, and the response also carries rows the fallback chain supplied
// — which is not a corner case but the normal one, because gatherSearchResults
// broadens whenever the primary answers with fewer than three rows
// (cmd/multisearch.go). Stamping the requested base uniformly put a scraper's
// TMDB ID under the name "yts", and that is not a cosmetic mislabel: base is
// what mayStreamTorrent reads (cmd/root.go), so replaying such a ref re-execs
// onto the torrent storage backend and resolves a magnet for a film YTS never
// returned.
func TestFindStampsEachRowWithTheProviderThatProducedIt(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "yts"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return provider.NewYTS() }
	t.Cleanup(func() { agentProvider = prevProv })

	refs := decodeFindRefs(t, []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
		{ID: "movie/603", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "soap2day"},
		{ID: "anipub/42", Title: "The Matrix Revolutions", Year: "2003", Type: media.Movie},
	})
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3", len(refs))
	}

	if refs[0].Base != "yts" {
		t.Errorf("ref for the YTS row has base %q, want %q", refs[0].Base, "yts")
	}
	if refs[1].Base != "soap2day" {
		t.Errorf("ref for the Soap2Day row has base %q, want %q: the row carries a TMDB id YTS cannot resolve", refs[1].Base, "soap2day")
	}
	if config.IsYTSBase(refs[1].Base) {
		t.Errorf("ref for the Soap2Day row has base %q, which mayStreamTorrent reads as YTS; replaying it opens a magnet for a film YTS never returned", refs[1].Base)
	}
	if refs[2].Base != "" {
		t.Errorf("ref for the unattributable row has base %q, want \"\": applyRefBase leaves the configuration alone only on an empty base", refs[2].Base)
	}
}

// The caller's own spelling of a base survives when the row really did come
// from the primary. A base is matched by substring (newProvider), so
// "flixhq.xx" names a specific mirror; collapsing that row to the generic
// "flixhq" would send a later play to the default domain instead, which is a
// different source than the one that answered.
func TestFindKeepsTheConfiguredSpellingForRowsFromThePrimary(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.xx"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return provider.NewFlixHQ("flixhq.xx") }
	t.Cleanup(func() { agentProvider = prevProv })

	refs := decodeFindRefs(t, []media.SearchResult{
		{ID: "movie/1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "flixhq"},
		{ID: "movie/2", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie, Provider: "vidnest"},
	})
	if refs[0].Base != "flixhq.xx" {
		t.Errorf("ref for the primary's row has base %q, want %q", refs[0].Base, "flixhq.xx")
	}
	if refs[1].Base != "vidnest" {
		t.Errorf("ref for the fallback row has base %q, want %q", refs[1].Base, "vidnest")
	}
}

// "auto" is never stamped, even when it is what the caller configured. It
// names no provider: it is a licence to route movies to YTS
// (baseIsAuto/routeByType) and to open a magnet (mayStreamTorrent), so on a
// row a scraper produced it is both false and dangerous.
func TestFindNeverStampsAutoOnARowAScraperProduced(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: config.BaseAuto}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return provider.NewSoap2Day() }
	t.Cleanup(func() { agentProvider = prevProv })

	refs := decodeFindRefs(t, []media.SearchResult{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
	})
	if refs[0].Base == config.BaseAuto {
		t.Fatalf("ref.Base = %q; auto licenses the YTS movie route and a magnet, so it is not the name of the provider that answered", refs[0].Base)
	}
	if refs[0].Base != "soap2day" {
		t.Fatalf("ref.Base = %q, want %q", refs[0].Base, "soap2day")
	}
}

// attributingProvider is a stub that answers one fixed result set and is
// mapped onto a base token by the searchProviderBase seam.
type attributingProvider struct {
	stubProvider
	token string
}

// Search returns a fresh copy: gatherSearchResults stamps attribution in
// place, and a stub handing out the same backing array twice would hide that.
func (a *attributingProvider) Search(string) ([]media.SearchResult, error) {
	out := make([]media.SearchResult, len(a.results))
	copy(out, a.results)
	return out, a.searchErr
}

// withStubAttribution maps stub providers onto base tokens for the duration of
// one test, so the merge layer can be driven without real providers.
func withStubAttribution(t *testing.T) {
	t.Helper()
	prev := searchProviderBase
	searchProviderBase = func(p provider.Provider) string {
		if a, ok := p.(*attributingProvider); ok {
			return a.token
		}
		return prev(p)
	}
	t.Cleanup(func() { searchProviderBase = prev })
}

// The real gatherSearchResults must attribute every row it returns, primary
// and fallback alike. Without this the find-level tests above would be
// asserting against a field nothing in production ever sets.
func TestGatherSearchResultsAttributesEveryRowToItsProducer(t *testing.T) {
	withStubAttribution(t)

	primary := &attributingProvider{token: "yts", stubProvider: stubProvider{results: []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}}}
	fallbacks := []provider.Provider{
		&attributingProvider{token: "soap2day", stubProvider: stubProvider{results: []media.SearchResult{
			{ID: "movie/604", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		}}},
		&attributingProvider{token: "vidnest", stubProvider: stubProvider{results: []media.SearchResult{
			{ID: "movie/605", Title: "The Matrix Revolutions", Year: "2003", Type: media.Movie},
		}}},
	}

	got, err := gatherSearchResults(primary, fallbacks, "the matrix")
	if err != nil {
		t.Fatalf("gatherSearchResults: %v", err)
	}
	want := map[string]string{
		"The Matrix":             "yts",
		"The Matrix Reloaded":    "soap2day",
		"The Matrix Revolutions": "vidnest",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		if r.Provider != want[r.Title] {
			t.Errorf("%q attributed to %q, want %q", r.Title, r.Provider, want[r.Title])
		}
	}
}

// Deduplication pins the first arrival's ID (so playback is handed an ID the
// primary can resolve). The attribution has to be pinned with it, or the
// merged row carries one provider's ID under another provider's name — which
// is the same lie in a subtler place.
//
// The input is the one that would violate it: the *fallback* entry is richer,
// so fillGaps takes it as the base and its own Provider field would otherwise
// survive, while the ID kept is the primary's.
func TestDeduplicateResultsPinsAttributionToTheSurvivingID(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "3024", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
	}
	fallback := [][]media.SearchResult{{
		// Richer on every axis resultScore counts.
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie,
			Provider: "soap2day", Poster: "p.jpg", Duration: "136m", URL: "https://example.invalid/603"},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 1 {
		t.Fatalf("got %d merged rows, want 1: %+v", len(merged), merged)
	}
	if merged[0].ID != "3024" {
		t.Fatalf("merged ID = %q, want the first arrival's %q", merged[0].ID, "3024")
	}
	if merged[0].Provider != "yts" {
		t.Fatalf("merged row keeps ID %q but is attributed to %q; the id and the name of the source that can resolve it must travel together", merged[0].ID, merged[0].Provider)
	}
	// The metadata merge itself must still happen.
	if merged[0].Poster != "p.jpg" {
		t.Fatalf("merged row lost the richer entry's poster: %+v", merged[0])
	}
}

// The mirror case: when the first arrival has no ID at all, fillGaps takes the
// incoming one, so the incoming provider is the one that can answer for it.
func TestDeduplicateResultsFollowsTheIDWhenTheFirstArrivalHasNone(t *testing.T) {
	primary := []media.SearchResult{
		{ID: "", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "yts"},
	}
	fallback := [][]media.SearchResult{{
		{ID: "movie/603", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "soap2day"},
	}}

	merged := deduplicateResults(primary, fallback)
	if len(merged) != 1 {
		t.Fatalf("got %d merged rows, want 1: %+v", len(merged), merged)
	}
	if merged[0].ID != "movie/603" {
		t.Fatalf("merged ID = %q, want %q", merged[0].ID, "movie/603")
	}
	if merged[0].Provider != "soap2day" {
		t.Fatalf("merged row carries id %q but is attributed to %q", merged[0].ID, merged[0].Provider)
	}
}

// newProvider has no unknown-base arm: anything it does not recognise falls
// through to MovieBox (GUIDE.md's source table warns about exactly this). A
// row MovieBox produced must be named "moviebox" and not echoed back as the
// caller's typo — "sopa2day" names no source at all, and a later play would
// resolve it back to MovieBox by the same fall-through while telling the user
// something false about where the row came from.
func TestFindNamesTheFallThroughProviderRatherThanEchoingAnUnknownBase(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "sopa2day"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return provider.NewMovieBox() }
	t.Cleanup(func() { agentProvider = prevProv })

	refs := decodeFindRefs(t, []media.SearchResult{
		{ID: "1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "moviebox"},
	})
	if refs[0].Base != "moviebox" {
		t.Fatalf("ref.Base = %q, want %q: the base fell through to MovieBox, which is what answered", refs[0].Base, "moviebox")
	}
}

// A fallback row must not inherit the primary's spelling just because the
// primary's base contains its token. "flixhq.ws" contains "flixhq", so a
// FlixHQ row under a FlixHQWS primary is the input that breaks a naive
// prefix-preserving stamp — and the two are different sites with different,
// non-portable ids.
func TestFindDoesNotRestampAFallbackRowWithThePrimarySpelling(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Base: "flixhq.ws"}
	t.Cleanup(func() { cfg = prevCfg })

	prevProv := agentProvider
	agentProvider = func() provider.Provider { return provider.NewFlixHQWS("flixhq.ws") }
	t.Cleanup(func() { agentProvider = prevProv })

	refs := decodeFindRefs(t, []media.SearchResult{
		{ID: "movie/1", Title: "The Matrix", Year: "1999", Type: media.Movie, Provider: "flixhq"},
	})
	if refs[0].Base != "flixhq" {
		t.Fatalf("ref.Base = %q, want %q: the row came from flixhq.to, not from flixhq.ws", refs[0].Base, "flixhq")
	}
}
