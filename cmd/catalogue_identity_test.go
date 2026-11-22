package cmd

import (
	"errors"
	"lobster/internal/catalog"
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

type idRecordingProvider struct {
	*stubProvider
	seasonIDs []string
}

func (p *idRecordingProvider) GetSeasons(id string) ([]media.Season, error) {
	p.seasonIDs = append(p.seasonIDs, id)
	return p.stubProvider.GetSeasons(id)
}

func catalogueHarness(t *testing.T) string {
	t.Helper()
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)
	recordSelections(t, 0)
	return stubStreamServer(t)
}

func TestCatalogueRowAsksTheProviderForItsOwnIDAndSavesUnderTheCatalogueID(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	prov := &idRecordingProvider{stubProvider: &stubProvider{
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}}
	sel := media.SearchResult{ID: "tv/37854", ProviderID: "tv/one-piece-100", Title: "Some Show", Year: "2013", Type: media.TV}

	if err := resolveAndPlay(prov, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if len(prov.seasonIDs) != 1 || prov.seasonIDs[0] != "tv/one-piece-100" {
		t.Fatalf("provider asked for %v, want tv/one-piece-100", prov.seasonIDs)
	}
	entries, err := history.Load()
	if err != nil || len(entries) != 1 || entries[0].ID != "tv/37854" {
		t.Fatalf("history = %+v, %v; want one row under tv/37854", entries, err)
	}
}

func TestUnresolvedCatalogueRowNeverReachesTheProviderWithItsCatalogueID(t *testing.T) {
	url := catalogueHarness(t)
	fb := newListingChainProvider(url)
	withFallbackChain(t, fb)
	prov := &idRecordingProvider{stubProvider: &stubProvider{seasons: []media.Season{{ID: "s1", Number: 1}}}}
	sel := media.SearchResult{ID: "tv/37854", Unresolved: true, Title: "Some Show", Year: "2013", Type: media.TV}

	if err := resolveAndPlay(prov, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay = %v; the chain can still play an unresolved row", err)
	}
	if len(prov.seasonIDs) != 0 {
		t.Fatalf("provider was asked for %v", prov.seasonIDs)
	}
	entries, err := history.Load()
	if err != nil || len(entries) != 1 || entries[0].ID != "tv/37854" {
		t.Fatalf("history = %+v, %v; want one row under tv/37854", entries, err)
	}
}

func TestUnresolvedCatalogueRowNeverPlaysASameTitledSeriesFromAnotherYear(t *testing.T) {
	url := catalogueHarness(t)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2020"
	withFallbackChain(t, fb)
	prov := &idRecordingProvider{stubProvider: &stubProvider{seasons: []media.Season{{ID: "s1", Number: 1}}}}
	sel := media.SearchResult{ID: "tv/37854", Unresolved: true, FromCatalogue: true, Title: "Some Show", Year: "2013", Type: media.TV}

	if err := resolveAndPlay(prov, sel, 1, 22); err == nil {
		t.Fatal("resolveAndPlay = nil; the chain only has the 2020 series")
	}
	if asked := fb.mediaAsked(); asked != "" {
		t.Fatalf("the 2020 series %s was asked for a stream", asked)
	}
	entries, err := history.Load()
	if err != nil || len(entries) != 0 {
		t.Fatalf("history = %+v, %v; want nothing saved under the 2013 row", entries, err)
	}
}

type idServers struct {
	*stubProvider
	url       string
	serverIDs []string
}

func (p *idServers) GetServers(id, ep string) ([]media.Server, error) {
	p.serverIDs = append(p.serverIDs, id)
	return p.stubProvider.GetServers(id, ep)
}

func (p *idServers) Watch(id, _, _, _ string) (*media.Stream, error) {
	p.serverIDs = append(p.serverIDs, id)
	return &media.Stream{URL: p.url}, nil
}

func TestUnresolvedCatalogueMovieNeverReachesTheProviderWithItsCatalogueID(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	prov := &idServers{stubProvider: &stubProvider{}, url: url}
	sel := media.SearchResult{ID: "movie/603", Unresolved: true, Title: "Some Show", Year: "2013", Type: media.Movie}
	_ = resolveAndPlay(prov, sel, 0, 0)
	if len(prov.serverIDs) != 0 {
		t.Fatalf("provider was asked for %v", prov.serverIDs)
	}
}

func TestCatalogueMovieAsksTheProviderForItsOwnID(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	prov := &idServers{stubProvider: &stubProvider{}, url: url}
	sel := media.SearchResult{ID: "movie/603", ProviderID: "movie/the-matrix-7", Title: "Some Show", Year: "2013", Type: media.Movie}
	_ = resolveAndPlay(prov, sel, 0, 0)
	if len(prov.serverIDs) == 0 || prov.serverIDs[0] != "movie/the-matrix-7" {
		t.Fatalf("provider asked for %v, want movie/the-matrix-7", prov.serverIDs)
	}
}

type twinSearchProvider struct {
	listProvider
}

func (twinSearchProvider) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
	}, nil
}

