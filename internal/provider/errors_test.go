package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every provider whose Search signals an empty catalog with an *error* must
// wrap ErrNoResults, or cmd.gatherSearchResults classifies a typo as an
// outage and `find` exits 3 ("run lobster doctor") instead of 2.
//
// This is the test the plumbing in cmd/ cannot provide: those tests construct
// ErrNoResults themselves, so they pass even if not one real provider ever
// produces it. Here each provider is driven, through its own parser, to the
// zero-result branch and the returned error is inspected.
//
// The four providers that return an empty slice with a nil error instead —
// Consumet, YTS, MovieBox and LiveTV — need no sentinel: gatherSearchResults
// already reads err == nil as "reached".
// TestSearchProvidersThatReportEmptyAsNilError pins the two of those that have
// an injectable endpoint.
func TestSearchWrapsErrNoResultsOnAnEmptyCatalog(t *testing.T) {
	// An HTML page with no result cards: what every scraper provider gets back
	// for a query its site does not index. TLS, and the provider is built by
	// hand with ts.Client() and a scheme-less base — the same shape as
	// TestFlixHQWSGetServersIntegration. The constructors cannot be used here:
	// BaseURL() prepends "https://" itself and httputil rejects anything else,
	// so a plain httptest.NewServer URL turns into "https://http//127.0.0.1:..."
	// and the test performs a real DNS lookup for the host "http".
	emptyHTML := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div class="film_list-wrap"></div></body></html>`))
	}))
	t.Cleanup(emptyHTML.Close)
	htmlHost := strings.TrimPrefix(emptyHTML.URL, "https://")

	// TMDB's multi-search answering with an empty result set.
	emptyTMDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(emptyTMDB.Close)

	oldTMDB := tmdbBaseURL
	tmdbBaseURL = emptyTMDB.URL
	t.Cleanup(func() { tmdbBaseURL = oldTMDB })

	tbcpl := NewTBCPL("tbcpl")
	tbcpl.tmdbBaseURL = emptyTMDB.URL

	// AniPub answers a miss with a bare `{"found":false}` object; AllAnime with
	// an empty GraphQL edge list. fakeDoer's default response covers both.
	anipub := NewAniPub()
	anipub.client = fakeDoer{routes: map[string]string{"/api/search/": `{"found":false}`}}

	allanime := NewAllAnime(false)
	allanime.client = fakeDoer{routes: map[string]string{
		"shows": `{"data":{"shows":{"edges":[]}}}`,
	}}

	cases := []struct {
		name string
		p    Provider
	}{
		{"flixhq", &FlixHQ{base: htmlHost, client: emptyHTML.Client()}},
		{"flixhqws", &FlixHQWS{base: htmlHost, client: emptyHTML.Client()}},
		{"kimcartoon", &KimCartoon{base: htmlHost, client: emptyHTML.Client()}},
		{"soap2day", NewSoap2Day()},
		{"vidnest", NewVidNest()},
		{"vaplayer", NewVaPlayer()},
		{"tbcpl", tbcpl},
		{"anipub", anipub},
		{"allanime", allanime},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := tc.p.Search("zzzznotathing")
			if err == nil {
				// Not a failure of this package's contract — a provider may
				// legitimately report empty as (nil, nil) — but it means this
				// row is in the wrong table and proves nothing.
				t.Fatalf("Search returned (%v, nil); this table is for providers that error on empty", results)
			}
			if !errors.Is(err, ErrNoResults) {
				t.Fatalf("Search error = %v; want errors.Is(err, ErrNoResults) so cmd/find can exit 2 rather than 3", err)
			}
			// The query has to survive the wrap: the interactive path prints
			// this error verbatim.
			if !strings.Contains(err.Error(), "zzzznotathing") {
				t.Fatalf("error %q does not name the query", err)
			}
		})
	}
}

// AniPub's title-variant search is a second, independent zero-result site on
// the same provider, reached by ResolveByTitle rather than Search.
func TestAniPubSearchVariantsWrapsErrNoResults(t *testing.T) {
	p := NewAniPub()
	p.client = fakeDoer{routes: map[string]string{"/api/search/": `{"found":false}`}}

	_, err := p.searchVariants("zzzznotathing")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("searchVariants error = %v, want errors.Is(err, ErrNoResults)", err)
	}
}

// The other half of the contract. These providers report an empty catalog as
// an empty slice and a nil error, which gatherSearchResults already reads as
// "provider reached". If one of them ever starts erroring instead it must join
// the table above, so assert the current shape rather than leaving it implied.
//
// Consumet and YTS are covered here because both take an injectable endpoint.
// MovieBox (moviebox.go, end of Search) and LiveTV (livetv.go, end of Search)
// have the same shape but reach a hardcoded host with no seam, so they are
// verified by inspection only — do not read this test as covering them.
func TestSearchProvidersThatReportEmptyAsNilError(t *testing.T) {
	emptyJSON := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(emptyJSON.Close)

	consumet := &Consumet{baseURL: emptyJSON.URL, client: emptyJSON.Client()}
	results, err := consumet.Search("zzzznotathing")
	if err != nil {
		t.Fatalf("Consumet.Search on an empty catalog = %v; if it now errors it must wrap ErrNoResults", err)
	}
	if len(results) != 0 {
		t.Fatalf("Consumet.Search = %+v, want no results", results)
	}

	// YTS answers a miss with movie_count 0 and a null movies array.
	emptyYTS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","data":{"movie_count":0,"movies":null}}`))
	}))
	t.Cleanup(emptyYTS.Close)

	oldYTS := ytsBase
	ytsBase = emptyYTS.URL
	t.Cleanup(func() { ytsBase = oldYTS })

	yts := &YTS{client: emptyYTS.Client()}
	results, err = yts.Search("zzzznotathing")
	if err != nil {
		t.Fatalf("YTS.Search on an empty catalog = %v; if it now errors it must wrap ErrNoResults", err)
	}
	if len(results) != 0 {
		t.Fatalf("YTS.Search = %+v, want no results", results)
	}
}
