package tui

import (
	"fmt"
	"os"
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
	"lobster/internal/poster"
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
	tabLiveTV
	tabDownloads
)

// AppModel struct holding state
type AppModel struct {
	state             state
	provider          provider.Provider
	cartoonProvider   provider.Provider
	animeProvider     provider.Provider
	fallbackProviders []provider.Provider
	config            *config.Config

	list        list.Model
	searchInput textinput.Model
	loader      spinner.Model
	results     []media.SearchResult

	width  int
	height int

	currentItem   *media.SearchResult
	currentDetail *media.ContentDetail
	currentPoster string
	posterReady   bool // an inline poster image is loaded and ready to overlay
	posterB64     string
	posterImgW    int
	posterImgH    int

	err              error
	isSearching      bool
	selectedResult   *media.SearchResult
	selectedProvider provider.Provider
	selectedLineup   []media.SearchResult // full Live TV category lineup for surf; nil otherwise
	selectedIndex    int                  // start index into selectedLineup

	// Tab and download manager state
	activeTab      tab
	downloadsModel downloads.Model
	dlManager      *dlmanager.Manager
	toast          string // transient notification

	// Live TV state
	liveTVProvider *provider.LiveTV
	liveLevel      int       // 0 = categories, 1 = channels
	liveCategory   string    // current category at level 1
	liveMaster     []liveRow // unfiltered rows for the current level

	// Download dialog for TV show batch downloads
	dlDialog downloadDialog

	out            *syncWriter // synchronized terminal output; nil in tests
	drawnPosterKey string      // posterKey of the last successfully painted overlay
}

// item adapter for list.Model
type listItem struct {
	result media.SearchResult
	desc   string // overrides Description() when non-empty (Live TV rows)
}

