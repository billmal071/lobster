package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/ui"
)

const multiSearchTimeout = 5 * time.Second

// An empty search has two causes that call for opposite advice, so
// gatherSearchResults reports which one happened and callers branch on it with
// errors.Is rather than matching the message text.
//
// errNoResults means every provider that was asked answered, and none of them
// indexes this title — usually a typo, so `find` exits 2 and the agent should
// suggest a spelling. errProvidersFailed means nothing answered at all, so
// `find` exits 3 and the agent should run `lobster doctor`. Conflating them is
// not cosmetic: exit 3 on a typo sends an agent to diagnose provider health
// over a misspelling, which is what the binary did until this split existed.
var (
	errNoResults       = errors.New("no results found")
	errProvidersFailed = errors.New("no provider could be reached")
)

// gatherSearchResults runs the primary provider's search, then broadens to the
// fallback providers whenever the primary errors or returns few results — so a
// title the primary catalog lacks (e.g. anime, which only the TMDB/AllAnime/
// AniPub providers index) is still discoverable.
//
// It errors only when no provider yields anything, with errNoResults or
// errProvidersFailed wrapped in so the caller can tell the two apart. Both
// messages name the query, because the interactive path (playFlow) prints the
// error verbatim.
func gatherSearchResults(primary provider.Provider, fallbacks []provider.Provider, query string) ([]media.SearchResult, error) {
	stop := ui.StartSpinner(fmt.Sprintf("Searching for %q...", query))
	results, err := primary.Search(query)
	stop()
	// A provider that answered with nothing still counts as reached: it is
	// evidence the title does not exist, which a failed call is not. Most
	// providers signal "nothing matched" with an error rather than an empty
	// slice, so `err == nil` alone would classify every real typo as an
	// outage — see provider.ErrNoResults.
	reached := err == nil || errors.Is(err, provider.ErrNoResults)
	if err != nil {
		debugf("primary search (%T) failed: %v; broadening to fallback providers", primary, err)
		results = nil
	}

	// The merged results may originate from fallback providers, but playback
	// still uses the primary provider. Providers use their own ID formats;
	// stream resolution is title-based via the resolver, so cross-provider
	// fallback works.
	if len(results) < 3 {
		debugf("primary returned %d results, searching fallback providers...", len(results))
		stop = ui.StartSpinner("Searching more providers...")
		merged, fallbackReached := multiProviderSearch(results, fallbacks, query)
		stop()
		reached = reached || fallbackReached
		if len(merged) > 0 {
			results = merged
		}
	}

	if len(results) == 0 {
		if !reached {
			return nil, fmt.Errorf("%w (searching %q)", errProvidersFailed, query)
		}
		return nil, fmt.Errorf("%w for %q", errNoResults, query)
	}
	return results, nil
}

// multiProviderSearch searches the fallback providers in parallel and merges
// them behind primaryResults, which the caller has already obtained. The
// primary is deliberately not re-queried: a thin-but-successful first search
// followed by a transient second failure used to discard the only results the
// user would have seen.
//
// The second return reports whether at least one fallback actually answered —
// with or without results. gatherSearchResults needs it to tell "no provider
// indexes this title" from "no provider is up", and an empty merged slice
// cannot distinguish the two on its own.
func multiProviderSearch(primaryResults []media.SearchResult, fallbacks []provider.Provider, query string) ([]media.SearchResult, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), multiSearchTimeout)
	defer cancel()

	var mu sync.Mutex
	// Pre-allocate slice for fallback results to maintain order.
	fallbackResults := make([][]media.SearchResult, len(fallbacks))
	reached := false

	var wg sync.WaitGroup
	for i, fb := range fallbacks {
		wg.Add(1)
		go func(idx int, p provider.Provider) {
			defer wg.Done()
			results, err := searchWithContext(ctx, p, query)
			if err != nil {
				debugf("multi-search fallback (%T) failed: %v", p, err)
				// Answered, just emptily — still evidence about the title.
				if errors.Is(err, provider.ErrNoResults) {
					mu.Lock()
					reached = true
					mu.Unlock()
				}
				return
			}
			mu.Lock()
			fallbackResults[idx] = results
			reached = true
			mu.Unlock()
		}(i, fb)
	}

	wg.Wait()

	return deduplicateResults(primaryResults, fallbackResults), reached
}

