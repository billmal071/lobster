package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/tui/downloads"
)

// State enumerated type
type state int

const (
	stateTrending state = iota
	stateSearch
	statePlaying
)

// tab identifies the active tab.
type tab int

const (
	tabMovies tab = iota
	tabSeries
	tabCartoons
	tabAnime
	tabDownloads
)

// AppModel struct holding state
type AppModel struct {
	state           state
	provider        provider.Provider
	cartoonProvider provider.Provider
	animeProvider   provider.Provider
	config          *config.Config

	list        list.Model
	searchInput textinput.Model
	loader      spinner.Model
	results     []media.SearchResult

	width  int
	height int

	currentItem   *media.SearchResult
	currentDetail *media.ContentDetail
	currentPoster string

	err              error
	isSearching      bool
	selectedResult   *media.SearchResult
	selectedProvider provider.Provider

	// Tab and download manager state
	activeTab      tab
	downloadsModel downloads.Model
	dlManager      *dlmanager.Manager
	toast          string // transient notification
}

// item adapter for list.Model
type listItem struct {
	result media.SearchResult
}

func (i listItem) Title() string { return i.result.Title }
func (i listItem) Description() string {
	desc := fmt.Sprintf("%s \u2022 %s", i.result.Type.String(), i.result.Year)
	if i.result.Type == media.TV {
		desc = fmt.Sprintf("%s \u2022 %d Seasons", desc, i.result.Seasons)
	}
	return desc
}
func (i listItem) FilterValue() string { return i.result.Title }

