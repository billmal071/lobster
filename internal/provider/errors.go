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

// ErrIncompleteEpisodeList reports that an episode list is everything the
// provider could measure, and that the provider knows it may be short.
//
// It is returned *alongside a usable list*, which is the point: a provider
// that enumerates by probing has two different failures, and only one of them
// should cost the caller the list it already has. A probe that cannot reach
// the host has nothing to offer. A probe that ran out of budget, hit its own
// ceiling, or could not confirm where the series ends has a measured prefix —
// and reporting that prefix as the whole series is the fabricated-episode-list
// problem of #61 wearing different clothes. Nothing is invented, but the
// caller is told a complete list when it has a partial one, and it cannot see
// the difference.
//
// So the contract is: a non-nil list plus an error wrapping this sentinel
// means "here is what was measured, do not treat it as the end". cmd/episodes
// keeps the list and adds an episode_list_incomplete warning to its JSON.
//
// It deliberately does not wrap ErrNoResults: the catalogue does have this
// show, and a caller asking "does this source have it?" must get yes.
var ErrIncompleteEpisodeList = errors.New("episode list may be incomplete")
