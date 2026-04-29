// Package downloads provides the Bubbletea model for the Downloads tab.
package downloads

import (
	"lobster/internal/dlmanager"
	"lobster/internal/dlmanager/store"

	tea "github.com/charmbracelet/bubbletea"
)

// Model is the Bubbletea model for the Downloads tab.
type Model struct {
	store     *store.Store
	manager   *dlmanager.Manager
	downloads []store.Download
	progress  map[int]dlmanager.ProgressUpdate // live progress by download ID
	selected  int
	width     int
	height    int
	focused   bool
}

// New creates a new Downloads tab model.
func New(s *store.Store, m *dlmanager.Manager) Model {
	return Model{
		store:    s,
		manager:  m,
		progress: make(map[int]dlmanager.ProgressUpdate),
	}
}

// Init returns no initial command.
func (m Model) Init() tea.Cmd {
	return m.refresh()
}

// SetSize sets the available rendering area.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetFocused sets whether this tab is active.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
}

// UpdateProgress applies a progress update from the download manager.
func (m *Model) UpdateProgress(p dlmanager.ProgressUpdate) tea.Cmd {
	m.progress[p.DownloadID] = p

	// If status changed (completed, failed, paused), refresh the full list.
	if p.Status == "completed" || p.Status == "failed" || p.Status == "paused" {
		delete(m.progress, p.DownloadID)
		return m.refresh()
	}
	return nil
}

// Update handles messages for the downloads tab.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if !m.focused {
			return m, nil
		}
		return m.handleKey(msg)

	case refreshedMsg:
		m.downloads = msg
		if m.selected >= len(m.downloads) {
			m.selected = max(0, len(m.downloads)-1)
		}
		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.selected < len(m.downloads)-1 {
			m.selected++
		}
	case "k", "up":
		if m.selected > 0 {
			m.selected--
		}
	case "p":
		return m, m.togglePause()
	case "x":
		return m, m.cancelSelected()
	case "r":
		return m, m.retrySelected()
	case "backspace":
		return m, m.removeSelected()
	case "c":
		return m, m.clearCompleted()
	}
	return m, nil
}

func (m *Model) togglePause() tea.Cmd {
	if m.selected >= len(m.downloads) {
		return nil
	}
	dl := m.downloads[m.selected]
	return func() tea.Msg {
		switch dl.Status {
		case "downloading", "queued":
			m.manager.Pause(dl.ID)
		case "paused":
			m.manager.Resume(dl.ID)
		}
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) cancelSelected() tea.Cmd {
	if m.selected >= len(m.downloads) {
		return nil
	}
	dl := m.downloads[m.selected]
	return func() tea.Msg {
		m.manager.Cancel(dl.ID)
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) retrySelected() tea.Cmd {
	if m.selected >= len(m.downloads) {
		return nil
	}
	dl := m.downloads[m.selected]
	if dl.Status != "failed" {
		return nil
	}
	return func() tea.Msg {
		m.manager.Retry(dl.ID)
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) removeSelected() tea.Cmd {
	if m.selected >= len(m.downloads) {
		return nil
	}
	dl := m.downloads[m.selected]
	return func() tea.Msg {
		m.manager.Remove(dl.ID)
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) clearCompleted() tea.Cmd {
	return func() tea.Msg {
		m.store.ClearCompleted()
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg {
		return refreshedMsg(m.loadDownloads())
	}
}

func (m *Model) loadDownloads() []store.Download {
	list, err := m.store.ListDownloads()
	if err != nil {
		return nil
	}
	return list
}

// refreshedMsg carries an updated download list.
type refreshedMsg []store.Download
