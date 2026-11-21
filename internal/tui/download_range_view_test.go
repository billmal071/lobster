package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"lobster/internal/media"
)

func partialRangeView(t *testing.T, numbers []int, width, height int) string {
	t.Helper()
	eps := make([]media.Episode, len(numbers))
	for i, n := range numbers {
		eps[i] = media.Episode{Number: n}
	}
	d := newDownloadDialog()
	d.active = true
	d.Update(dlEpisodesMsg{episodes: eps, partial: true})
	d.step = dlStepRangeInput
	return d.View(width, height)
}

func numbersFrom(first, last, step int) []int {
	var out []int
	for n := first; n <= last; n += step {
		out = append(out, n)
	}
	return out
}

func unwrapped(view string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(view)), " ")
}

func TestRangeViewShowsConsecutiveEpisodesOfAPartialListAsARange(t *testing.T) {
	view := partialRangeView(t, numbersFrom(2, 201, 1), 80, 24)
	if got := lipgloss.Height(view); got > 24 {
		t.Fatalf("view is %d lines in a 24-line terminal, so its top is cut off:\n%s", got, view)
	}
	if text := unwrapped(view); !strings.Contains(text, "Partial list, only these exist: 2-201 ") {
		t.Errorf("view does not sum up episodes 2 to 201 as one range:\n%s", view)
	}
}

func TestRangeViewKeepsALongPartialListWithinTheTerminal(t *testing.T) {
	view := partialRangeView(t, numbersFrom(2, 400, 2), 80, 24)
	if got := lipgloss.Height(view); got > 24 {
		t.Fatalf("view is %d lines in a 24-line terminal, so the warning scrolls off the top:\n%s", got, view)
	}
	text := unwrapped(view)
	if !strings.Contains(text, "Partial list, only these exist: 2, 4, 6, ") {
		t.Errorf("view lost the warning or the first episode numbers:\n%s", view)
	}
	if !strings.Contains(text, " more ") || strings.Contains(text, "400") {
		t.Errorf("view does not say that episode numbers were left out:\n%s", view)
	}
}

func TestRangeViewListsEveryEpisodeOfAPartialListThatFits(t *testing.T) {
	text := unwrapped(partialRangeView(t, numbersFrom(2, 400, 2), 80, 60))
	if !strings.Contains(text, "398, 400 ") || strings.Contains(text, " more ") {
		t.Errorf("a terminal tall enough for the whole list did not show all of it:\n%s", text)
	}
}

func episodesNumbered(numbers ...int) []media.Episode {
	eps := make([]media.Episode, len(numbers))
	for i, n := range numbers {
		eps[i] = media.Episode{Number: n}
	}
	return eps
}

func TestPartialListLabel(t *testing.T) {
	cases := []struct {
		name     string
		episodes []media.Episode
		rows     int
		want     string
	}{
		{"isolated numbers stay a plain list", episodesNumbered(2, 5), 3, "Partial list, only these exist: 2, 5"},
		{"consecutive numbers become a range", episodesNumbered(1, 2, 3, 5, 8, 9), 3, "Partial list, only these exist: 1-3,\n5, 8-9"},
		{"unsorted and repeated numbers are ordered and counted once", episodesNumbered(5, 3, 2, 3, 5), 3, "Partial list, only these exist: 2-3,\n5"},
		{"what does not fit is counted, not listed", episodesNumbered(1, 3, 5, 6, 7, 9, 11, 13, 15, 17, 19, 21, 30, 31), 2, "Partial list, only these exist: 1,\n3, 5-7, 9, 11, 13, 15, 17 and 4 more"},
		{"no room at all still names the first episode", episodesNumbered(4, 6, 8), 0, "Partial list, only these exist: 4\nand 2 more"},
		{"an empty list is only the warning", nil, 3, "Partial list, only these exist:"},
		{"an empty list with no room is still only the warning", nil, 0, "Partial list, only these exist:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := partialListLabel(c.episodes, 36, c.rows); got != c.want {
				t.Errorf("label = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRangeViewFitsANarrowTerminalWhoseFooterWraps(t *testing.T) {
	view := partialRangeView(t, numbersFrom(2, 400, 2), 60, 16)
	if got := lipgloss.Height(view); got > 16 {
		t.Fatalf("view is %d lines in a 16-line terminal:\n%s", got, view)
	}
	if text := unwrapped(view); !strings.Contains(text, "Partial list, only these exist: 2, 4, ") || !strings.Contains(text, " more ") {
		t.Errorf("view lost the warning, the first episode numbers or the count left out:\n%s", view)
	}
}
