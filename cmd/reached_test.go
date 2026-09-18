package cmd

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/provider"
)

// `reached` has to mean "this provider completed a real exchange with its
// host", not "this provider returned without a transport error we happened to
// classify". The distinction is the whole of the exit 2 / exit 3 split:
//
//	exit 2 — every provider answered, none indexes the title. Fix the spelling.
//	exit 3 — nothing answered. Run `lobster doctor`.
//
// A captive portal or an intercepted TLS session hands every scraper the same
// 200-with-HTML interception page, which parses to zero result cards. While
// that was reported as provider.ErrNoResults, a completely dead network voted
// "reached" eleven times over and `find` told the user to check their
// spelling. Both rows below are asserted together on purpose: a fix that made
// the unreachable row pass by making the typo row fail would only have
// inverted the misreport.
func TestGatherSearchResultsDoesNotCountAnUnrecognisedResponseAsReached(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{
			name: "every provider handed back a block page",
			err:  fmt.Errorf("%w: searching for %q on example.invalid", provider.ErrUnrecognisedResponse, "the matrix"),
			want: errProvidersFailed,
		},
		{
			name: "every provider answered, none indexes the title",
			err:  fmt.Errorf("%w for %q", provider.ErrNoResults, "the matrix"),
			want: errNoResults,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := &stubProvider{searchErr: tc.err}
			fallbacks := []provider.Provider{
				&stubProvider{searchErr: tc.err},
				&stubProvider{searchErr: tc.err},
			}

			_, err := gatherSearchResults(primary, fallbacks, "the matrix")
			if err == nil {
				t.Fatalf("gatherSearchResults returned no error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("gatherSearchResults = %v, want errors.Is(err, %v); find turns this into the exit code that decides between \"check the spelling\" and \"run lobster doctor\"", err, tc.want)
			}
		})
	}
}
