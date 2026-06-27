package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lobster/internal/poster"
)

// posterDrawMsg carries a prepared overlay sequence to be written (after
// re-validation) from Update, so a draw scheduled for a now-stale state is
// dropped instead of painted.
type posterDrawMsg struct {
	seq string
	key string
}

// posterKey identifies the desired overlay state so identical redraws are
// skipped. It keys on the poster URL (a stable per-image identity, unlike the
// base64 length which could collide between different posters) plus the
// rendered dimensions, so a new image, a resize, or a visibility change all
// produce a different key.
func (m AppModel) posterKey() string {
	posterID := ""
	if m.currentItem != nil {
		posterID = m.currentItem.Poster
	}
	return fmt.Sprintf("%s:%d:%d:%d:%d:%t",
		posterID, m.posterImgW, m.posterImgH, m.width, m.height, m.posterVisible())
}

// redrawPoster computes the current header/tab heights and returns the overlay
// draw command (or nil when nothing should be drawn).
func (m AppModel) redrawPoster() tea.Cmd {
	hs, ts := m.buildHeaderTab()
	return m.redrawPosterCmd(lipgloss.Height(hs), lipgloss.Height(ts))
}

// redrawPosterCmd paints the inline poster into its reserved box after a
// one-frame debounce, so it lands after bubbletea has drawn the blank box. It
// writes through m.out (the syncWriter) so it never interleaves with frames.
func (m AppModel) redrawPosterCmd(headerH, tabBarH int) tea.Cmd {
	if m.out == nil || !m.posterVisible() || m.posterB64 == "" {
		return nil
	}
	lm := computeLayout(m.width, m.height, headerH, tabBarH, m.isSearching, m.posterImgW, m.posterImgH)
	seq := poster.PositionedImage(lm.bandRow, lm.bandCol, lm.posterCols, lm.posterRows, m.posterB64)
	key := m.posterKey()
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
		return posterDrawMsg{seq: seq, key: key}
	})
}
