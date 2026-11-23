package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/catalog"
	"lobster/internal/config"
	"lobster/internal/history"
	"lobster/internal/media"
)

func tmdbDetails(t *testing.T, status int, body string) *int {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		calls++
		rw.WriteHeader(status)
		io.WriteString(rw, body)
	}))
	t.Cleanup(srv.Close)
	prevCfg, prevNew := cfg, newCatalogClient
	t.Cleanup(func() { cfg, newCatalogClient = prevCfg, prevNew })
	c := config.Default()
	if cfg != nil {
		cp := *cfg
		c = &cp
	}
	c.TMDBAPIKey = "k"
	cfg = c
	newCatalogClient = func(k string) *catalog.Client { return catalog.NewWithBase(k, srv.URL, srv.Client()) }
	return &calls
}

func yearlessResume() media.HistoryEntry {
	return media.HistoryEntry{ID: "tv/37854", Title: "Some Show", Type: media.TV, Season: 1, Episode: 22}
}

func TestResumeOfAYearlessCatalogueEntryNeverPlaysASameTitledSeriesFromAnotherYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2020"
	withFallbackChain(t, fb)
	inner := &idRecordingProvider{stubProvider: &stubProvider{}}

	if err := resumeViaPicker(emptySearchPlayProvider{inner}, yearlessResume(), nil); err == nil {
		t.Fatal("resumeViaPicker = nil; the chain only has the 2020 series")
	}
	if asked := fb.mediaAsked(); asked != "" {
		t.Fatalf("the 2020 series %s was asked for a stream", asked)
	}
}

func TestResumeOfAYearlessCatalogueEntryPlaysTheChainRowFromTheCatalogueYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2013"
	withFallbackChain(t, fb)
	inner := &idRecordingProvider{stubProvider: &stubProvider{}}

	if err := resumeViaPicker(emptySearchPlayProvider{inner}, yearlessResume(), nil); err != nil {
		t.Fatalf("resumeViaPicker = %v", err)
	}
	entries, err := history.Load()
	if err != nil || len(entries) != 1 || entries[0].ID != "tv/37854" {
		t.Fatalf("history = %+v, %v; want one row under tv/37854", entries, err)
	}
}

func TestYearlessCatalogueRowIsRefusedWhenTheCatalogueCannotNameItsYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusInternalServerError, `{}`)
	fb := newListingChainProvider(url)
	withFallbackChain(t, fb)
	inner := &idRecordingProvider{stubProvider: &stubProvider{}}

	if err := resumeViaPicker(emptySearchPlayProvider{inner}, yearlessResume(), nil); err == nil {
		t.Fatal("resumeViaPicker = nil; nothing confirmed which Some Show was saved")
	}
	if asked := fb.mediaAsked(); asked != "" {
		t.Fatalf("%s was asked for a stream on a title match alone", asked)
	}
}

func TestYearlessCatalogueRowDropsAProviderIDMatchedOnTitleAlone(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2020"
	withFallbackChain(t, fb)
	prov := &idRecordingProvider{stubProvider: &stubProvider{
		results: []media.SearchResult{{ID: "tv/remake-2020", Title: "Some Show", Year: "2020", Type: media.TV}},
		seasons: []media.Season{{ID: "s1", Number: 1}},
	}}
	sel := media.SearchResult{ID: "tv/37854", ProviderID: "tv/remake-2020", FromCatalogue: true, Title: "Some Show", Type: media.TV}

	if err := resolveAndPlay(prov, sel, 1, 22); err == nil {
		t.Fatal("resolveAndPlay = nil; only the 2020 series is on offer")
	}
	if len(prov.seasonIDs) != 0 {
		t.Fatalf("provider was asked for %v, a row matched on title alone", prov.seasonIDs)
	}
}

func TestDownloadDialogNeverListsASameTitledSeriesFromAnotherYearForAYearlessCatalogueRow(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2020"
	withFallbackChain(t, fb)
	item := media.SearchResult{ID: "tv/37854", FromCatalogue: true, Unresolved: true, Title: "Some Show", Type: media.TV}

	if eps, err := tuiEpisodeListFallback(&stubProvider{}, item, 1); err == nil {
		t.Fatalf("tuiEpisodeListFallback = %d episodes of the 2020 series", len(eps))
	}
}