func (i listItem) Title() string { return i.result.Title }
func (i listItem) Description() string {
	if i.desc != "" {
		return i.desc
	}
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
func StartApp(p provider.Provider, cfg *config.Config, mgr *dlmanager.Manager, fallbacks ...provider.Provider) (*media.SearchResult, []media.SearchResult, int, provider.Provider, error) {
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
		state:             stateTrending,
		provider:          p,
		cartoonProvider:   provider.NewKimCartoon(provider.ResolveDomain("kimcartoon.com.co", "kimcartoon", cfg.DomainOverrides)),
		animeProvider:     provider.NewAllAnime(cfg.AnimeDub),
		liveTVProvider:    provider.NewLiveTV(cfg.LiveTV.Sources()),
		fallbackProviders: fallbacks,
		config:            cfg,
		list:              l,
		searchInput:       ti,
		loader:            sp,
		dlManager:         mgr,
		dlDialog:          newDownloadDialog(),
	}

	if mgr != nil {
		m.downloadsModel = downloads.New(mgr.Store(), mgr)
	}

	out := newSyncWriter(os.Stdout)
	m.out = out
	p2 := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out))
	m2, err := p2.Run()
	if err != nil {
		return nil, nil, 0, nil, err
	}
	appModel := m2.(AppModel)
	if appModel.selectedProvider == nil {
		appModel.selectedProvider = p
	}
	return appModel.selectedResult, appModel.selectedLineup, appModel.selectedIndex, appModel.selectedProvider, nil
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
	case dlSeasonsMsg, dlEpisodesMsg:
		handled, cmd := m.dlDialog.Update(msg)
		if handled && cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Route to download dialog when it's active
		if m.dlDialog.active {
			handled, cmd := m.dlDialog.Update(msg)
			if handled {
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				if !m.dlDialog.active {
					// dialog just closed — repaint the poster
					m.drawnPosterKey = ""
					cmds = append(cmds, m.redrawPoster())
				}
				return m, tea.Batch(cmds...)
			}
		}

		if m.isSearching {
			switch msg.Type {
			case tea.KeyEnter:
				m.isSearching = false
				m.searchInput.Blur()
				if m.activeTab == tabLiveTV {
					// Client-side filter of the current level; no network call.
					// An empty query restores the full list (filterLiveRows returns
					// the full master), so this is also the clear-filter path.
					q := m.searchInput.Value()
					if m.liveLevel == 1 && m.liveCategory != "" {
						m.list.Title = m.liveCategory
					} else {
						m.list.Title = "Live TV"
					}
					m.setLiveRows(filterLiveRows(m.liveMaster, q))
					return m, nil
				}
				m.list.Title = "Search: " + m.searchInput.Value()
				m.results = nil // Clear current results to show loader
				m.currentItem = nil
				cmds = append(cmds, m.list.StartSpinner())
				// Only the Movies/Series tabs fan out to the movies fallback
				// providers. Anime/Cartoons have their own dedicated provider, so
				// searching them must NOT wait on the (often slow/dead) movies
				// fallbacks — that was adding the multi-search timeout to every
				// anime search.
				fallbacks := m.fallbackProviders
				if m.activeTab == tabAnime || m.activeTab == tabCartoons {
					fallbacks = nil
				}
				return m, tea.Batch(searchCmd(m.providerForActiveTab(), m.searchInput.Value(), fallbacks...), m.list.StartSpinner())
			case tea.KeyEsc:
				m.isSearching = false
				m.searchInput.Blur()
				return m, m.redrawPoster()
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
			return m, m.switchTab(tabLiveTV)
		case "6":
			if m.dlManager != nil {
				return m, m.switchTab(tabDownloads)
			}
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
			m.drawnPosterKey = ""
			return m, tea.ClearScreen
		case "esc":
			if m.activeTab == tabLiveTV && m.liveLevel == 1 {
				m.liveCategory = ""
				return m, tea.Batch(fetchCategoriesCmd(m.liveTVProvider), m.list.StartSpinner())
			}
		case "enter":
			if m.activeTab == tabLiveTV {
				if m.currentItem == nil {
					return m, nil
				}
				if m.liveLevel == 0 {
					// Drill into the selected category; do NOT quit.
					cat := m.currentItem.ID
					m.liveCategory = cat
					return m, tea.Batch(fetchChannelsCmd(m.liveTVProvider, cat), m.list.StartSpinner())
				}
				// level 1: play the selected channel, with the full category
				// lineup so the play layer can surf past dead channels.
				lineup := make([]media.SearchResult, len(m.liveMaster))
				for idx, r := range m.liveMaster {
					lineup[idx] = r.result
				}
				startIdx := 0
				for idx := range lineup {
					if lineup[idx].ID == m.currentItem.ID {
						startIdx = idx
						break
					}
				}
				m.selectedResult = m.currentItem
				m.selectedProvider = m.liveTVProvider
				m.selectedLineup = lineup
				m.selectedIndex = startIdx
				return m, tea.Quit
			}
			if m.currentItem != nil {
				m.selectedResult = m.currentItem
				m.selectedProvider = m.providerForActiveTab()
				return m, tea.Quit
			}
			return m, nil
		case "d":
			if m.activeTab == tabAnime {
				if aa, ok := m.animeProvider.(*provider.AllAnime); ok {
					if aa.Translation() == "dub" {
						aa.SetTranslation("sub")
					} else {
						aa.SetTranslation("dub")
					}
					m.toast = "Anime audio: " + aa.Translation()
					m.results = nil
					m.currentItem = nil
					return m, tea.Batch(fetchTabCmd(m.providerForActiveTab(), m.activeTab), m.list.StartSpinner())
				}
			}
			if m.activeTab == tabLiveTV {
				m.toast = "Live channels can't be downloaded"
				return m, nil
			}
			if m.dlManager != nil && m.currentItem != nil {
				if m.currentItem.Type == media.TV {
					outputDir, err := m.config.ExpandDownloadDir()
					if err != nil {
						m.err = err
						return m, nil
					}
					cmd := m.dlDialog.start(*m.currentItem, m.providerForActiveTab(), m.dlManager, outputDir)
					m.drawnPosterKey = ""
					return m, cmd
				}
				return m, m.queueCurrentDownload()
			}
		}

	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.width = msg.Width - h
		m.height = msg.Height - v

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
		// Clear all selection-derived state so the previous title's metadata and
		// poster don't show under the new result until the fresh fetches land.
		m.currentDetail = nil
		m.currentPoster = ""
		m.posterReady = false
		m.posterB64 = ""
		m.posterImgW = 0
		m.posterImgH = 0
		m.drawnPosterKey = ""
		items := make([]list.Item, len(msg))
		for i, v := range msg {
			items[i] = listItem{result: v}
		}
		m.list.SetItems(items)
		m.list.StopSpinner()
		if len(msg) > 0 {
			m.currentItem = &msg[0]
			cmds = append(cmds, fetchDetailCmd(m.providerForActiveTab(), msg[0].ID))
			pw, ph := poster.BoxDims(m.width, 0, 0)
			cmds = append(cmds, fetchPosterForItemCmd(msg[0], pw, ph, m.posterLookup()))
		} else {
			m.currentItem = nil
			m.currentDetail = nil
		}

	case liveItemsFetchedMsg:
		m.list.StopSpinner()
		m.liveLevel = msg.level
		m.liveMaster = msg.rows
		m.list.Title = msg.title
		m.setLiveRows(msg.rows)
		return m, nil

	case detailFetchedMsg:
		m.err = nil
		if m.currentItem != nil && m.currentItem.ID == msg.id {
			m.currentDetail = msg.detail
		}

	case posterFetchedMsg:
		if m.currentItem != nil && m.currentItem.ID == msg.id {
			if msg.inline {
				m.posterB64 = msg.b64
				m.posterImgW = msg.imgW
				m.posterImgH = msg.imgH
				m.posterReady = msg.b64 != ""
				m.currentPoster = "" // inline path never renders art in View()
			} else {
				m.currentPoster = msg.poster
				m.posterReady = false
			}
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

	case posterDrawMsg:
		// Re-validate before writing: drop draws scheduled for a stale state.
		if m.out != nil && m.posterVisible() && msg.key == m.posterKey() {
			m.out.WriteString(msg.seq)
			m.drawnPosterKey = msg.key
		}
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
			m.posterReady = false
			m.posterB64 = ""
			m.err = nil
			cmds = append(cmds, fetchDetailCmd(m.providerForActiveTab(), m.currentItem.ID))
			pw, ph := poster.BoxDims(m.width, 0, 0)
			cmds = append(cmds, fetchPosterForItemCmd(*m.currentItem, pw, ph, m.posterLookup()))
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

	// Redraw the inline poster overlay only when its desired state changed.
	if m.posterVisible() {
		if k := m.posterKey(); k != m.drawnPosterKey {
			m.drawnPosterKey = k // optimistic; the posterDrawMsg handler confirms
			cmds = append(cmds, m.redrawPoster())
		}
	} else {
		m.drawnPosterKey = ""
	}

	return m, tea.Batch(cmds...)
}

// setLiveRows populates the list + m.results from live rows, keeping them
// index-aligned (selection reads m.results[m.list.Index()]).
func (m *AppModel) setLiveRows(rows []liveRow) {
	m.results = make([]media.SearchResult, len(rows))
	items := make([]list.Item, len(rows))
	for i, r := range rows {
		m.results[i] = r.result
		items[i] = listItem{result: r.result, desc: r.desc}
	}
	m.list.SetItems(items)
	if len(m.results) > 0 {
		m.currentItem = &m.results[0]
	} else {
		m.currentItem = nil
	}
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

// buildHeaderTab constructs the header banner and tab bar strings that appear
// at the top of every view. It is extracted so both View and redrawPoster can
// measure their heights consistently.
func (m AppModel) buildHeaderTab() (string, string) {
	headerASCII := `
    __    ____  ____  __________________
   / /   / __ \/ __ )/ ___/_  __/ ____/ __
  / /   / / / / __  |\__ \ / / / __/   / /
 / /___/ /_/ / /_/ /___/ // / / /___  /_/
/_____/\____/_____//____//_/ /_____/ (_) `

	header := headerStyle.Render(strings.TrimPrefix(headerASCII, "\n") + "\n  Terminal Media Streamer  \u2022  Search, Play, Download\n")
	tabBar := m.renderTabBar()
	return header, tabBar
}

func (m AppModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	header, tabBar := m.buildHeaderTab()

	headerH := lipgloss.Height(header)
	tabBarH := lipgloss.Height(tabBar)
	lm := computeLayout(m.width, m.height, headerH, tabBarH, m.isSearching, m.posterImgW, m.posterImgH)

	var mainContent string
	if m.activeTab == tabDownloads {
		m.downloadsModel.SetSize(m.width, lm.mainHeight)
		mainContent = m.downloadsModel.View()
	} else {
		mainContent = m.renderBrowseContent(headerH, tabBarH)
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
		if m.dlManager != nil && m.activeTab != tabLiveTV {
			dlHint = "  [D] Download  "
		}
		footer = footerStyle.Render("[ENTER] Play" + dlHint + "[S] Search  [1-6] Categories  [TAB] Next Tab  [Q] Quit")
	}

	base := docStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabBar,
		mainContent,
		footer,
	))

	// Overlay the download dialog when active
	if m.dlDialog.active {
		return m.dlDialog.View(m.width, m.height)
	}

	return base
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
		{tabLiveTV, "5 Live TV"},
	}

	if m.dlManager != nil {
		labels = append(labels, struct {
			tab   tab
			label string
		}{tabDownloads, "6 Downloads"})
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
	case tabLiveTV:
		return m.liveTVProvider
	default:
		return m.provider
	}
}

