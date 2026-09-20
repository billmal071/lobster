package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A host that answers with something other than the site's search page must
// not be reported as an empty catalog.
//
// This is the inversion behind issue #63. A captive portal, a corporate TLS
// interception page or an ISP block page is served with a 200 and a body of
// HTML, so it clears every check the fetch layer makes; the scraper then finds
// zero result cards in it and used to call that ErrNoResults. cmd's
// gatherSearchResults reads ErrNoResults as "this provider was reached and its
// catalog does not have the title", so with every provider behind the same
// interception page a completely dead network answered `find` with exit 2 —
// "nothing matched, check the spelling" — instead of exit 3, "run lobster
// doctor". That is exactly the inversion the 2/3 split exists to prevent.
//
// The fixture is the input that violates the guarantee: a plausible block page
// rather than an empty one. TestSearchWrapsErrNoResultsOnAnEmptyCatalog
// already covers the genuine empty catalog, and both must keep holding — a
// change that made this test pass by making that one fail would have swapped
// the misreport around rather than fixed it.
func TestScrapersReportABlockPageAsUnreachableNotAsAnEmptyCatalog(t *testing.T) {
	blockPage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Access Denied</title></head>` +
			`<body><h1>This site has been blocked</h1>` +
			`<p>Contact your network administrator.</p></body></html>`))
	}))
	t.Cleanup(blockPage.Close)
	host := strings.TrimPrefix(blockPage.URL, "https://")

	for _, tc := range []struct {
		name string
		p    Provider
	}{
		{"flixhq", &FlixHQ{base: host, client: blockPage.Client()}},
		{"flixhqws", &FlixHQWS{base: host, client: blockPage.Client()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.Search("zzzznotathing")
			if err == nil {
				t.Fatalf("Search on a block page returned no error")
			}
			if errors.Is(err, ErrNoResults) {
				t.Fatalf("Search on a block page = %v, which errors.Is(ErrNoResults); cmd counts that as the provider having been reached, so a dead network reports exit 2 \"check the spelling\"", err)
			}
			if !errors.Is(err, ErrUnrecognisedResponse) {
				t.Fatalf("Search on a block page = %v; want errors.Is(err, ErrUnrecognisedResponse) so the reason is machine-readable rather than a bare message", err)
			}
		})
	}
}