// searchWithContext runs a provider search, aborting if the context expires.
// Note: the inner goroutine continues running after a timeout because
// provider.Search does not accept a context. This is intentional — the
// goroutine's result is simply discarded via the select, and the goroutine
// will terminate naturally when the HTTP request completes or times out.
func searchWithContext(ctx context.Context, p provider.Provider, query string) ([]media.SearchResult, error) {
	type result struct {
		results []media.SearchResult
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := p.Search(query)
		ch <- result{r, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.results, r.err
	}
}

// deduplicateResults merges primary results with fallback results, deduplicating
// by title, media type and year (case-insensitive). When duplicates exist, the
// entry with more metadata (poster, year, seasons/episodes) is preferred.
func deduplicateResults(primary []media.SearchResult, fallbackGroups [][]media.SearchResult) []media.SearchResult {
	// Dedup on title + media type + year, not title alone: "Spider-Man" is the
	// 2002 film, a 1994 cartoon and a 1967 cartoon, and collapsing them hides
	// most of a franchise behind one row. Providers that omit the year fall
	// back to a looser title+type match so they still merge rather than
	// producing a near-duplicate.
	seen := make(map[string]int)        // title|type|year -> index in merged
	yearless := make(map[string]int)    // title|type -> index of a year-less entry
	byTitleType := make(map[string]int) // title|type -> index of the first entry
	var merged []media.SearchResult

	// Two entries for the same work carry different subsets of the metadata.
	// Keep the richer one as the base and fill its gaps from the other, so a
	// year-bearing duplicate can never leave the merged entry year-less —
	// an empty year disables the resolver's year-based candidate ranking.
	keep := func(idx int, r media.SearchResult) {
		// The ID of whichever entry arrived first is authoritative, and that is
		// the primary provider's when it supplied this work. Playback resolves
		// against the primary, and providers do not share an ID namespace — YTS
		// answers to yts/3024, not to a TMDB movie/1930. Letting the richer
		// duplicate donate its ID handed playback an ID its own provider could
		// not resolve, so --base yts fell through to another provider and played
		// that one's dub instead. Metadata still merges; only the ID is pinned.
		id := merged[idx].ID
		if resultScore(r) > resultScore(merged[idx]) {
			r = fillGaps(r, merged[idx])
		} else {
			r = fillGaps(merged[idx], r)
		}
		if id != "" {
			r.ID = id
		}
		merged[idx] = r
	}

	addResult := func(r media.SearchResult) {
		loose := normalizeTitle(r.Title) + "|" + r.Type.String()
		key := loose + "|" + r.Year

		if idx, ok := seen[key]; ok {
			keep(idx, r)
			return
		}
		if r.Year == "" {
			// No year to disambiguate with: merge into whatever we already have
			// for this title and type.
			if idx, ok := byTitleType[loose]; ok {
				keep(idx, r)
				return
			}
		} else if idx, ok := yearless[loose]; ok {
			// A year-less entry for this work arrived first — upgrade it in place.
			keep(idx, r)
			seen[key] = idx
			delete(yearless, loose)
			return
		}

		idx := len(merged)
		seen[key] = idx
		if _, ok := byTitleType[loose]; !ok {
			byTitleType[loose] = idx
		}
		if r.Year == "" {
			yearless[loose] = idx
		}
		merged = append(merged, r)
	}

	// Primary results first.
	for _, r := range primary {
		addResult(r)
	}

	// Fallback results in provider order.
	for _, group := range fallbackGroups {
		for _, r := range group {
			addResult(r)
		}
	}

	return merged
}

// fillGaps returns base with every empty field filled in from other.
func fillGaps(base, other media.SearchResult) media.SearchResult {
	if base.ID == "" {
		base.ID = other.ID
	}
	if base.Year == "" {
		base.Year = other.Year
	}
	if base.Duration == "" {
		base.Duration = other.Duration
	}
	if base.URL == "" {
		base.URL = other.URL
	}
	if base.Poster == "" {
		base.Poster = other.Poster
	}
	if base.Seasons == 0 {
		base.Seasons = other.Seasons
	}
	if base.Episodes == 0 {
		base.Episodes = other.Episodes
	}
	return base
}

// normalizeTitle returns a lowercase, trimmed version of the title for dedup.
func normalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

// resultScore returns a simple metadata completeness score for a search result.
// Higher is better.
func resultScore(r media.SearchResult) int {
	score := 0
	if r.Year != "" {
		score++
	}
	if r.Poster != "" {
		score++
	}
	if r.Duration != "" {
		score++
	}
	if r.Seasons > 0 {
		score++
	}
	if r.Episodes > 0 {
		score++
	}
	if r.URL != "" {
		score++
	}
	return score
}

// fallbackSearchProviders returns fallback providers suitable for search.
// This is a subset of fallbackProviders — only providers that have meaningful
// search capabilities and cover different content catalogs.
// Providers whose concrete type matches the primary are excluded to avoid
// re-querying the same source in fallback mode.
func fallbackSearchProviders(primary provider.Provider) []provider.Provider {
	all := fallbackProviders(primary)
	primaryType := fmt.Sprintf("%T", primary)
	filtered := make([]provider.Provider, 0, len(all))
	for _, fb := range all {
		if fmt.Sprintf("%T", fb) != primaryType {
			filtered = append(filtered, fb)
		}
	}
	return filtered
}
