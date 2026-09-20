package provider

import "errors"

// ErrNoResults reports that a provider was reached and answered, but its
// catalog has nothing for the query.
//
// Most providers signal an empty search by returning an error rather than an
// empty slice, which makes "this title does not exist" indistinguishable from
// "this provider is down" to a caller that only sees `err != nil`. That
// ambiguity is not cosmetic: cmd/find.go turns it into an exit code, and
// exit 3 ("every source is down, run lobster doctor") on what is usually a
// typo sends an agent to diagnose provider health over a misspelling.
//
// Wrap this with %w at every such site so callers can use errors.Is instead of
// matching the message text.
var ErrNoResults = errors.New("no results found")

// ErrUnrecognisedResponse reports that a host answered, but not with the page
// this provider knows how to read — so the answer is evidence about the
// *connection*, not about the catalog.
//
// It exists because the two are not the same and were being conflated. A
// scraper parses a page it did not recognise into zero result cards, and zero
// cards used to be reported as ErrNoResults, which cmd/multisearch.go reads as
// "this provider was reached and its catalog does not have the title". Under a
// captive portal or an intercepted TLS session every scraper is handed the
// same interception page, so a completely broken network answered `find` with
// exit 2 — "nothing matched, check the spelling" — which is the exact
// inversion the 2/3 split exists to prevent.
//
// It deliberately does not wrap ErrNoResults: a caller asking "was this
// provider reached?" must get no for this, and errors.Is would otherwise say
// yes.
var ErrUnrecognisedResponse = errors.New("response was not a recognisable search page")
