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
