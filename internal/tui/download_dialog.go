package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"
	"lobster/internal/media"
	"lobster/internal/provider"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dlStep tracks which step of the download dialog we're on.
type dlStep int

const (
	dlStepNone        dlStep = iota
	dlStepLoadSeasons        // loading seasons
	dlStepPickSeason         // user picks a season
	dlStepLoadEpisodes       // loading episodes
	dlStepPickMode           // one / all / range
	dlStepPickEpisode        // user picks a single episode
	dlStepRangeInput         // user types a range like "1-5,8"
)

// downloadDialog holds the state for the TV show download overlay.
type downloadDialog struct {
	active   bool
	step     dlStep
	item     media.SearchResult
	provider provider.Provider
	manager  *dlmanager.Manager
	outputDir string

	seasons  []media.Season
	episodes []media.Episode

	seasonCursor  int
	modeCursor    int
	episodeCursor int

	rangeInput textinput.Model

	err error
}

var modeLabels = []string{
	"Download one episode",
	"Download all episodes",
	"Download range (e.g. 1-5,8)",
}

func newDownloadDialog() downloadDialog {
	ri := textinput.New()
	ri.Placeholder = "e.g. 1-5,8,10-12"
	ri.CharLimit = 64
	ri.Width = 30
	return downloadDialog{rangeInput: ri}
}

// start initiates the download dialog for a TV show.
func (d *downloadDialog) start(item media.SearchResult, p provider.Provider, mgr *dlmanager.Manager, outputDir string) tea.Cmd {
	d.active = true
	d.step = dlStepLoadSeasons
	d.item = item
	d.provider = p
	d.manager = mgr
	d.outputDir = outputDir
	d.seasons = nil
	d.episodes = nil
	d.seasonCursor = 0
	d.modeCursor = 0
	d.episodeCursor = 0
	d.err = nil

	prov := p
	id := item.ID
	return func() tea.Msg {
		seasons, err := prov.GetSeasons(id)
		if err != nil {
			return dlSeasonsMsg{err: err}
		}
		return dlSeasonsMsg{seasons: seasons}
	}
}

func (d *downloadDialog) cancel() {
	d.active = false
	d.step = dlStepNone
	d.err = nil
}

// --- Messages ---

type dlSeasonsMsg struct {
	seasons []media.Season
	err     error
}

type dlEpisodesMsg struct {
	episodes []media.Episode
	err      error
}

// --- Update ---

func (d *downloadDialog) Update(msg tea.Msg) (bool, tea.Cmd) {
	if !d.active {
		return false, nil
	}

	switch msg := msg.(type) {
	case dlSeasonsMsg:
		if msg.err != nil {
			d.err = msg.err
			return true, nil
		}
		d.seasons = msg.seasons
		if len(d.seasons) == 1 {
			// Skip season selection if only one season
			d.step = dlStepLoadEpisodes
			return true, d.fetchEpisodes(0)
		}
		d.step = dlStepPickSeason
		return true, nil

	case dlEpisodesMsg:
		if msg.err != nil {
			d.err = msg.err
			return true, nil
		}
		d.episodes = msg.episodes
		d.step = dlStepPickMode
		return true, nil

	case tea.KeyMsg:
		return d.handleKey(msg)
	}

	// Forward to range input if active
	if d.step == dlStepRangeInput {
		var cmd tea.Cmd
		d.rangeInput, cmd = d.rangeInput.Update(msg)
		return true, cmd
	}

	return true, nil
}

func (d *downloadDialog) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	key := msg.String()

	// Escape always cancels
	if key == "esc" {
		d.cancel()
		return true, nil
	}

	// Error state: any key dismisses
	if d.err != nil {
		d.cancel()
		return true, nil
	}

	switch d.step {
	case dlStepLoadSeasons, dlStepLoadEpisodes:
		// Loading, ignore keys except esc
		return true, nil

	case dlStepPickSeason:
		return d.handlePickSeason(key)

	case dlStepPickMode:
		return d.handlePickMode(key)

	case dlStepPickEpisode:
		return d.handlePickEpisode(key)

	case dlStepRangeInput:
		return d.handleRangeInput(msg)
	}

	return true, nil
}