// posterLookup returns the poster-enrichment policy for the active tab: a TMDB
// high-res upgrade on the Movies/Series (FlixHQ) tabs, and none elsewhere so
// Cartoons/Anime keep their own posters and skip needless TMDB lookups.
func (m AppModel) posterLookup() func(title, year string, isTV bool) string {
	if m.activeTab == tabMovies || m.activeTab == tabSeries {
		return provider.TMDBPoster
	}
	return func(string, string, bool) string { return "" }
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
		return tabLiveTV
	case tabLiveTV:
		if m.dlManager != nil {
			return tabDownloads
		}
		return tabMovies
	case tabDownloads:
		return tabMovies
	default:
		return tabMovies
	}
}

func (m *AppModel) switchTab(next tab) tea.Cmd {
	m.drawnPosterKey = ""
	m.posterReady = false
	m.posterB64 = ""
	if next == tabDownloads && m.dlManager == nil {
		return tea.ClearScreen
	}

	m.activeTab = next
	m.isSearching = false
	m.searchInput.Blur()
	m.currentItem = nil
	m.currentDetail = nil
	m.err = nil

	if next == tabDownloads {
		m.downloadsModel.SetFocused(true)
		return tea.Batch(m.downloadsModel.Init(), tea.ClearScreen)
	}

	m.downloadsModel.SetFocused(false)
	m.results = nil
	m.list.SetItems(nil)
	m.list.Title = m.tabTitle(next)
	if next == tabLiveTV {
		m.liveLevel = 0
		m.liveCategory = ""
		return tea.Batch(fetchCategoriesCmd(m.liveTVProvider), m.list.StartSpinner(), tea.ClearScreen)
	}
	return tea.Batch(fetchTabCmd(m.providerForActiveTab(), next), m.list.StartSpinner(), tea.ClearScreen)
}

