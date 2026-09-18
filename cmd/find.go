package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/config"
	"lobster/internal/media"
)

var (
	flagFindType  string
	flagFindLimit int
)

// agentSearch is the search entry point, as a package var so tests can supply
// fixed results instead of reaching the network.
var agentSearch = gatherSearchResults

// agentProvider builds the primary provider. A package var so tests can supply
// a stub instead of one that reaches the network.
var agentProvider = newProvider

// parseFindType resolves --type. It reports whether filtering applies at all
// (an empty --type means "no filter") and rejects anything else.
//
// Comparison is case-insensitive: "TV" and "Movie" are what a human — or an
// agent echoing the user — naturally writes, and case is not a mistake worth
// failing on. An unrecognised word is: the old code treated every value that
// was not exactly "tv" as "movie", so `--type series` silently returned only
// films and reported success, which the caller has no way to notice.
func parseFindType(s string) (want media.MediaType, filter bool, err error) {
	switch strings.ToLower(s) {
	case "":
		return media.Movie, false, nil
	case "movie":
		return media.Movie, true, nil
	case "tv":
		return media.TV, true, nil
	default:
		return media.Movie, false, emitErr("usage", exitUsage,
			"unrecognised --type %q (valid: movie, tv; case-insensitive)", s)
	}
}

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search for a movie or TV show and print JSON (no prompts)",
	Long: `Search and print matching titles as JSON on stdout.

Unlike the interactive commands, find never opens fzf and never waits for
input, so it is safe to call from a script or an agent. Each result carries an
opaque "ref" which is the handle to pass to "lobster play --ref".`,
	Args: cobra.MinimumNArgs(1),
	RunE: findRun,
}

