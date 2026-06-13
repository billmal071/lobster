package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
)

const multiSearchTimeout = 5 * time.Second

// multiProviderSearch searches the primary provider and fallbacks in parallel,
// then merges and deduplicates results. The primary provider's results are
// preferred and appear first.
func multiProviderSearch(primary provider.Provider, fallbacks []provider.Provider, query string) []media.SearchResult {
	ctx, cancel := context.WithTimeout(context.Background(), multiSearchTimeout)
	defer cancel()

	var mu sync.Mutex
	var primaryResults []media.SearchResult
	var fallbackResults [][]media.SearchResult

	// Pre-allocate slice for fallback results to maintain order.
	fallbackResults = make([][]media.SearchResult, len(fallbacks))

	var wg sync.WaitGroup

	// Search primary provider.
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := searchWithContext(ctx, primary, query)
		if err != nil {
			debugf("multi-search primary (%T) failed: %v", primary, err)
			return
		}
		mu.Lock()
		primaryResults = results
		mu.Unlock()
	}()

	// Search fallback providers in parallel.
	for i, fb := range fallbacks {
		wg.Add(1)
		go func(idx int, p provider.Provider) {
			defer wg.Done()
			results, err := searchWithContext(ctx, p, query)
			if err != nil {
				debugf("multi-search fallback (%T) failed: %v", p, err)
				return
			}
			mu.Lock()
			fallbackResults[idx] = results
			mu.Unlock()
		}(i, fb)
	}

	wg.Wait()

	return deduplicateResults(primaryResults, fallbackResults)
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
// by title (case-insensitive). When duplicates exist, the entry with more
// metadata (poster, year, seasons/episodes) is preferred.
func deduplicateResults(primary []media.SearchResult, fallbackGroups [][]media.SearchResult) []media.SearchResult {
	seen := make(map[string]int) // normalized title -> index in merged
	var merged []media.SearchResult

	addResult := func(r media.SearchResult) {
		key := normalizeTitle(r.Title)
		if idx, exists := seen[key]; exists {
			// Keep the one with more metadata.
			existing := merged[idx]
			if resultScore(r) > resultScore(existing) {
				merged[idx] = r
			}
			return
		}
		seen[key] = len(merged)
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
