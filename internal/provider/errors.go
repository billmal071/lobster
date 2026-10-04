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

// ErrUnconfirmedEpisodeList narrows ErrIncompleteEpisodeList to the one case
// where the list is short only if a single observation was a lie.
//
// It is always returned *wrapped alongside* ErrIncompleteEpisodeList, never
// instead of it, so every caller that only asks the broad question — "may this
// list be short?" — is unaffected. internal/provider/episodeid.go and
// cmd/episodes.go both ask it that way and both keep working unchanged.
//
// The distinction it draws is between two states that were reported under one
// code, and the measured reason it has to exist is that one of them is the
// normal steady state of a probing enumeration while the other is the bug:
//
//   - A probe that never located the end of the series — every episode it
//     asked about answered present, or the wave that would have found the end
//     was shed, or the budget ran out before the end was bracketed — is
//     reporting a count that comes from its own probe schedule rather than
//     from the catalogue. That list is missing episodes, and it is the plain
//     ErrIncompleteEpisodeList case.
//   - A probe that *did* locate the end — episode n present, episode n+1
//     answered absent — and only failed to re-confirm that absence on a
//     request of its own is this case. The list is complete unless the one
//     404 behind it was a lie.
//
// Both are honest and they are not interchangeable: a caller that cannot tell
// them apart sees a warning on most healthy runs, which is how a warning stops
// being read. Nine live `episodes` runs against a 12-episode series returned
// the right 12 eight times and warned six times — five of those on a correct
// list, under this case.
//
// It is still a warning rather than silence, and that is deliberate. The lie
// this case cannot rule out is exactly the one that produced the reported bug:
// a 404 answered inside a concurrent wave for a manifest the CDN then served
// ended a 12-episode series at 10. The shortfall it hides lands on the newest
// episodes, which are the ones a caller is most likely to want. So it is
// reported, under its own code, with words that say the list is probably whole.
var ErrUnconfirmedEpisodeList = errors.New("the end of the episode list was not confirmed")
