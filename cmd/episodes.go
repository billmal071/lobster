package cmd

import (
	"context"
	"fmt"
	"strings"
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
	src := seasonSource(primary, r)
	if len(src.seasons) == 0 {
		if src.err != nil {
			return emitErr("providers_failed", exitProvidersFailed, "getting seasons: %v", src.err)
		}
		return emitErr("no_results", exitNoResults, "no seasons found for %q", r.Title)
	}

	p, seasons := src.provider, src.seasons
	// --season shares flagSeason with play, whose default is 0 and which
	// requires a positive number, so this command cannot tell "--season 0" from
	// "no --season" and keeps reading both as "the first season". The
	// translation stops there: everything downstream sees the sentinel, so
	// pickSeason's 0 means season zero for the recovery callers, which do pass
	// a concrete number.
	wantSeason := flagSeason
	if wantSeason <= 0 {
		wantSeason = seasonUnspecified
	}
	sel, ok := pickSeason(seasons, wantSeason)
	if !ok {
		// One season list is not the show. VidNest's GetSeasons probes each
		// season for streams and stops at the first one it cannot reach, so
		// whoever answered here can genuinely have fewer seasons than the show
		// does — and `--season 5` was no_results for a show another chain
		// member lists in full. Episodes already ask every hit; seasons did
		// not.
		src, seasons, sel, ok = seasonAcrossHits(primary, r, src, wantSeason)
		p = src.provider
	}
	if !ok {
		return emitErr("no_results", exitNoResults, "season %d not found for %q", flagSeason, r.Title)
	}

	eps, err := listSeasonEpisodes(p, src, sel)
	if err != nil || len(eps) == 0 {
		// Enumerating seasons and enumerating episodes are different
		// questions, and a provider can answer the first and not the second:
		// MovieBox reports a real season count from cached search data while
		// its episode listing needs authentication, and VidNest measures
		// seasons by probing for streams but has no episode index at all.
		// Both now return an error rather than a generated list, so without
		// this the command would be exit 3 for every show under such a
		// primary.
		//
		// Whoever answered here, the chain is asked next — and every hit in it
		// is asked, not just the first. Selecting a source on "can you
		// enumerate seasons?" and then making one GetEpisodes call meant a
		// single seasons-yes/episodes-no provider ended the search for a show
		// the rest of the chain could list; the real chain leads with two of
		// exactly that shape (VidNest, MovieBox).
		//
		// src.alts is already in hand when the season list came from the
		// chain, so that case costs no extra scan. Only a primary-sourced
		// season list has to run the scan here.
		debugf("episodes: %T enumerated seasons but not episodes (err=%v, episodes=%d); asking the fallback chain", p, err, len(eps))
		alts := src.alts
		if src.fromPrimary {
			alts = fallbackSeasonHits(primary, seasonRequest(r))
		}
		if a := firstEpisodeList(alts, wantSeason); a != nil {
			debugf("episodes: %T listed %d episodes of season %d", a.hit.provider, len(a.episodes), a.season.Number)
			p, seasons, sel, eps, err = a.hit.provider, a.hit.seasons, a.season, a.episodes, nil
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
		"provider": providerLabel(p),
	})
}

// listSeasonEpisodes makes the first episode-listing call, under a deadline
// whenever the provider is a chain member rather than the configured primary.
//
// Every other chain call in this file goes through seasonsWithContext or
// episodesWithContext; this one did not, and a chain provider slow to list
// episodes could hold `episodes` for the HTTP client's own timeout — around
// 30s against the 5s the command otherwise promises. The primary keeps its
// unbounded call: it is the user's own configured provider, it is asked
// exactly once, and no fan-out is waiting on it.
func listSeasonEpisodes(p provider.Provider, src seasonAnswer, sel media.Season) ([]media.Episode, error) {
	if src.fromPrimary {
		return p.GetEpisodes(src.id, sel.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), episodesFallbackTimeout)
	defer cancel()
	return episodesWithContext(ctx, p, src.id, sel.ID)
}