func (m AppModel) tabTitle(t tab) string {
	switch t {
	case tabSeries:
		return "Series"
	case tabCartoons:
		return "Cartoons"
	case tabAnime:
		return "Anime"
	case tabLiveTV:
		return "Live TV"
	default:
		return "Movies"
	}
}

func (m AppModel) renderBrowseContent(headerH, tabBarH int) string {
	lm := computeLayout(m.width, m.height, headerH, tabBarH, m.isSearching, m.posterImgW, m.posterImgH)

	// ----- Hero band (poster box + detail text), full width -----
	band := m.renderHeroBand(lm)

	// ----- Results list, full width below the band -----
	// Size the list from the band's ACTUAL height (it may have grown to fit the
	// detail text), not the poster-only estimate in lm.
	lh := lm.mainHeight - lipgloss.Height(band)
	if m.isSearching {
		lh -= searchHeaderRows // search label + input + spacer rendered above the list
	}
	if lh < 0 {
		lh = 0
	}
	m.list.SetSize(lm.listWidth, lh)
	var listView string
	if m.isSearching {
		listView = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FF79C6")).Bold(true).Render(" Search Query:"),
			m.searchInput.View(),
			"",
			m.list.View(),
		)
	} else {
		listView = m.list.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, band, listView)
}

func (m AppModel) renderHeroBand(lm layoutMetrics) string {
	bandStyle := lipgloss.NewStyle().
		Width(m.width-2*bandBorder).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#444444")).
		Padding(bandPadV, bandPadH)

	if m.currentItem == nil {
		msg := "No selection"
		if len(m.results) == 0 && m.err == nil {
			msg = fmt.Sprintf("%s Fetching content...", m.loader.View())
		}
		return bandStyle.Height(lm.posterRows).Render(lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).Render(msg))
	}

	text := m.renderDetailText(lm.textWidth)

	// Grow the band so the detail text (incl. description) is fully shown, but
	// never far enough to starve the results list below minListRows. The poster
	// box stays posterRows tall and top-aligned, so the inline-image overlay
	// position is unaffected.
	innerRows := lm.posterRows
	if th := lipgloss.Height(text); th > innerRows {
		innerRows = th
	}
	maxInner := lm.mainHeight - minListRows - 2*bandBorder
	if maxInner < lm.posterRows {
		maxInner = lm.posterRows
	}
	if innerRows > maxInner {
		innerRows = maxInner
	}

	// Build the poster column.
	var posterCol string
	if poster.IsInlineImage() {
		// Blank box; the OSC-1337 image is painted out-of-band over these cells.
		posterCol = lipgloss.NewStyle().
			Width(lm.posterCols).
			Height(lm.posterRows).
			Render("")
	} else if m.currentPoster != "" {
		posterCol = m.currentPoster
	} else {
		posterCol = lipgloss.NewStyle().Width(lm.posterCols).Height(lm.posterRows).Render("")
	}

	inner := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lm.posterCols).Render(posterCol),
		lipgloss.NewStyle().Width(bandGap).Render(""),
		lipgloss.NewStyle().Width(lm.textWidth).Render(text),
	)
	return bandStyle.Height(innerRows).Render(inner)
}

