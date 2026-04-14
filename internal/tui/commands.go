package tui

import (
	"lobster/internal/media"
	"lobster/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

// fetchTrendingCmd fetches trending movies.
func fetchTrendingCmd(p provider.Provider) tea.Cmd {
	return func() tea.Msg {
		results, err := p.Trending(media.Movie)
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