type twinPlayProvider struct {
	*idRecordingProvider
}

func (twinPlayProvider) Search(string) ([]media.SearchResult, error) {
	return twinSearchProvider{}.Search("")
}

func TestHistoryResumeThroughThePickerKeepsTheSavedIdentity(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	inner := &idRecordingProvider{stubProvider: &stubProvider{
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}}
	saved := media.HistoryEntry{ID: "tv/1", Title: "Some Show", Type: media.TV, Season: 1, Episode: 22}

	if err := resumeViaPicker(twinPlayProvider{inner}, saved, twinSearchResults()); err != nil {
		t.Fatalf("resumeViaPicker = %v", err)
	}
	if len(inner.seasonIDs) == 0 || inner.seasonIDs[0] != "tv/utopia-2013" {
		t.Fatalf("provider asked for %v, want the picked tv/utopia-2013", inner.seasonIDs)
	}
	entries, err := history.Load()
	if err != nil || len(entries) != 1 || entries[0].ID != "tv/1" || entries[0].Season != 1 || entries[0].Episode != 22 {
		t.Fatalf("history = %+v, %v; want one row tv/1 S1E22", entries, err)
	}
}

type mixedTypePlayProvider struct {
	*idRecordingProvider
}

func (mixedTypePlayProvider) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{
		{ID: "movie/utopia-film", Title: "Some Show", Type: media.Movie, Year: "2013"},
		{ID: "tv/utopia-2013", Title: "Some Show", Type: media.TV, Year: "2013"},
		{ID: "tv/utopia-2020", Title: "Some Show", Type: media.TV, Year: "2020"},
	}, nil
}

func TestResumeViaPickerOffersOnlyTheSavedMediaType(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	offered := recordSelections(t, 0)
	inner := &idRecordingProvider{stubProvider: &stubProvider{
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}}
	saved := media.HistoryEntry{ID: "tv/1", Title: "Some Show", Type: media.TV, Season: 1, Episode: 22}

	if err := resumeViaPicker(mixedTypePlayProvider{inner}, saved, mustSearch(t, mixedTypePlayProvider{inner})); err != nil {
		t.Fatalf("resumeViaPicker = %v", err)
	}
	if len(*offered) == 0 || len((*offered)[0]) != 2 {
		t.Fatalf("picker offered %v, want only the two TV works", *offered)
	}
	if len(inner.seasonIDs) == 0 || inner.seasonIDs[0] != "tv/utopia-2013" {
		t.Fatalf("provider asked for %v, want tv/utopia-2013", inner.seasonIDs)
	}
}

type emptySearchPlayProvider struct {
	*idRecordingProvider
}

func (emptySearchPlayProvider) Search(string) ([]media.SearchResult, error) {
	return nil, provider.ErrNoResults
}

func TestResumeViaPickerNeverHandsAFallbackIDToThePrimary(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	offered := recordSelections(t, 0)
	inner := &idRecordingProvider{stubProvider: &stubProvider{}}
	saved := media.HistoryEntry{ID: "tv/1", Title: "Some Show", Type: media.TV, Season: 1, Episode: 22}

	if err := resumeViaPicker(emptySearchPlayProvider{inner}, saved, nil); err != nil {
		t.Fatalf("resumeViaPicker = %v", err)
	}
	if len(inner.seasonIDs) != 0 {
		t.Fatalf("primary was asked for seasons of %v, an ID it never issued", inner.seasonIDs)
	}
	if len(*offered) != 0 {
		t.Fatalf("picker offered %v, want none: only the primary's own rows are offered", *offered)
	}
}

func TestHistorySearchTreatsNoResultsAsEmptyForCatalogueEntriesOnly(t *testing.T) {
	boom := errors.New("network down")
	cases := []struct {
		name    string
		id      string
		err     error
		wantErr error
	}{
		{"catalogue entry, no results", "tv/37854", provider.ErrNoResults, nil},
		{"catalogue entry, other error", "tv/37854", boom, boom},
		{"provider entry, no results", "tv/some-show-1", provider.ErrNoResults, provider.ErrNoResults},
	}
	for _, c := range cases {
		p := &stubProvider{searchErr: c.err}
		_, err := historySearch(p, media.HistoryEntry{ID: c.id, Title: "Some Show"})
		if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
	}
}