var (
	docStyle         = lipgloss.NewStyle().Margin(1, 2)
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF4C4C"))
	detailTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	detailLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

// StartApp launches the TUI. If mgr is nil, download features are disabled.
func StartApp(p provider.Provider, cfg *config.Config, mgr *dlmanager.Manager) (*media.SearchResult, provider.Provider, error) {
	ti := textinput.New()
	ti.Placeholder = "Search..."
	ti.CharLimit = 156
	ti.Width = 40

	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Movies"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false) // We handle custom filtering/search
	l.Styles.Title = lipgloss.NewStyle().MarginLeft(2).Bold(true).Foreground(lipgloss.Color("#FF0055"))

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF4C4C"))

	m := AppModel{
		state:           stateTrending,
		provider:        p,
		cartoonProvider: provider.NewKimCartoon("kimcartoon.com.co"),
		animeProvider:   provider.NewKimCartoon("kimcartoon.com.co"),
		config:          cfg,
		list:            l,
		searchInput:     ti,
		loader:          sp,
		dlManager:       mgr,
	}

	if mgr != nil {
		m.downloadsModel = downloads.New(mgr.Store(), mgr)
	}

	p2 := tea.NewProgram(m, tea.WithAltScreen())
	m2, err := p2.Run()
	if err != nil {
		return nil, nil, err
	}
	appModel := m2.(AppModel)
	if appModel.selectedProvider == nil {
		appModel.selectedProvider = p
	}
	return appModel.selectedResult, appModel.selectedProvider, nil
}

func (m AppModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		fetchTabCmd(m.providerForActiveTab(), m.activeTab),
		textinput.Blink,
		m.loader.Tick,
		m.list.StartSpinner(),
	}

	if m.dlManager != nil {
		cmds = append(cmds, listenProgressCmd(m.dlManager))
		cmds = append(cmds, m.downloadsModel.Init())
	}

	return tea.Batch(cmds...)
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.isSearching {
			switch msg.Type {
			case tea.KeyEnter:
				m.isSearching = false
				m.searchInput.Blur()
				m.list.Title = "Search: " + m.searchInput.Value()
				m.results = nil // Clear current results to show loader
				m.currentItem = nil
				cmds = append(cmds, m.list.StartSpinner())
				return m, tea.Batch(searchCmd(m.providerForActiveTab(), m.searchInput.Value()), m.list.StartSpinner())
			case tea.KeyEsc:
				m.isSearching = false
				m.searchInput.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}

		// Global keybindings (work on both tabs).
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			return m, m.switchTab(m.nextTab())
		case "1":
			return m, m.switchTab(tabMovies)
		case "2":
			return m, m.switchTab(tabSeries)
		case "3":
			return m, m.switchTab(tabCartoons)
		case "4":
			return m, m.switchTab(tabAnime)
		case "5":
			if m.dlManager != nil {
				return m, m.switchTab(tabDownloads)
			}
			return m, nil
		}

		// Route to active tab.
		if m.activeTab == tabDownloads {
			var cmd tea.Cmd
			m.downloadsModel, cmd = m.downloadsModel.Update(msg)
			return m, cmd
		}

		// Browse tab keybindings.
		switch msg.String() {
		case "s", "/":
			m.isSearching = true
			m.searchInput.Focus()
			m.searchInput.SetValue("")
			return m, nil
		case "enter":
			if m.currentItem != nil {
				m.selectedResult = m.currentItem
				m.selectedProvider = m.providerForActiveTab()
				return m, tea.Quit
			}
			return m, nil
		case "d":
			if m.dlManager != nil && m.currentItem != nil {
				return m, m.queueCurrentDownload()
			}
		}

	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.width = msg.Width - h
		m.height = msg.Height - v
		mainHeight := m.height - 10 // approximate header + footer
		if mainHeight < 0 {
			mainHeight = 0
		}
		m.downloadsModel.SetSize(m.width, mainHeight)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.loader, cmd = m.loader.Update(msg)
		cmds = append(cmds, cmd)

	case errMsg:
		m.err = msg.err
		m.list.StopSpinner()

	case resultsFetchedMsg:
		m.err = nil
		m.results = msg
		items := make([]list.Item, len(msg))
		for i, v := range msg {
			items[i] = listItem{result: v}
		}
		m.list.SetItems(items)
		m.list.StopSpinner()
		if len(msg) > 0 {
			m.currentItem = &msg[0]
			cmds = append(cmds, fetchDetailCmd(m.providerForActiveTab(), msg[0].ID))
			if msg[0].Poster != "" {
				pw, ph := m.posterSize()
				cmds = append(cmds, fetchPosterCmd(msg[0].ID, msg[0].Poster, pw, ph))
			}
		} else {
			m.currentItem = nil
			m.currentDetail = nil
		}

	case detailFetchedMsg:
		m.err = nil
		if m.currentItem != nil && m.currentItem.ID == msg.id {
			m.currentDetail = msg.detail
		}

	case posterFetchedMsg:
		if m.currentItem != nil && m.currentItem.ID == msg.id {
			m.currentPoster = msg.poster
		}

	case downloadProgressMsg:
		p := dlmanager.ProgressUpdate(msg)
		cmd := m.downloadsModel.UpdateProgress(p)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Re-subscribe to progress.
		if m.dlManager != nil {
			cmds = append(cmds, listenProgressCmd(m.dlManager))
		}

	case downloadQueuedMsg:
		m.toast = fmt.Sprintf("Queued: %s", msg.title)
		cmds = append(cmds, m.downloadsModel.Init())

	case downloadBatchQueuedMsg:
		m.toast = fmt.Sprintf("Queued %d episodes: %s", msg.count, msg.title)
		cmds = append(cmds, m.downloadsModel.Init())
	}

	// Update browse list and handle item change.
	if m.activeTab != tabDownloads {
		var listCmd tea.Cmd
		prevIndex := m.list.Index()
		m.list, listCmd = m.list.Update(msg)
		if !m.isSearching && len(m.results) > 0 && m.list.Index() != prevIndex {
			m.currentItem = &m.results[m.list.Index()]
			m.currentDetail = nil
			m.currentPoster = ""
			m.err = nil
			cmds = append(cmds, fetchDetailCmd(m.providerForActiveTab(), m.currentItem.ID))
			if m.currentItem.Poster != "" {
				pw, ph := m.posterSize()
				cmds = append(cmds, fetchPosterCmd(m.currentItem.ID, m.currentItem.Poster, pw, ph))
			}
		}
		cmds = append(cmds, listCmd)
	}

	// Always route messages to downloads model (for refresh, progress, etc.)
	if m.dlManager != nil {
		var dlCmd tea.Cmd
		m.downloadsModel, dlCmd = m.downloadsModel.Update(msg)
		if dlCmd != nil {
			cmds = append(cmds, dlCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// queueCurrentDownload queues the currently selected item for download.
func (m *AppModel) queueCurrentDownload() tea.Cmd {
	if m.currentItem == nil || m.dlManager == nil {
		return nil
	}
	item := *m.currentItem
	mgr := m.dlManager
	cfg := m.config

	return func() tea.Msg {
		outputDir, err := cfg.ExpandDownloadDir()
		if err != nil {
			return errMsg{err}
		}

		// For now, queue with metadata. Stream resolution happens at download time.
		d := &store.Download{
			Title:      item.Title,
			MediaTitle: item.Title,
			MediaType:  item.Type.String(),
			MediaID:    item.ID,
			OutputPath: outputDir + "/" + item.Title + ".mkv",
			Status:     "queued",
			StreamType: "hls", // Default; will be resolved by worker.
		}

		id, err := mgr.Queue(d)
		if err != nil {
			return errMsg{err}
		}
		return downloadQueuedMsg{downloadID: id, title: item.Title}
	}
}

func (m AppModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	headerASCII := `
    __    ____  ____  __________________
   / /   / __ \/ __ )/ ___/_  __/ ____/ __
  / /   / / / / __  |\__ \ / / / __/   / /
 / /___/ /_/ / /_/ /___/ // / / /___  /_/
/_____/\____/_____//____//_/ /_____/ (_) `

	header := headerStyle.Render(strings.TrimPrefix(headerASCII, "\n") + "\n  Terminal Media Streamer  \u2022  Search, Play, Download\n")

	// Tab bar
	tabBar := m.renderTabBar()

	// Dynamically calculate the main content area bounds
	mainHeight := m.height - lipgloss.Height(header) - lipgloss.Height(tabBar) - 3
	if mainHeight < 0 {
		mainHeight = 0
	}

	var mainContent string
	if m.activeTab == tabDownloads {
		m.downloadsModel.SetSize(m.width, mainHeight)
		mainContent = m.downloadsModel.View()
	} else {
		mainContent = m.renderBrowseContent(mainHeight)
	}

	footerStyle := lipgloss.NewStyle().
		MarginTop(1).
		Foreground(lipgloss.Color("#6272A4")).
		Background(lipgloss.Color("#282A36")).
		Padding(0, 2)

	var footer string
	if m.toast != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Render(m.toast)
	} else if m.err != nil {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Render(fmt.Sprintf("Error: %v", m.err))
	} else if m.activeTab == tabDownloads {
		footer = footerStyle.Render("[P] Pause/Resume  [X] Cancel  [R] Retry  [BS] Remove  [C] Clear  [Shift+R] Refresh  [TAB] Browse")
	} else {
		dlHint := ""
		if m.dlManager != nil {
			dlHint = "  [D] Download  "
		}
		footer = footerStyle.Render("[ENTER] Play" + dlHint + "[S] Search  [1-4] Categories  [TAB] Next Tab  [Q] Quit")
	}

	return docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabBar,
		mainContent,
		footer,
	))
}