func (m AppModel) renderDetailText(textWidth int) string {
	titleStr := detailTitleStyle.Render(m.currentItem.Title)
	if m.currentItem.Year != "" {
		titleStr += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).Render("("+m.currentItem.Year+")")
	}
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
		if m.currentDetail.Rating != "" {
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#F1FA8C")).Render("★ "+m.currentDetail.Rating))
		}
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
		if len(m.currentDetail.Genre) > 0 {
			parts = append(parts, labelStyle.Render("Genre:")+" "+strings.Join(m.currentDetail.Genre, ", "))
		}
		if len(m.currentDetail.Casts) > 0 {
			parts = append(parts, labelStyle.Render("Cast:")+" "+strings.Join(m.currentDetail.Casts, ", "))
		}
		if m.currentDetail.Description != "" {
			desc := lipgloss.NewStyle().Width(textWidth).Foreground(lipgloss.Color("#BFBFBF")).MarginTop(1).Render(m.currentDetail.Description)
			parts = append(parts, desc)
		}
		extDetails = strings.Join(parts, "\n")
	} else if m.err != nil {
		extDetails = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).MarginTop(1).Render("Failed to load details. Scroll to retry.")
	} else {
		extDetails = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4")).MarginTop(1).Render("Loading details...")
	}

	return lipgloss.JoinVertical(lipgloss.Left, titleStr, typeStr, "", extDetails)
}