func TestEpisodeListFallbackExcludesTheUnwrappedPrimary(t *testing.T) {
	hostileEnv(t)
	rec := withRecordingFallbackChain(t)
	primary := &stubProvider{}
	wrapped := catalog.Wrap(primary, nil, catalog.General)

	_, _ = tuiEpisodeListFallback(wrapped, media.SearchResult{ID: "tv/37854", Title: "Show", Type: media.TV}, 1)

	seen := rec.seen()
	if len(seen) == 0 || seen[0] != provider.Provider(primary) {
		t.Fatalf("fallback chain built for %T, want the unwrapped primary", seen)
	}
}

func TestCatalogueEntryIsDecidedByTheProviderNotTheIDShape(t *testing.T) {
	tmdbShaped := media.HistoryEntry{ID: "movie/550", Title: "Fight Club", Type: media.Movie}
	if !catalogueEntry(&stubProvider{}, tmdbShaped) {
		t.Error("a TMDB-shaped ID under a provider with its own IDs is a catalogue entry")
	}
	if catalogueEntry(provider.NewTBCPL("tbcpl"), tmdbShaped) {
		t.Error("TBCPL mints movie/<n> IDs itself; its history entries are native")
	}
	if catalogueEntry(&stubProvider{}, media.HistoryEntry{ID: "movie/the-matrix-7"}) {
		t.Error("a slug ID is never a catalogue entry")
	}
}

func TestResumeSavedCatalogueEntryAlwaysAsksWhichWork(t *testing.T) {
	url := catalogueHarness(t)
	withFallbackChain(t, newListingChainProvider(url))
	offered := recordSelections(t, 0)
	inner := &idRecordingProvider{stubProvider: &stubProvider{
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}}
	only := []media.SearchResult{{ID: "tv/utopia-2020", Title: "Some Show", Type: media.TV, Year: "2020"}}
	saved := media.HistoryEntry{ID: "tv/1", Title: "Some Show", Type: media.TV, Season: 1, Episode: 22}

	if err := resumeSaved(singleResultPlayProvider{inner, only}, saved, only); err != nil {
		t.Fatalf("resumeSaved = %v", err)
	}
	if len(*offered) != 1 {
		t.Fatalf("picker shown %d times; a lone same-title work must still be confirmed, the entry has no year", len(*offered))
	}
}

type singleResultPlayProvider struct {
	*idRecordingProvider
	rows []media.SearchResult
}

func (p singleResultPlayProvider) Search(string) ([]media.SearchResult, error) { return p.rows, nil }

func twinSearchResults() []media.SearchResult {
	rows, _ := twinSearchProvider{}.Search("")
	return rows
}

func mustSearch(t *testing.T, p provider.Provider) []media.SearchResult {
	t.Helper()
	rows, err := p.Search("")
	if err != nil {
		t.Fatalf("Search = %v", err)
	}
	return rows
}

type nativeTMDBIDProvider struct {
	*stubProvider
}

func (nativeTMDBIDProvider) MintsTMDBShapedIDs() bool { return true }

func TestNativeTMDBShapedHistoryEntryNeverTakesTheCataloguePicker(t *testing.T) {
	var picker, flow int
	prevPicker, prevFlow := resumePicker, resumeFlow
	resumePicker = func(provider.Provider, media.HistoryEntry, []media.SearchResult) error { picker++; return nil }
	resumeFlow = func(provider.Provider, string) error { flow++; return nil }
	t.Cleanup(func() { resumePicker, resumeFlow = prevPicker, prevFlow })

	native := nativeTMDBIDProvider{&stubProvider{searchErr: provider.ErrNoResults}}
	entry := media.HistoryEntry{ID: "movie/550", Title: "Fight Club", Type: media.Movie}

	results, err := historySearch(native, entry)
	if !errors.Is(err, provider.ErrNoResults) {
		t.Fatalf("historySearch err = %v, want ErrNoResults passed through for a native entry", err)
	}
	if err := resumeSaved(native, entry, results); err != nil {
		t.Fatalf("resumeSaved = %v", err)
	}
	if picker != 0 || flow != 1 {
		t.Fatalf("picker=%d flow=%d, want the plain search flow only", picker, flow)
	}

	picker, flow = 0, 0
	plain := &stubProvider{searchErr: provider.ErrNoResults}
	results, err = historySearch(plain, entry)
	if err != nil {
		t.Fatalf("historySearch under a catalogue-shaped provider = %v, want ErrNoResults treated as empty", err)
	}
	if err := resumeSaved(plain, entry, results); err != nil {
		t.Fatalf("resumeSaved = %v", err)
	}
	if picker != 1 || flow != 0 {
		t.Fatalf("picker=%d flow=%d, want the catalogue picker only", picker, flow)
	}
}