// providerLabel names a provider for the JSON envelope. provider.Provider has
// no Name method — cmd/fallback_providers_test.go reaches for %T for the same
// reason — so the concrete type is reduced to a lowercase token:
// *provider.VaPlayer becomes "vaplayer".
//
// It is an attribution, not a --base value: episodes answers from whichever
// provider could enumerate the ref, which is often not the configured primary
// and need not be reachable as a base at all. Naming it is what makes a
// listing checkable — a run that looked like one provider's answer was
// another's, which is how two providers shipped invented episode lists without
// anyone noticing.
func providerLabel(p provider.Provider) string {
	name := fmt.Sprintf("%T", p)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
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
func seasonSource(primary provider.Provider, r playRef) seasonAnswer {
	seasons, err := primary.GetSeasons(r.ID)
	if err == nil && len(seasons) > 0 {
		return seasonAnswer{provider: primary, id: r.ID, seasons: seasons, fromPrimary: true}
	}
	debugf("episodes: primary could not enumerate %q (err=%v, seasons=%d); re-searching the fallback chain by title", r.ID, err, len(seasons))

	if hits := fallbackSeasonHits(primary, seasonRequest(r)); len(hits) > 0 {
		return seasonAnswer{
			provider: hits[0].provider,
			id:       hits[0].id,
			seasons:  hits[0].seasons,
			alts:     hits[1:],
		}
	}
	return seasonAnswer{provider: primary, id: r.ID, fromPrimary: true, err: err}
}

// seasonAcrossHits looks for wantSeason in the chain hits src did not pick,
// returning the hit that has it as the new answer. It reports ok false when
// none does, leaving the caller's own answer untouched.
//
// The scan only has to run when the season list came from the primary; a
// chain-sourced answer already carries its alternatives.
func seasonAcrossHits(primary provider.Provider, r playRef, src seasonAnswer, wantSeason int) (seasonAnswer, []media.Season, media.Season, bool) {
	alts := src.alts
	if src.fromPrimary {
		alts = fallbackSeasonHits(primary, seasonRequest(r))
	}
	for i, h := range alts {
		sel, ok := pickSeason(h.seasons, wantSeason)
		if !ok {
			continue
		}
		debugf("episodes: %T has season %d, which %T does not", h.provider, sel.Number, src.provider)
		// The hits after this one stay available, so a provider that has the
		// season but cannot list its episodes still falls through to the next.
		return seasonAnswer{
			provider: h.provider,
			id:       h.id,
			seasons:  h.seasons,
			alts:     alts[i+1:],
		}, h.seasons, sel, true
	}
	return src, src.seasons, media.Season{}, false
}

// seasonAnswer is who ended up answering seasonSource, plus the chain hits it
// did not pick. alts exists so the caller can move on to the next provider
// when the chosen one turns out to enumerate seasons but not episodes, without
// paying for the chain scan a second time.
type seasonAnswer struct {
	provider provider.Provider
	id       string
	seasons  []media.Season
	alts     []*seasonHit
	// fromPrimary reports that the chain scan has not run, so alts is empty
	// because nothing looked rather than because nothing answered.
	fromPrimary bool
	err         error
}

// seasonRequest is the ref as the resolver sees it: the ID/Title/Year all
// count towards ranking, which is the reason a ref carries more than an ID.
func seasonRequest(r playRef) resolver.Request {
	return resolver.Request{
		ID:        r.ID,
		Title:     r.Title,
		Year:      r.Year,
		MediaType: media.TV,
	}
}

// fallbackEpisodeList finds a chain provider that has this work and can list
// the requested season's episodes. seasonNumber is a concrete number — season
// 0 means season 0, and a chain provider that does not have it is no answer.
//
// It is the same move `episodes` makes, exposed for the playback paths: a
// primary that enumerates seasons but not episodes leaves every list-shaped
// caller — the interactive episode menu, the TUI download dialog, a
// multi-season batch — with nothing to show, and the chain usually has the
// list. The answer is a provider's own list, so a number offered from it is a
// number that provider will honour.
func fallbackEpisodeList(primary provider.Provider, content media.SearchResult, seasonNumber int) *episodeAnswer {
	if seasonNumber < 0 {
		// Every caller here has a season in hand. A negative number is not a
		// request for "whatever is first" — it is a caller that lost track of
		// which season it was on, and answering it with some other season is
		// the substitution this path exists to refuse.
		debugf("fallback episode list: refusing a negative season number %d", seasonNumber)
		return nil
	}
	return firstEpisodeList(fallbackSeasonHits(primary, contentRequest(content)), seasonNumber)
}

// contentRequest is a selected search result as the resolver sees it: the
// ID/Title/Year all count towards ranking a chain provider's candidates.
func contentRequest(content media.SearchResult) resolver.Request {
	return resolver.Request{
		ID:        content.ID,
		Title:     content.Title,
		Year:      content.Year,
		MediaType: content.Type,
	}
}

// seasonUnspecified is pickSeason's "no season was requested", which is a
// distinct value rather than 0.
//
// Season 0 is a real, reachable number: consumet maps it straight from
// ep.Season for specials (internal/provider/consumet.go), and flixhqws and
// parser reach it through strconv.Atoi, which returns 0 whenever the label
// does not parse. While want <= 0 meant "first season", a caller asking for
// season 0 — and the recovery callers all pass a concrete number — was handed
// whatever season happened to be first, with nothing saying the season had
// changed.
const seasonUnspecified = -1

// pickSeason returns the season with the requested number, or the first season
// when want is seasonUnspecified. ok is false only for a requested number the
// list does not have — season 0 included.
func pickSeason(seasons []media.Season, want int) (media.Season, bool) {
	if len(seasons) == 0 {
		return media.Season{}, false
	}
	if want == seasonUnspecified {
		return seasons[0], true
	}
	for _, s := range seasons {
		if s.Number == want {
			return s, true
		}
	}
	return media.Season{}, false
}

// fallbackSeasonHits runs the parallel, deadline-bounded scan of the fallback
// chain described on seasonSource, returning every provider that has this work
// and can enumerate its seasons, in chain order.
//
// Every hit, not just the first: enumerating seasons does not imply
// enumerating episodes, so the caller needs somewhere to go when its first
// choice cannot answer the second question.
func fallbackSeasonHits(primary provider.Provider, req resolver.Request) []*seasonHit {
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

	found := make([]*seasonHit, 0, len(hits))
	for _, h := range hits {
		if h != nil {
			debugf("episodes: %T answers for %q (ID %s)", h.provider, h.title, h.id)
			found = append(found, h)
		}
	}
	return found
}

// episodeAnswer is one fallback provider's episode list for the requested
// season.
type episodeAnswer struct {
	hit      *seasonHit
	season   media.Season
	episodes []media.Episode
}

// firstEpisodeList asks every hit for the requested season's episodes and
// returns the first one in chain order that answers, or nil.
//
// It fans out under the same deadline the season scan uses, for the same
// reason: asking up to eleven providers in turn, each against a 30s HTTP
// timeout, is minutes of silence on a degraded chain, and `episodes` exists so
// an agent is never left waiting. Provider order still decides the winner, so
// the answer does not depend on which provider was quickest.
func firstEpisodeList(hits []*seasonHit, wantSeason int) *episodeAnswer {
	if len(hits) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), episodesFallbackTimeout)
	defer cancel()

	answers := make([]*episodeAnswer, len(hits))
	var wg sync.WaitGroup
	for i, h := range hits {
		sel, ok := pickSeason(h.seasons, wantSeason)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(idx int, h *seasonHit, sel media.Season) {
			defer wg.Done()
			eps, err := episodesWithContext(ctx, h.provider, h.id, sel.ID)
			if err != nil || len(eps) == 0 {
				debugf("episodes: fallback %T cannot list season %d (err=%v, episodes=%d)", h.provider, sel.Number, err, len(eps))
				return
			}
			answers[idx] = &episodeAnswer{hit: h, season: sel, episodes: eps}
		}(i, h, sel)
	}
	wg.Wait()

	for _, a := range answers {
		if a != nil {
			return a
		}
	}
	return nil
}

// episodesWithContext is seasonsWithContext for GetEpisodes, with the same
// caveat: provider.Provider takes no context, so the inner goroutine runs to
// completion and its result is discarded when the deadline wins.
func episodesWithContext(ctx context.Context, p provider.Provider, id, seasonID string) ([]media.Episode, error) {
	type result struct {
		episodes []media.Episode
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		e, err := p.GetEpisodes(id, seasonID)
		ch <- result{e, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.episodes, r.err
	}
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
