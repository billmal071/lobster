package cmd

import (
	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

var (
	flagRef     string
	flagSeason  int
	flagEpisode int
)

var episodesCmd = &cobra.Command{
	Use:   "episodes --ref <REF>",
	Short: "List seasons and episodes for a TV ref as JSON (no prompts)",
	Long: `List the episodes of a show without prompting.

Takes a ref emitted by "lobster find". With --season it lists that season's
episodes; without it, the first season's.`,
	Args: cobra.NoArgs,
	RunE: episodesRun,
}

func init() {
	markAgentCommand(episodesCmd)
	episodesCmd.Flags().StringVar(&flagRef, "ref", "", "Ref from lobster find (required)")
	episodesCmd.Flags().IntVar(&flagSeason, "season", 0, "Season number (default: first)")
}

func episodesRun(cmd *cobra.Command, args []string) error {
	r, err := decodeRef(flagRef)
	if err != nil {
		return emitErr("bad_ref", 1, "%v", err)
	}
	if r.Type != media.TV.String() {
		return emitErr("not_a_series", 1, "%q is a %s, which has no episodes", r.Title, r.Type)
	}

	applyRefBase(cmd, r)

	primary := agentProvider()
	p, id, seasons, primaryErr := seasonSource(primary, r)
	if len(seasons) == 0 {
		if primaryErr != nil {
			return emitErr("providers_failed", exitProvidersFailed, "getting seasons: %v", primaryErr)
		}
		return emitErr("no_results", exitNoResults, "no seasons found for %q", r.Title)
	}

	sel := seasons[0]
	if flagSeason > 0 {
		found := false
		for _, s := range seasons {
			if s.Number == flagSeason {
				sel, found = s, true
				break
			}
		}
		if !found {
			return emitErr("no_results", exitNoResults, "season %d not found for %q", flagSeason, r.Title)
		}
	}

	eps, err := p.GetEpisodes(id, sel.ID)
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "getting episodes: %v", err)
	}

	seasonNums := make([]int, 0, len(seasons))
	for _, s := range seasons {
		seasonNums = append(seasonNums, s.Number)
	}
	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		out = append(out, map[string]any{"number": e.Number, "title": e.Title})
	}

	return emitJSON(map[string]any{
		"title":    r.Title,
		"seasons":  seasonNums,
		"season":   sel.Number,
		"episodes": out,
	})
}

// agentFallbackProviders is the fallback chain, as a package var so tests can
// supply stubs instead of providers that reach the network.
var agentFallbackProviders = fallbackProviders

// seasonSource returns the provider that can actually enumerate this ref's
// seasons, the ID to ask it about, that season list, and the primary's error
// if it had one.
//
// The primary is asked first and almost always answers. It fails for one
// specific, non-rare reason: a ref's ID need not belong to the primary at all.
// find searches the primary *and* the fallback chain (gatherSearchResults
// broadens whenever the primary returns fewer than three results) while
// stamping the configured base on every ref it prints — playRef.Base is a
// starting point, not an attribution (cmd/ref.go). A ref whose ID came from a
// fallback provider therefore reaches the primary as a foreign ID, and the
// primary answers "no seasons" or errors.
//
// play already survives that: resolveAndPlay has a branch for exactly this
// condition (cmd/search.go, `if err != nil || len(seasons) == 0`) that
// re-searches by title across the whole chain. episodes did not, so `find` →
// `episodes --ref` reported "no seasons found" for a show `play --ref` then
// played without complaint. The two commands must agree on what one ref means.
//
// The re-search is by title and ranked by resolver.Candidates, so the ref's
// ID/Title/Year all count — the same ranking the resolver uses, and the reason
// a ref carries more than an ID. A fallback is only accepted when it returns a
// non-empty season list; an empty one means it does not really have this show.
func seasonSource(primary provider.Provider, r playRef) (provider.Provider, string, []media.Season, error) {
	seasons, err := primary.GetSeasons(r.ID)
	if err == nil && len(seasons) > 0 {
		return primary, r.ID, seasons, nil
	}
	debugf("episodes: primary could not enumerate %q (err=%v, seasons=%d); re-searching the fallback chain by title", r.ID, err, len(seasons))

	req := resolver.Request{
		ID:        r.ID,
		Title:     r.Title,
		Year:      r.Year,
		MediaType: media.TV,
	}
	for _, fb := range agentFallbackProviders(primary) {
		results, searchErr := fb.Search(r.Title)
		if searchErr != nil {
			debugf("episodes: fallback %T search failed: %v", fb, searchErr)
			continue
		}
		for _, c := range resolver.Candidates(results, req) {
			fbSeasons, seasonsErr := fb.GetSeasons(c.ID)
			if seasonsErr != nil || len(fbSeasons) == 0 {
				continue
			}
			debugf("episodes: %T answers for %q (ID %s)", fb, c.Title, c.ID)
			return fb, c.ID, fbSeasons, nil
		}
	}
	return primary, r.ID, nil, err
}
