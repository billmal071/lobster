package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"
	"lobster/internal/media"
	"lobster/internal/poster"
	"lobster/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

// fetchTabCmd fetches the default listing for a dashboard category.
func fetchTabCmd(p provider.Provider, active tab) tea.Cmd {
	return func() tea.Msg {
		mediaType := media.Movie
		if active == tabSeries || active == tabCartoons || active == tabAnime {
			mediaType = media.TV
		}

		results, err := p.Trending(mediaType)
		if err != nil {
			return errMsg{err}
		}
		return resultsFetchedMsg(results)
	}
}

// searchCmd searches for items matching the query using the primary provider
// and optional fallback providers. When the primary returns few results (< 3),
// fallbacks are searched in parallel and results are merged/deduplicated.
func searchCmd(p provider.Provider, query string, fallbacks ...provider.Provider) tea.Cmd {
	return func() tea.Msg {
		results, err := p.Search(query)
		if err != nil {
			return errMsg{err}
		}

		// If we got enough results or have no fallbacks, return immediately.
		if len(results) >= 3 || len(fallbacks) == 0 {
			return resultsFetchedMsg(results)
		}

		// Search fallbacks in parallel with a 5s timeout.
		// Provider provenance is not tracked when merging because all providers
		// use TMDB IDs as their universal content identifier — any provider can
		// resolve streams for results discovered by another.
		merged := tuiMultiSearch(results, fallbacks, query)
		return resultsFetchedMsg(merged)
	}
}

// tuiMultiSearch searches fallback providers in parallel and merges with
// existing primary results, deduplicating by title (case-insensitive).
func tuiMultiSearch(primary []media.SearchResult, fallbacks []provider.Provider, query string) []media.SearchResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	fbResults := make([][]media.SearchResult, len(fallbacks))

	var wg sync.WaitGroup
	for i, fb := range fallbacks {
		wg.Add(1)
		go func(idx int, p provider.Provider) {
			defer wg.Done()
			ch := make(chan []media.SearchResult, 1)
			go func() {
				r, err := p.Search(query)
				if err != nil {
					ch <- nil
					return
				}
				ch <- r
			}()
			select {
			case <-ctx.Done():
				return
			case r := <-ch:
				mu.Lock()
				fbResults[idx] = r
				mu.Unlock()
			}
		}(i, fb)
	}
	wg.Wait()

	return tuiDeduplicateResults(primary, fbResults)
}

// tuiDeduplicateResults merges primary and fallback results, deduplicating
// by title (case-insensitive). Prefers the result with more metadata.
func tuiDeduplicateResults(primary []media.SearchResult, fallbackGroups [][]media.SearchResult) []media.SearchResult {
	type entry struct {
		idx   int
		score int
	}
	seen := make(map[string]entry)
	var merged []media.SearchResult

	addResult := func(r media.SearchResult) {
		key := strings.ToLower(strings.TrimSpace(r.Title))
		score := tuiResultScore(r)
		if e, exists := seen[key]; exists {
			if score > e.score {
				merged[e.idx] = r
				seen[key] = entry{e.idx, score}
			}
			return
		}
		seen[key] = entry{len(merged), score}
		merged = append(merged, r)
	}

	for _, r := range primary {
		addResult(r)
	}
	for _, group := range fallbackGroups {
		for _, r := range group {
			addResult(r)
		}
	}

	return merged
}

// tuiResultScore returns a metadata completeness score for dedup preference.
func tuiResultScore(r media.SearchResult) int {
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

// posterURLForItem chooses the poster URL to render: upgrade an empty or
// non-TMDB poster to a confidently-matched TMDB one, else keep what the item
// already has (e.g. FlixHQ's own thumbnail). lookup is injected for testing.
func posterURLForItem(item media.SearchResult, lookup func(title, year string, isTV bool) string) string {
	u := item.Poster
	if u == "" || !strings.Contains(u, "image.tmdb.org") {
		if tu := lookup(item.Title, item.Year, item.Type == media.TV); tu != "" {
			u = tu
		}
	}
	return u
}

// fetchDetailCmd fetches detailed metadata for a specific item.
func fetchDetailCmd(p provider.Provider, id string) tea.Cmd {
	return func() tea.Msg {
		detail, err := p.GetDetails(id)
		if err != nil {
			return errMsg{err}
		}
		return detailFetchedMsg{id: id, detail: detail}
	}
}

// queueDownloadCmd adds a single download to the queue.
func queueDownloadCmd(mgr *dlmanager.Manager, d *store.Download) tea.Cmd {
	return func() tea.Msg {
		id, err := mgr.Queue(d)
		if err != nil {
			return errMsg{err}
		}
		return downloadQueuedMsg{downloadID: id, title: d.Title}
	}
}

// queueSeasonCmd queues all episodes in a season for download.
func queueSeasonCmd(mgr *dlmanager.Manager, downloads []*store.Download) tea.Cmd {
	return func() tea.Msg {
		for _, d := range downloads {
			if _, err := mgr.Queue(d); err != nil {
				return errMsg{err}
			}
		}
		title := "Unknown"
		if len(downloads) > 0 {
			title = downloads[0].MediaTitle
		}
		return downloadBatchQueuedMsg{count: len(downloads), title: title}
	}
}

// fetchPosterForItemCmd resolves the best poster URL for item (upgrading to a
// TMDB high-res poster when the caller's lookup policy allows) and renders it
// for the detail pane. lookup is supplied per-tab so only the Movies/Series
// (FlixHQ) views opt into TMDB enrichment; other tabs keep their own posters.
func fetchPosterForItemCmd(item media.SearchResult, width, height int, lookup func(title, year string, isTV bool) string) tea.Cmd {
	return func() tea.Msg {
		url := posterURLForItem(item, lookup)
		if url == "" {
			return posterFetchedMsg{id: item.ID, inline: poster.IsInlineImage()}
		}
		if poster.IsInlineImage() {
			b64, w, h, err := poster.FetchInlineImage(url)
			if err != nil {
				return posterFetchedMsg{id: item.ID, inline: true} // empty -> placeholder box
			}
			return posterFetchedMsg{id: item.ID, inline: true, b64: b64, imgW: w, imgH: h}
		}
		rendered := poster.RenderTUI(url, width, height)
		return posterFetchedMsg{id: item.ID, poster: rendered}
	}
}

// listenProgressCmd reads one progress update from the manager and returns it.
// The TUI re-invokes this after each message to keep listening.
func listenProgressCmd(mgr *dlmanager.Manager) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-mgr.Progress()
		if !ok {
			return nil // Channel closed, manager stopped.
		}
		return downloadProgressMsg(update)
	}
}