func (d *downloadDialog) handlePickSeason(key string) (bool, tea.Cmd) {
	switch key {
	case "up", "k":
		if d.seasonCursor > 0 {
			d.seasonCursor--
		}
	case "down", "j":
		if d.seasonCursor < len(d.seasons)-1 {
			d.seasonCursor++
		}
	case "enter":
		d.step = dlStepLoadEpisodes
		return true, d.fetchEpisodes(d.seasonCursor)
	}
	return true, nil
}

func (d *downloadDialog) handlePickMode(key string) (bool, tea.Cmd) {
	switch key {
	case "up", "k":
		if d.modeCursor > 0 {
			d.modeCursor--
		}
	case "down", "j":
		if d.modeCursor < len(modeLabels)-1 {
			d.modeCursor++
		}
	case "enter":
		switch d.modeCursor {
		case 0: // one episode
			d.step = dlStepPickEpisode
			d.episodeCursor = 0
		case 1: // all episodes
			return true, d.queueEpisodes(d.episodes)
		case 2: // range
			d.step = dlStepRangeInput
			d.rangeInput.SetValue("")
			d.rangeInput.Focus()
			return true, textinput.Blink
		}
	}
	return true, nil
}

func (d *downloadDialog) handlePickEpisode(key string) (bool, tea.Cmd) {
	switch key {
	case "up", "k":
		if d.episodeCursor > 0 {
			d.episodeCursor--
		}
	case "down", "j":
		if d.episodeCursor < len(d.episodes)-1 {
			d.episodeCursor++
		}
	case "enter":
		ep := d.episodes[d.episodeCursor]
		return true, d.queueEpisodes([]media.Episode{ep})
	}
	return true, nil
}

func (d *downloadDialog) handleRangeInput(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		matched, err := parseEpisodeRange(d.rangeInput.Value(), d.episodes)
		if err != nil {
			d.err = fmt.Errorf("invalid range: %w", err)
			return true, nil
		}
		if len(matched) == 0 {
			d.err = fmt.Errorf("no episodes matched the range")
			return true, nil
		}
		return true, d.queueEpisodes(matched)
	case tea.KeyEsc:
		d.step = dlStepPickMode
		d.rangeInput.Blur()
		return true, nil
	default:
		var cmd tea.Cmd
		d.rangeInput, cmd = d.rangeInput.Update(msg)
		return true, cmd
	}
}

func (d *downloadDialog) fetchEpisodes(seasonIdx int) tea.Cmd {
	prov := d.provider
	id := d.item.ID
	seasonID := d.seasons[seasonIdx].ID
	return func() tea.Msg {
		episodes, err := prov.GetEpisodes(id, seasonID)
		if err != nil {
			return dlEpisodesMsg{err: err}
		}
		return dlEpisodesMsg{episodes: episodes}
	}
}

func (d *downloadDialog) queueEpisodes(episodes []media.Episode) tea.Cmd {
	mgr := d.manager
	item := d.item
	outputDir := d.outputDir
	seasonNum := 0
	if d.seasonCursor < len(d.seasons) {
		seasonNum = d.seasons[d.seasonCursor].Number
	}

	dls := make([]*store.Download, 0, len(episodes))
	for _, ep := range episodes {
		epLabel := fmt.Sprintf("S%02dE%02d", seasonNum, ep.Number)
		if ep.Title != "" {
			epLabel += " - " + ep.Title
		}
		dls = append(dls, &store.Download{
			Title:      fmt.Sprintf("%s %s", item.Title, epLabel),
			MediaTitle: item.Title,
			MediaType:  item.Type.String(),
			MediaID:    item.ID,
			EpisodeID:  ep.ID,
			Season:     seasonNum,
			Episode:    ep.Number,
			OutputPath: fmt.Sprintf("%s/%s/Season %02d/%s.mkv", outputDir, item.Title, seasonNum, epLabel),
			Status:     "queued",
			StreamType: "hls",
		})
	}

	d.cancel()

	if len(dls) == 1 {
		return queueDownloadCmd(mgr, dls[0])
	}
	return queueSeasonCmd(mgr, dls)
}

// --- parseEpisodeRange (same logic as cmd/batch.go) ---

