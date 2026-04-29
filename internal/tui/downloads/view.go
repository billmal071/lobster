package downloads

import (
	"fmt"
	"strings"

	"lobster/internal/dlmanager"

	"github.com/charmbracelet/lipgloss"
)

var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))
	purpleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#BD93F9"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555"))
	grayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1FA8C"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

// View renders the Downloads tab.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	if len(m.downloads) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Padding(2).
			Render("No downloads yet.\n\nPress [d] on any title in Browse to start downloading.\nPress [D] to download an entire season.")
		return empty
	}

	leftWidth := m.width / 3
	rightWidth := m.width - leftWidth - 4

	leftPane := m.renderList(leftWidth, m.height)
	rightPane := m.renderDetail(rightWidth, m.height)

	leftPaneStyle := lipgloss.NewStyle().
		Width(leftWidth).
		Height(m.height).
		Border(lipgloss.RoundedBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("#444444")).
		PaddingRight(2)

	rightPaneStyle := lipgloss.NewStyle().
		Width(rightWidth).
		Height(m.height).
		Padding(0, 2)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		leftPaneStyle.Render(leftPane),
		rightPaneStyle.Render(rightPane),
	)
}

func (m Model) renderList(width, height int) string {
	var lines []string

	// Group by status.
	var active, completed, failed []int
	for i, dl := range m.downloads {
		switch dl.Status {
		case "downloading", "queued", "paused", "pending":
			active = append(active, i)
		case "completed":
			completed = append(completed, i)
		case "failed":
			failed = append(failed, i)
		}
	}

	if len(active) > 0 {
		lines = append(lines, purpleStyle.Bold(true).Render(" Active"))
		for _, i := range active {
			lines = append(lines, m.renderListItem(i, width))
		}
	}

	if len(failed) > 0 {
		lines = append(lines, "")
		lines = append(lines, redStyle.Bold(true).Render(" Failed"))
		for _, i := range failed {
			lines = append(lines, m.renderListItem(i, width))
		}
	}

	if len(completed) > 0 {
		lines = append(lines, "")
		lines = append(lines, grayStyle.Bold(true).Render(" Completed"))
		for _, i := range completed {
			lines = append(lines, m.renderListItem(i, width))
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderListItem(idx, width int) string {
	dl := m.downloads[idx]
	cursor := "  "
	if idx == m.selected {
		cursor = "> "
	}

	statusIcon := statusIcon(dl.Status)
	title := truncate(dl.Title, width-10)

	line := fmt.Sprintf("%s%s %s", cursor, statusIcon, title)

	// Add progress bar for active downloads.
	if dl.Status == "downloading" {
		if p, ok := m.progress[dl.ID]; ok {
			bar := renderProgressBar(p, width-4)
			line += "\n    " + bar
		}
	}

	style := lipgloss.NewStyle()
	if idx == m.selected {
		style = style.Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	}

	return style.Render(line)
}

func (m Model) renderDetail(width, height int) string {
	if m.selected >= len(m.downloads) {
		return grayStyle.Render("No selection")
	}

	dl := m.downloads[m.selected]
	var lines []string

	lines = append(lines, titleStyle.Render(dl.Title))

	if dl.MediaType == "tv" {
		lines = append(lines, purpleStyle.Render(
			fmt.Sprintf("S%02dE%02d - %s", dl.Season, dl.Episode, dl.MediaTitle)))
	} else {
		lines = append(lines, purpleStyle.Render(dl.MediaTitle))
	}

	lines = append(lines, "")

	// Status with color.
	statusStr := statusStyled(dl.Status)
	lines = append(lines, labelStyle.Render("Status:   ")+statusStr)

	// Progress.
	if p, ok := m.progress[dl.ID]; ok {
		if p.TotalBytes > 0 {
			lines = append(lines, labelStyle.Render("Progress: ")+
				fmt.Sprintf("%s / %s", formatBytes(p.DoneBytes), formatBytes(p.TotalBytes)))
		}
		if p.TotalSegments > 0 {
			lines = append(lines, labelStyle.Render("Segments: ")+
				fmt.Sprintf("%d / %d", p.DoneSegments, p.TotalSegments))
		}
		if p.Speed > 0 {
			lines = append(lines, labelStyle.Render("Speed:    ")+formatSpeed(p.Speed))
			remaining := float64(0)
			if p.TotalBytes > 0 && p.Speed > 0 {
				remaining = float64(p.TotalBytes-p.DoneBytes) / p.Speed
			} else if p.TotalSegments > 0 && p.Speed > 0 {
				remaining = float64(p.TotalSegments-p.DoneSegments) / p.Speed
			}
			if remaining > 0 {
				lines = append(lines, labelStyle.Render("ETA:      ")+formatDuration(remaining))
			}
		}
	} else {
		// Show stored progress for paused/completed.
		if dl.TotalBytes > 0 {
			lines = append(lines, labelStyle.Render("Progress: ")+
				fmt.Sprintf("%s / %s", formatBytes(dl.DoneBytes), formatBytes(dl.TotalBytes)))
		}
		if dl.TotalSegments > 0 {
			lines = append(lines, labelStyle.Render("Segments: ")+
				fmt.Sprintf("%d / %d", dl.DoneSegments, dl.TotalSegments))
		}
	}

	lines = append(lines, "")
	lines = append(lines, labelStyle.Render("Type:     ")+dl.StreamType)
	lines = append(lines, labelStyle.Render("Output:   ")+truncate(dl.OutputPath, width-12))

	if dl.SubtitleURL != "" {
		lines = append(lines, labelStyle.Render("Subs:     ")+"Yes")
	}

	if dl.Error != "" {
		lines = append(lines, "")
		lines = append(lines, redStyle.Render("Error: "+dl.Error))
	}

	return strings.Join(lines, "\n")
}

func renderProgressBar(p dlmanager.ProgressUpdate, width int) string {
	if width < 10 {
		width = 10
	}
	barWidth := width - 20 // space for percentage + speed
	if barWidth < 5 {
		barWidth = 5
	}

	var pct float64
	if p.TotalBytes > 0 {
		pct = float64(p.DoneBytes) / float64(p.TotalBytes)
	} else if p.TotalSegments > 0 {
		pct = float64(p.DoneSegments) / float64(p.TotalSegments)
	}

	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}

	bar := greenStyle.Render(strings.Repeat("█", filled)) +
		grayStyle.Render(strings.Repeat("░", barWidth-filled))

	speedStr := ""
	if p.Speed > 0 {
		speedStr = " " + formatSpeed(p.Speed)
	}

	return fmt.Sprintf("%s %3.0f%%%s", bar, pct*100, speedStr)
}

func statusIcon(status string) string {
	switch status {
	case "downloading":
		return greenStyle.Render("↓")
	case "queued":
		return purpleStyle.Render("◌")
	case "pending":
		return purpleStyle.Render("…")
	case "paused":
		return yellowStyle.Render("⏸")
	case "completed":
		return grayStyle.Render("✓")
	case "failed":
		return redStyle.Render("✗")
	default:
		return " "
	}
}

func statusStyled(status string) string {
	switch status {
	case "downloading":
		return greenStyle.Render("Downloading")
	case "queued":
		return purpleStyle.Render("Queued")
	case "pending":
		return purpleStyle.Render("Pending")
	case "paused":
		return yellowStyle.Render("Paused")
	case "completed":
		return greenStyle.Render("Completed")
	case "failed":
		return redStyle.Render("Failed")
	default:
		return status
	}
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatSpeed(bytesPerSec float64) string {
	return formatBytes(int64(bytesPerSec)) + "/s"
}

func formatDuration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.0fs", seconds)
	}
	m := int(seconds) / 60
	s := int(seconds) % 60
	if m < 60 {
		return fmt.Sprintf("%d:%02d", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