func (m AppModel) renderTabBar() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#FF0055")).
		Padding(0, 2)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Padding(0, 2)

	labels := []struct {
		tab   tab
		label string
	}{
		{tabMovies, "1 Movies"},
		{tabSeries, "2 Series"},
		{tabCartoons, "3 Cartoons"},
		{tabAnime, "4 Anime"},
	}

	if m.dlManager != nil {
		labels = append(labels, struct {
			tab   tab
			label string
		}{tabDownloads, "5 Downloads"})
	}

	rendered := make([]string, 0, len(labels)*2)
	for _, item := range labels {
		if m.activeTab == item.tab {
			rendered = append(rendered, activeStyle.Render(item.label))
		} else {
			rendered = append(rendered, inactiveStyle.Render(item.label))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func (m AppModel) providerForActiveTab() provider.Provider {
	switch m.activeTab {
	case tabCartoons:
		return m.cartoonProvider
	case tabAnime:
		return m.animeProvider
	default:
		return m.provider
	}
}

func (m AppModel) nextTab() tab {
	switch m.activeTab {
	case tabMovies:
		return tabSeries
	case tabSeries:
		return tabCartoons
	case tabCartoons:
		return tabAnime
	case tabAnime:
		if m.dlManager != nil {
			return tabDownloads
		}
		return tabMovies
	default:
		return tabMovies
	}
}

func (m *AppModel) switchTab(next tab) tea.Cmd {
	if next == tabDownloads && m.dlManager == nil {
		return nil
	}

	m.activeTab = next
	m.isSearching = false
	m.searchInput.Blur()
	m.currentItem = nil
	m.currentDetail = nil
	m.err = nil

	if next == tabDownloads {
		m.downloadsModel.SetFocused(true)
		return m.downloadsModel.Init()
	}

	m.downloadsModel.SetFocused(false)
	m.results = nil
	m.list.SetItems(nil)
	m.list.Title = m.tabTitle(next)
	return tea.Batch(fetchTabCmd(m.providerForActiveTab(), next), m.list.StartSpinner())
}

func (m AppModel) tabTitle(t tab) string {
	switch t {
	case tabSeries:
		return "Series"
	case tabCartoons:
		return "Cartoons"
	case tabAnime:
		return "Anime"
	default:
		return "Movies"
	}
}

// posterSize returns responsive poster dimensions based on current terminal size.
func (m AppModel) posterSize() (cols, rows int) {
	rightWidth := m.width - m.width/3 - 4
	cols = rightWidth * 3 / 10
	if cols > 30 {
		cols = 30
	}
	if cols < 10 {
		cols = 10
	}
	rows = cols * 6 / 10
	if rows < 6 {
		rows = 6
	}
	return
}

func (m AppModel) renderBrowseContent(mainHeight int) string {
	m.list.SetSize(m.width/3, mainHeight)

	var leftPane string
	if m.isSearching {
		leftPane = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF79C6")).Bold(true).Render(" Search Query:"),
			m.searchInput.View(),
			"",
			m.list.View(),
		)
	} else {
		leftPane = m.list.View()
	}

	leftPaneStyle := lipgloss.NewStyle().
		Width(m.width/3).
		Height(mainHeight).
		Border(lipgloss.RoundedBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("#444444")).
		PaddingRight(2)

	leftPane = leftPaneStyle.Render(leftPane)

	var rightPane string
	rightWidth := m.width - m.width/3 - 4
	if m.currentItem != nil {
		// Compute text width accounting for poster
		textWidth := rightWidth - 4
		if m.currentPoster != "" {
			pw, _ := m.posterSize()
			textWidth = rightWidth - pw - 5
		}
		if textWidth < 20 {
			textWidth = 20
		}

		// Title + year
		titleStr := detailTitleStyle.Render(m.currentItem.Title)
		if m.currentItem.Year != "" {
			titleStr += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).Render("("+m.currentItem.Year+")")
		}

		// Type badge
		dot := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).Render(" • ")
		var typeStr string
		if m.currentItem.Type == media.TV {
			typeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#BD93F9")).Render("TV Series")
			if m.currentItem.Seasons > 0 {
				typeStr += dot + fmt.Sprintf("%d Seasons", m.currentItem.Seasons)
			}
			if m.currentItem.Episodes > 0 {
				typeStr += dot + fmt.Sprintf("%d Eps", m.currentItem.Episodes)
			}
		} else {
			typeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#BD93F9")).Render("Movie")
			if m.currentItem.Duration != "" {
				typeStr += dot + m.currentItem.Duration
			}
		}

		var extDetails string
		if m.currentDetail != nil {
			var parts []string

			// Rating
			if m.currentDetail.Rating != "" {
				parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#F1FA8C")).Render("★ "+m.currentDetail.Rating))
			}

			// Metadata
			labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
			if len(m.currentDetail.Genre) > 0 {
				parts = append(parts, labelStyle.Render("Genre:")+" "+strings.Join(m.currentDetail.Genre, ", "))
			}
			if len(m.currentDetail.Casts) > 0 {
				parts = append(parts, labelStyle.Render("Cast:")+" "+strings.Join(m.currentDetail.Casts, ", "))
			}

			// Description
			if m.currentDetail.Description != "" {
				desc := lipgloss.NewStyle().
					Width(textWidth).
					Foreground(lipgloss.Color("#BFBFBF")).
					MarginTop(1).
					Render(m.currentDetail.Description)
				parts = append(parts, desc)
			}

			extDetails = strings.Join(parts, "\n")
		} else {
			if m.err != nil {
				extDetails = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).MarginTop(1).Render(
					"Failed to load details. Scroll to retry.")
			} else {
				extDetails = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).MarginTop(1).Render(
					"Loading details...")
			}
		}

		rightPaneContent := lipgloss.JoinVertical(lipgloss.Left,
			titleStr,
			typeStr,
			"",
			extDetails,
		)

		rightPaneStyle := lipgloss.NewStyle().
			Width(rightWidth).
			Height(mainHeight).
			Padding(0, 2)

		if m.currentPoster != "" {
			pw, _ := m.posterSize()
			textWidth := rightWidth - pw - 5
			if textWidth < 20 {
				textWidth = 20
			}
			posterBlock := lipgloss.NewStyle().MarginRight(2).Render(m.currentPoster)
			textBlock := lipgloss.NewStyle().Width(textWidth).Render(rightPaneContent)
			rightPane = rightPaneStyle.Render(lipgloss.JoinHorizontal(lipgloss.Top, posterBlock, textBlock))
		} else {
			rightPane = rightPaneStyle.Render(rightPaneContent)
		}
	} else {
		if len(m.results) == 0 && m.err == nil {
			msg := fmt.Sprintf("\n\n  %s Fetching content...", m.loader.View())
			rightPane = lipgloss.NewStyle().Padding(2).Foreground(lipgloss.Color("#6272A4")).Render(msg)
		} else {
			rightPane = lipgloss.NewStyle().Padding(2).Foreground(lipgloss.Color("#6272A4")).Render("No selection")
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}
