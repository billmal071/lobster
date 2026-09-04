package cmd

import (
	"context"
	"sync"

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

// episodesFallbackTimeout bounds the whole fallback season scan. It matches
// multiSearchTimeout, which bounds find's fan-out over the same chain — the two
// commands wait on the same providers and should give up at the same point.
// A var, not a const, so tests can shrink it and still be deterministic.
var episodesFallbackTimeout = multiSearchTimeout

// seasonSource returns the provider that can actually enumerate this ref's
// seasons, the ID to ask it about, that season list, and the primary's error
// if it had one.
//
// The primary is asked first and almost always answers. It fails for one
// specific, non-rare reason: a ref's ID need not belong to the primary at all.
// find searches the primary *and* the fallback chain (gatherSearchResults
// broadens whenever the primary returns fewer than three results) while
// stamping the configured base on every ref it prints — playRef.Base is a
// config-time choice of primary, a starting point rather than an attribution,
// and cmd/ref.go explains why no honest per-row one exists. A ref whose ID came from a
// fallback provider therefore reaches the primary as a foreign ID, and the
// primary answers "no seasons" or errors.
//
// play already survives that: resolveAndPlay has a branch for exactly this
// condition (cmd/search.go, `if err != nil || len(seasons) == 0`) that
// re-searches by title across the whole chain. episodes did not, so `find` →
// `episodes --ref` reported "no seasons found" for a show `play --ref` then
// played without complaint. The two commands must agree on what one ref means.
//
// The re-search is by title, ranked by resolver.Candidates so the ref's
// ID/Title/Year all count — the reason a ref carries more than an ID — and then
// admitted only if resolver.Matches says the candidate is the same work.
// Ranking alone is not a gate: candidatesFor applies no score threshold, so
// without Matches the first fallback that returns any seasons for any show
// wins, and the envelope echoes the ref's own title over it. The caller could
// not detect the swap. A candidate that matches but returns an empty season
// list is skipped too — it does not really have this show.
//
// The chain is scanned in parallel under a deadline, the same shape find uses
// (multiProviderSearch), because a serial scan is the wrong shape for a command
// whose premise is that an agent is never left waiting: eleven providers, each
// a Search plus up to MaxCandidates GetSeasons calls against a 30s HTTP client
// timeout, is minutes of silence on a degraded chain. Provider order still
// decides the winner, so the answer does not depend on which provider was
// quickest.
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

	ctx, cancel := context.WithTimeout(context.Background(), episodesFallbackTimeout)
	defer cancel()

	fallbacks := agentFallbackProviders(primary)
	// Indexed by provider so the result is provider order, not arrival order.
	hits := make([]*seasonHit, len(fallbacks))
	var wg sync.WaitGroup
	for i, fb := range fallbacks {
		wg.Add(1)
		go func(idx int, p provider.Provider) {
			defer wg.Done()
			hits[idx] = probeSeasons(ctx, p, req)
		}(i, fb)
	}
	// Every worker returns at the deadline even when a provider call is still
	// blocked: the calls are wrapped in the same select-on-ctx pattern
	// searchWithContext uses, so this Wait is bounded by the context.
	wg.Wait()

	for _, h := range hits {
		if h != nil {
			debugf("episodes: %T answers for %q (ID %s)", h.provider, h.title, h.id)
			return h.provider, h.id, h.seasons, nil
		}
	}
	return primary, r.ID, nil, err
}

// seasonHit is one fallback provider's answer for a ref.
type seasonHit struct {
	provider provider.Provider
	id       string
	title    string
	seasons  []media.Season
}

// probeSeasons asks one fallback provider whether it has req's work and can
// enumerate its seasons. It returns nil for "no", including when the context
// expires mid-call.
func probeSeasons(ctx context.Context, p provider.Provider, req resolver.Request) *seasonHit {
	results, err := searchWithContext(ctx, p, req.Title)
	if err != nil {
		debugf("episodes: fallback %T search failed: %v", p, err)
		return nil
	}
	for _, c := range resolver.Candidates(results, req) {
		if !resolver.Matches(c, req) {
			debugf("episodes: fallback %T offered %q, which is not %q", p, c.Title, req.Title)
			continue
		}
		seasons, err := seasonsWithContext(ctx, p, c.ID)
		if err != nil || len(seasons) == 0 {
			continue
		}
		return &seasonHit{provider: p, id: c.ID, title: c.Title, seasons: seasons}
	}
	return nil
}

// seasonsWithContext runs GetSeasons, abandoning the result if the context
// expires. provider.Provider takes no context, so — exactly as in
// searchWithContext — the inner goroutine runs to completion and its result is
// discarded; it ends when the underlying HTTP call does.
func seasonsWithContext(ctx context.Context, p provider.Provider, id string) ([]media.Season, error) {
	type result struct {
		seasons []media.Season
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := p.GetSeasons(id)
		ch <- result{s, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.seasons, r.err
	}
}
