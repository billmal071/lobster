package tui

import (
	"context"
	"errors"
	"fmt"
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
		if active == tabSeries || active == tabAnime {
			mediaType = media.TV
		}

		results, err := p.Trending(mediaType)
		if err != nil {
			return errMsg{err}
		}
		return resultsFetchedMsg{results: results}
	}
}

// searchCmd searches for items matching the query using the primary provider
// and optional fallback providers. When the primary returns few results (< 3),
// fallbacks are searched in parallel and results are merged/deduplicated, each
// row keeping the provider whose ID it carries.
func searchCmd(p provider.Provider, query string, fallbacks ...provider.Provider) tea.Cmd {
	return func() tea.Msg {
		results, err := p.Search(query)
		if err != nil && (len(fallbacks) == 0 || !errors.Is(err, provider.ErrNoResults)) {
			return errMsg{err}
		}

		// If we got enough results or have no fallbacks, return immediately.
		if len(results) >= 3 || len(fallbacks) == 0 {
			return resultsFetchedMsg{results: results}
		}

		// Search fallbacks in parallel with a 5s timeout.
		merged, owners := tuiMultiSearch(p, results, fallbacks, query)
		if len(merged) == 0 && err != nil {
			return errMsg{err}
		}
		return resultsFetchedMsg{results: merged, owners: owners}
	}
}

// tuiMultiSearch searches fallback providers in parallel and merges with
// existing primary results, deduplicating by title (case-insensitive).
func tuiMultiSearch(primaryProvider provider.Provider, primary []media.SearchResult, fallbacks []provider.Provider, query string) ([]media.SearchResult, []provider.Provider) {
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

	return tuiDeduplicateResults(primaryProvider, primary, fallbacks, fbResults)
}

// tuiDeduplicateResults merges primary and fallback results, deduplicating
// by title (case-insensitive). Prefers the result with more metadata. The
// second slice is index-aligned with the first and names the provider each
// row came from.
func tuiDeduplicateResults(primaryProvider provider.Provider, primary []media.SearchResult, fallbacks []provider.Provider, fallbackGroups [][]media.SearchResult) ([]media.SearchResult, []provider.Provider) {
	type entry struct {
		idx   int
		score int
	}
	seen := make(map[string]entry)
	var merged []media.SearchResult
	var owners []provider.Provider

	addResult := func(r media.SearchResult, owner provider.Provider) {
		key := strings.ToLower(strings.TrimSpace(r.Title))
		score := tuiResultScore(r)
		if e, exists := seen[key]; exists {
			if score > e.score {
				merged[e.idx] = r
				owners[e.idx] = owner
				seen[key] = entry{e.idx, score}
			}
			return
		}
		seen[key] = entry{len(merged), score}
		merged = append(merged, r)
		owners = append(owners, owner)
	}

	for _, r := range primary {
		addResult(r, primaryProvider)
	}
	for i, group := range fallbackGroups {
		for _, r := range group {
			addResult(r, fallbacks[i])
		}
	}

	return merged, owners
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
		return detailFetchedMsg{id: id, owner: p, detail: detail}
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
func fetchPosterForItemCmd(item media.SearchResult, owner provider.Provider, width, height int, lookup func(title, year string, isTV bool) string) tea.Cmd {
	return func() tea.Msg {
		url := posterURLForItem(item, lookup)
		if url == "" {
			return posterFetchedMsg{id: item.ID, owner: owner, inline: poster.IsInlineImage()}
		}
		if poster.IsInlineImage() {
			b64, w, h, err := poster.FetchInlineImage(url)
			if err != nil {
				return posterFetchedMsg{id: item.ID, owner: owner, inline: true} // empty -> placeholder box
			}
			return posterFetchedMsg{id: item.ID, owner: owner, inline: true, b64: b64, imgW: w, imgH: h}
		}
		rendered := poster.RenderTUI(url, width, height)
		return posterFetchedMsg{id: item.ID, owner: owner, poster: rendered}
	}
}

// liveRow is one displayable Live TV item plus its description override.
type liveRow struct {
	result media.SearchResult
	desc   string
}

// fetchCategoriesCmd loads the Live TV category list.
func fetchCategoriesCmd(p *provider.LiveTV, gen int) tea.Cmd {
	return func() tea.Msg {
		cats, err := p.Categories()
		if err != nil {
			return errMsg{err}
		}
		rows := make([]liveRow, len(cats))
		for i, c := range cats {
			rows[i] = liveRow{result: c, desc: fmt.Sprintf("%d channels", c.Episodes)}
		}
		return liveItemsFetchedMsg{rows: rows, level: 0, title: "Live TV", gen: gen}
	}
}

// fetchChannelsCmd loads the channels in a Live TV category.
func fetchChannelsCmd(p *provider.LiveTV, category string, gen int) tea.Cmd {
	return func() tea.Msg {
		chans, err := p.Channels(category)
		if err != nil {
			return errMsg{err}
		}
		rows := make([]liveRow, len(chans))
		for i, c := range chans {
			rows[i] = liveRow{result: c, desc: category}
		}
		return liveItemsFetchedMsg{rows: rows, level: 1, title: category, gen: gen}
	}
}

// filterLiveRows returns rows whose title contains q (case-insensitive); an
// empty query returns the full slice.
func filterLiveRows(master []liveRow, q string) []liveRow {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return master
	}
	var out []liveRow
	for _, r := range master {
		if strings.Contains(strings.ToLower(r.result.Title), q) {
			out = append(out, r)
		}
	}
	return out
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

// stampResults tags the result list (or error) cmd yields with the results
// generation current when it was issued, so Update drops an answer the user
// has moved past by switching tabs or starting another search.
func (m AppModel) stampResults(cmd tea.Cmd) tea.Cmd {
	gen := m.resultsGen
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case resultsFetchedMsg:
			msg.gen = gen
			return msg
		case errMsg:
			return resultsErrMsg{err: msg.err, gen: gen}
		default:
			return msg
		}
	}
}
