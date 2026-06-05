package tui

import (
	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"
	"lobster/internal/media"
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

// searchCmd searches for items matching the query.
func searchCmd(p provider.Provider, query string) tea.Cmd {
	return func() tea.Msg {
		results, err := p.Search(query)
		if err != nil {
			return errMsg{err}
		}
		return resultsFetchedMsg(results)
	}
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
