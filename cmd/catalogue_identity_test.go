package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
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

func TestResumeRowResolvesACatalogueHistoryEntryForTheProvider(t *testing.T) {
	p := &tvSearchProvider{}
	got, ok := resumeRow(p, media.HistoryEntry{ID: "tv/37854", Title: "One Piece", Type: media.TV})
	if !ok || got.ID != "tv/37854" || got.ProviderID != "tv/one-piece-100" {
		t.Fatalf("resumeRow = %+v, %v", got, ok)
	}
	if _, ok := resumeRow(p, media.HistoryEntry{ID: "tv/other-1", Title: "One Piece", Type: media.TV}); ok {
		t.Fatal("a non-catalogue entry the search does not list must not resolve")
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

func TestResumeRowLeavesTwoSameTitleWorksToTheUser(t *testing.T) {
	if _, ok := resumeRow(&twinSearchProvider{}, media.HistoryEntry{ID: "tv/1", Title: "Utopia", Type: media.TV}); ok {
		t.Fatal("resumeRow picked one of two same-title works the history entry cannot tell apart")
	}
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

	if err := resumeViaPicker(twinPlayProvider{inner}, saved); err != nil {
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