func findRun(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	// Validated before the search so an unusable filter costs no network
	// round trip, and so the error is unambiguous rather than arriving as an
	// empty result set.
	want, filter, err := parseFindType(flagFindType)
	if err != nil {
		return err
	}

	p := agentProvider()
	results, err := agentSearch(p, fallbackSearchProviders(p), query)
	if err != nil {
		// gatherSearchResults reports "nothing matched" as an error, not as an
		// empty slice, so without this branch every typo exited 3 and sent the
		// agent to `lobster doctor` over a misspelling. errors.Is, not a
		// message match: the text is a display string and may be reworded.
		if errors.Is(err, errNoResults) {
			return emitErr("no_results", exitNoResults, "nothing matched %q", query)
		}
		return emitErr("providers_failed", exitProvidersFailed, "search failed: %v", err)
	}

	if filter {
		filtered := results[:0:0]
		for _, r := range results {
			if r.Type == want {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Reachable only via --type now: a search that found nothing arrives as
	// errNoResults above, never as an empty slice.
	if len(results) == 0 {
		return emitErr("no_results", exitNoResults, "nothing matched %q", query)
	}

	if flagFindLimit > 0 && len(results) > flagFindLimit {
		results = results[:flagFindLimit]
	}

	// Each row is stamped with the provider that actually returned it, not
	// with the base that was asked for. gatherSearchResults broadens past the
	// primary whenever it errors or answers thinly (cmd/multisearch.go), so a
	// uniform cfg.Base stamp put a fallback provider's ID under the primary's
	// name — and a row falsely named yts moves a later play onto the torrent
	// path (mayStreamTorrent, cmd/root.go) for a film YTS never returned.
	configured := ""
	if cfg != nil {
		configured = config.NormalizeBase(cfg.Base)
	}
	primaryBase := searchProviderBase(p)

	out := make([]map[string]any, 0, len(results))
	for i, r := range results {
		ref, err := encodeRef(playRef{
			ID:    r.ID,
			Title: r.Title,
			Year:  r.Year,
			Type:  r.Type.String(),
			Base:  refBaseFor(r, primaryBase, configured),
		})
		if err != nil {
			return emitErr("internal", 1, "encoding ref: %v", err)
		}
		out = append(out, map[string]any{
			"idx":   i,
			"ref":   ref,
			"title": r.Title,
			"year":  r.Year,
			"type":  r.Type.String(),
		})
	}
	payload := map[string]any{"results": out}
	if w := baseNotUsedWarnings(configured, primaryBase, results); len(w) > 0 {
		payload["warnings"] = w
	}
	return emitJSON(payload)
}

// baseNotUsedWarnings reports, machine-readably, that a base was asked for and
// none of the results came from it.
//
// find always searches the fallback chain as well as the requested base
// (gatherSearchResults), and until now it announced the broadening only
// through debugf and a spinner that ui.spinnerVisible suppresses on a
// non-TTY — so an agent or a script, which is what find exists for, saw
// nothing at all. It then had no way to tell "yts has this film" from "yts
// answered nothing and eight scrapers did", and the two call for different
// next steps.
//
// It is additive and absent when there is nothing to say, so a consumer that
// does not know the key is unaffected: schema 1 is documented in README.md and
// skills/lobster-play/SKILL.md as a marker for the shape of what *is* there
// ("check schema before trusting the shape of the rest"), never as a closed
// set of fields.
//
// The test is per row, against the emitted rows, so `--type` and `--limit` are
// accounted for: the question is whether the base produced anything the caller
// actually received, not whether it produced anything at all.
func baseNotUsedWarnings(configured, primaryBase string, results []media.SearchResult) []map[string]any {
	// No base was asked for (or "auto" was, which is the absence of a
	// preference), or no base value names the primary at all — with api_url
	// set, base does not select the provider in the first place.
	if configured == "" || configured == config.BaseAuto || primaryBase == "" {
		return nil
	}
	for _, r := range results {
		if r.Provider == primaryBase {
			return nil
		}
	}
	return []map[string]any{{
		"code": "base_not_used",
		"base": configured,
		"message": fmt.Sprintf(
			"no result came from base %q; every result below came from a fallback provider, and each ref names the source that produced it",
			configured),
	}}
}

// refBaseFor returns the base token to stamp on one row's ref: the token that
// selects the provider which produced the row, or "" when no base value
// selects it (applyRefBase treats an empty base as "leave the configuration
// alone", cmd/play.go).
//
// The one refinement over row.Provider is spelling. A base is matched by
// substring (newProvider, cmd/provider.go), so a user who configured a
// specific mirror — "flixhq.xx" — gets a primary built from that domain, and
// collapsing their row to the generic "flixhq" would send a later play to the
// default domain instead. When the row came from the primary *and* the
// configured value still names that provider, the caller's own spelling is
// both honest and more precise, so it is preserved.
//
// Both halves of that condition are load-bearing:
//
//   - "came from the primary" — "flixhq.ws" contains "flixhq", so without it a
//     row that FlixHQ supplied as a fallback under a FlixHQWS primary would be
//     restamped as FlixHQWS's.
//   - "still names that provider" — newProvider has no unknown-base arm and
//     falls through to MovieBox, so `--base nonesuch` yields MovieBox rows.
//     Echoing "nonesuch" back would name a source that does not exist;
//     "moviebox" is what actually answered. config.BaseAuto is excluded by the
//     same test, which is what it deserves: "auto" names no provider, it
//     licenses routing movies to YTS and opening a magnet (cmd/typeroute.go,
//     cmd/root.go).
//
// The cost of the second test is a base whose spelling does not contain its
// provider's token — "1shows.org" selects TBCPL — which is collapsed to
// "tbcpl" and so replays against TBCPL's default site rather than that one.
// It still names the source that answered, which is the property being bought;
// echoing back a string that may name nothing is what it is being bought with.
func refBaseFor(r media.SearchResult, primaryBase, configured string) string {
	if r.Provider == "" {
		return ""
	}
	if r.Provider == primaryBase && strings.Contains(configured, r.Provider) {
		return configured
	}
	return r.Provider
}

func init() {
	markAgentCommand(findCmd)
	findCmd.Flags().StringVar(&flagFindType, "type", "", "Filter results: movie | tv (case-insensitive)")
	findCmd.Flags().IntVar(&flagFindLimit, "limit", 0, "Maximum results to print (0 = no limit)")
}
