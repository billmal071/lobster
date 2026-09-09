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
	p, id, seasons, fromPrimary, primaryErr := seasonSource(primary, r)
	if len(seasons) == 0 {
		if primaryErr != nil {
			return emitErr("providers_failed", exitProvidersFailed, "getting seasons: %v", primaryErr)
		}
		return emitErr("no_results", exitNoResults, "no seasons found for %q", r.Title)
	}

	sel, ok := pickSeason(seasons, flagSeason)
	if !ok {
		return emitErr("no_results", exitNoResults, "season %d not found for %q", flagSeason, r.Title)
	}

	eps, err := p.GetEpisodes(id, sel.ID)
	if (err != nil || len(eps) == 0) && fromPrimary {
		// Enumerating seasons and enumerating episodes are different
		// questions, and a provider can answer the first and not the second:
		// MovieBox reports a real season count from cached search data while
		// its episode listing needs authentication, and VidNest measures
		// seasons by probing for streams but has no episode index at all.
		// Both now return an error rather than a generated list, so without
		// this the command would be exit 3 for every show under such a
		// primary. Re-search the chain exactly as a failed GetSeasons does.
		//
		// Only when the primary answered: if seasons already came from the
		// fallback scan, that scan has run and rerunning it would double this
		// command's worst-case wait.
		debugf("episodes: %T enumerated seasons but not episodes (err=%v, episodes=%d); re-searching the fallback chain by title", p, err, len(eps))
		if fp, fid, fseasons, fok := fallbackSeasonSource(primary, r); fok {
			if fsel, ok := pickSeason(fseasons, flagSeason); ok {
				if feps, ferr := fp.GetEpisodes(fid, fsel.ID); ferr == nil && len(feps) > 0 {
					p, id, seasons, sel, eps, err = fp, fid, fseasons, fsel, feps, nil
				}
			}
		}
	}
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
// supply stubs instead of providers that reach the network. Both users of the
// chain go through it: this file's season scan and tryFallbackStream
// (cmd/fallback.go).
var agentFallbackProviders = fallbackProviders

// episodesFallbackTimeout bounds the whole fallback season scan. It matches
// multiSearchTimeout, which bounds find's fan-out over the same chain — the two
// commands wait on the same providers and should give up at the same point.
// A var, not a const, so tests can shrink it and still be deterministic.
var episodesFallbackTimeout = multiSearchTimeout

// seasonSource returns the provider that can actually enumerate this ref's
// seasons, the ID to ask it about, that season list, whether the answer came
// from the primary (so a caller knows the fallback scan has not run yet), and
// the primary's error if it had one.
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
func seasonSource(primary provider.Provider, r playRef) (provider.Provider, string, []media.Season, bool, error) {
	seasons, err := primary.GetSeasons(r.ID)
	if err == nil && len(seasons) > 0 {
		return primary, r.ID, seasons, true, nil
	}
	debugf("episodes: primary could not enumerate %q (err=%v, seasons=%d); re-searching the fallback chain by title", r.ID, err, len(seasons))

	if p, id, fallbackSeasons, ok := fallbackSeasonSource(primary, r); ok {
		return p, id, fallbackSeasons, false, nil
	}
	return primary, r.ID, nil, true, err
}

// pickSeason returns the season with the requested number, or the first season
// when want is 0 (no --season given). ok is false only for a requested number
// the list does not have.
func pickSeason(seasons []media.Season, want int) (media.Season, bool) {
	if len(seasons) == 0 {
		return media.Season{}, false
	}
	if want <= 0 {
		return seasons[0], true
	}
	for _, s := range seasons {
		if s.Number == want {
			return s, true
		}
	}
	return media.Season{}, false
}

// fallbackSeasonSource runs the parallel, deadline-bounded scan of the
// fallback chain described on seasonSource, returning the first provider in
// chain order that has this work and can enumerate its seasons.
func fallbackSeasonSource(primary provider.Provider, r playRef) (provider.Provider, string, []media.Season, bool) {
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
			return h.provider, h.id, h.seasons, true
		}
	}
	return nil, "", nil, false
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
