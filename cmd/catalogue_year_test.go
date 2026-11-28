package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestYearlessCatalogueRowLeftUnresolvedIsAskedOfThePrimaryOnceTheCatalogueNamesItsYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	withFallbackChain(t, newListingChainProvider(url))
	prov := &idRecordingProvider{stubProvider: &stubProvider{
		results:          []media.SearchResult{{ID: "tv/original-2013", Title: "Some Show", Year: "2013", Type: media.TV}},
		seasons:          []media.Season{{ID: "s1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{"s1": twentyTwoEpisodes()},
	}}
	sel := resolveCatalogueRow(prov, media.SearchResult{ID: "tv/37854", FromCatalogue: true, Title: "Some Show", Type: media.TV})
	if !sel.Unresolved {
		t.Fatalf("setup: a yearless row should start Unresolved, got %+v", sel)
	}

	if err := resolveAndPlay(prov, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if len(prov.seasonIDs) == 0 || prov.seasonIDs[0] != "tv/original-2013" {
		t.Fatalf("primary was asked for %v; want tv/original-2013", prov.seasonIDs)
	}
}

func TestARowTheTUIResolvedIsNotDatedAgainBeforePlayback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		io.WriteString(rw, `{"id":37854,"first_air_date":"2013-04-01"}`)
	}))
	t.Cleanup(srv.Close)
	inner := &stubProvider{results: []media.SearchResult{
		{ID: "tv/some-show-2013", Title: "Some Show", Type: media.TV, Year: "2013"},
		{ID: "tv/some-show-2020", Title: "Some Show", Type: media.TV, Year: "2020"},
	}}
	w := catalog.Wrap(inner, catalog.NewWithBase("k", srv.URL, srv.Client()), catalog.General)
	row := w.ResolveRow(media.SearchResult{ID: "tv/37854", Title: "Some Show", Type: media.TV, FromCatalogue: true})

	calls := tmdbDetails(t, http.StatusInternalServerError, `{}`)
	got, err := identifyCatalogueRow(w, row)
	if err != nil || got.ProviderID != "tv/some-show-2013" {
		t.Fatalf("identifyCatalogueRow = %+v, %v; want tv/some-show-2013", got, err)
	}
	if *calls != 0 {
		t.Fatalf("TMDB details asked %d times for a row the TUI already dated", *calls)
	}
}

func TestDownloadDialogFallbackUsesTheYearTheTUIAlreadyRecoveredWhenTMDBLaterFails(t *testing.T) {
	url := catalogueHarness(t)
	details := 0
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/trending/") {
			io.WriteString(rw, `{"results":[{"id":37854,"name":"Some Show"}]}`)
			return
		}
		details++
		if details > 1 {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(rw, `{"id":37854,"first_air_date":"2013-04-01"}`)
	}))
	t.Cleanup(srv.Close)
	tmdbDetails(t, http.StatusInternalServerError, `{}`)
	inner := &stubProvider{
		results: []media.SearchResult{{ID: "tv/some-show-2013", Title: "Some Show", Type: media.TV, Year: "2013"}},
		seasons: []media.Season{{ID: "s1", Number: 1}},
	}
	w := catalog.Wrap(inner, catalog.NewWithBase("k", srv.URL, srv.Client()), catalog.General)
	rows, err := w.Trending(media.TV)
	if err != nil || len(rows) != 1 || rows[0].Year != "" {
		t.Fatalf("setup: Trending = %+v, %v; want one yearless row", rows, err)
	}
	if _, err := w.GetSeasons(rows[0].ID); err != nil {
		t.Fatalf("setup: GetSeasons = %v", err)
	}
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2013"
	withFallbackChain(t, fb)

	eps, err := tuiEpisodeListFallback(w, rows[0], 1)
	if err != nil || len(eps) != 22 {
		t.Fatalf("tuiEpisodeListFallback = %d episodes, %v; want the 2013 series' 22", len(eps), err)
	}
}
