package cmd

import (
	"net/http"
	"testing"

	"lobster/internal/dlmanager"
)

func yearlessCatalogueDownload() dlmanager.ResolveRequest {
	return dlmanager.ResolveRequest{MediaID: "tv/37854", Title: "Some Show", MediaType: "tv", Season: 1, Episode: 22, FromCatalogue: true}
}

func TestQueuedYearlessCatalogueDownloadNeverResolvesASameTitledSeriesFromAnotherYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2020"
	withFallbackChain(t, fb)

	if res, err := makeStreamResolver(&stubProvider{})(yearlessCatalogueDownload()); err == nil {
		t.Fatalf("result = %+v; the chain only has the 2020 series", res)
	}
	if asked := fb.mediaAsked(); asked != "" {
		t.Fatalf("the 2020 series %s was asked for a stream", asked)
	}
}

func TestQueuedYearlessCatalogueDownloadResolvesTheChainRowFromTheCatalogueYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusOK, `{"id":37854,"first_air_date":"2013-04-01"}`)
	fb := newListingChainProvider(url)
	fb.results[0].Year = "2013"
	withFallbackChain(t, fb)

	if _, err := makeStreamResolver(&stubProvider{})(yearlessCatalogueDownload()); err != nil {
		t.Fatalf("makeStreamResolver = %v", err)
	}
	if asked := fb.mediaAsked(); asked != "tv/fallback-1" {
		t.Fatalf("stream asked of %q, want the 2013 series tv/fallback-1", asked)
	}
}

func TestQueuedYearlessCatalogueDownloadIsRefusedWhenTheCatalogueCannotNameItsYear(t *testing.T) {
	url := catalogueHarness(t)
	tmdbDetails(t, http.StatusInternalServerError, `{}`)
	fb := newListingChainProvider(url)
	withFallbackChain(t, fb)

	if res, err := makeStreamResolver(&stubProvider{})(yearlessCatalogueDownload()); err == nil {
		t.Fatalf("result = %+v; nothing confirmed which Some Show was queued", res)
	}
	if asked := fb.mediaAsked(); asked != "" {
		t.Fatalf("%s was asked for a stream on a title match alone", asked)
	}
}

func TestQueuedYearlessDownloadFromBeforeTheCatalogueFlagStillResolvesByTitle(t *testing.T) {
	url := catalogueHarness(t)
	calls := tmdbDetails(t, http.StatusInternalServerError, `{}`)
	fb := newListingChainProvider(url)
	withFallbackChain(t, fb)
	req := yearlessCatalogueDownload()
	req.FromCatalogue = false

	if _, err := makeStreamResolver(&stubProvider{})(req); err != nil {
		t.Fatalf("makeStreamResolver = %v", err)
	}
	if *calls != 0 {
		t.Fatalf("TMDB asked %d times for a row the catalogue did not produce", *calls)
	}
}