func parseEpisodeRange(input string, episodes []media.Episode) ([]media.Episode, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty range")
	}

	byNum := make(map[int]media.Episode)
	for _, ep := range episodes {
		byNum[ep.Number] = ep
	}

	requested := make(map[int]bool)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, "-"); idx >= 0 {
			startStr := strings.TrimSpace(part[:idx])
			endStr := strings.TrimSpace(part[idx+1:])
			start, err := strconv.Atoi(startStr)
			if err != nil {
				return nil, fmt.Errorf("invalid range start %q", startStr)
			}
			end, err := strconv.Atoi(endStr)
			if err != nil {
				return nil, fmt.Errorf("invalid range end %q", endStr)
			}
			if start > end {
				return nil, fmt.Errorf("invalid range: %d > %d", start, end)
			}
			for n := start; n <= end; n++ {
				requested[n] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid episode number %q", part)
			}
			requested[n] = true
		}
	}

	if len(requested) == 0 {
		return nil, fmt.Errorf("no episode numbers in range")
	}

	var matched []media.Episode
	for num := range requested {
		if ep, ok := byNum[num]; ok {
			matched = append(matched, ep)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Number < matched[j].Number
	})

	return matched, nil
}

// --- View ---

func (d *downloadDialog) View(width, height int) string {
	if !d.active {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF0055"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F8F8F2"))

	var content string

	switch d.step {
	case dlStepLoadSeasons:
		content = labelStyle.Render("Loading seasons...")

	case dlStepPickSeason:
		var lines []string
		lines = append(lines, titleStyle.Render("Select Season"))
		lines = append(lines, "")
		for i, s := range d.seasons {
			label := fmt.Sprintf("Season %d", s.Number)
			if i == d.seasonCursor {
				lines = append(lines, selectedStyle.Render("> "+label))
			} else {
				lines = append(lines, normalStyle.Render("  "+label))
			}
		}
		content = strings.Join(lines, "\n")

	case dlStepLoadEpisodes:
		content = labelStyle.Render("Loading episodes...")

	case dlStepPickMode:
		var lines []string
		lines = append(lines, titleStyle.Render(fmt.Sprintf("Download: %s", d.item.Title)))
		if d.seasonCursor < len(d.seasons) {
			lines = append(lines, labelStyle.Render(fmt.Sprintf("Season %d - %d episodes", d.seasons[d.seasonCursor].Number, len(d.episodes))))
		}
		lines = append(lines, "")
		for i, label := range modeLabels {
			if i == d.modeCursor {
				lines = append(lines, selectedStyle.Render("> "+label))
			} else {
				lines = append(lines, normalStyle.Render("  "+label))
			}
		}
		content = strings.Join(lines, "\n")

	case dlStepPickEpisode:
		var lines []string
		lines = append(lines, titleStyle.Render("Select Episode"))
		lines = append(lines, "")
		// Show a scrollable window of episodes
		windowSize := height - 6
		if windowSize < 5 {
			windowSize = 5
		}
		start := d.episodeCursor - windowSize/2
		if start < 0 {
			start = 0
		}
		end := start + windowSize
		if end > len(d.episodes) {
			end = len(d.episodes)
			start = end - windowSize
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			ep := d.episodes[i]
			label := fmt.Sprintf("E%02d", ep.Number)
			if ep.Title != "" {
				label += " - " + ep.Title
			}
			if i == d.episodeCursor {
				lines = append(lines, selectedStyle.Render("> "+label))
			} else {
				lines = append(lines, normalStyle.Render("  "+label))
			}
		}
		content = strings.Join(lines, "\n")

	case dlStepRangeInput:
		var lines []string
		lines = append(lines, titleStyle.Render("Episode Range"))
		if len(d.episodes) > 0 {
			lines = append(lines, labelStyle.Render(fmt.Sprintf("Available: 1-%d", d.episodes[len(d.episodes)-1].Number)))
		}
		lines = append(lines, "")
		lines = append(lines, d.rangeInput.View())
		content = strings.Join(lines, "\n")
	}

	if d.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555"))
		content = errStyle.Render(fmt.Sprintf("Error: %v\n\nPress any key to dismiss.", d.err))
	}

	footer := labelStyle.Render("[ESC] Cancel  [ENTER] Select  [j/k] Navigate")

	boxWidth := width / 2
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 60 {
		boxWidth = 60
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#FF0055")).
		Padding(1, 2).
		Width(boxWidth).
		Render(content + "\n\n" + footer)

	// Center the dialog
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
