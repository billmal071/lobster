package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type emptyListProvider struct{ provider.Provider }

func (emptyListProvider) GetSeasons(string) ([]media.Season, error) { return nil, nil }

func (emptyListProvider) GetEpisodes(string, string) ([]media.Episode, error) { return nil, nil }

func emptySeasonDialog(t *testing.T, fallback func(provider.Provider, media.SearchResult, int) ([]media.Episode, bool, error)) *downloadDialog {
	t.Helper()
	prev := EpisodeListFallback
	EpisodeListFallback = fallback
	t.Cleanup(func() { EpisodeListFallback = prev })
	return &downloadDialog{
		active:   true,
		item:     media.SearchResult{ID: "x", Title: "Show", Type: media.TV},
		provider: emptyListProvider{},
		seasons:  []media.Season{{ID: "1", Number: 1}},
	}
}

func TestFetchEpisodesReportsAnEmptyListWithNoFallback(t *testing.T) {
	d := emptySeasonDialog(t, nil)
	if msg := d.fetchEpisodes(0)().(dlEpisodesMsg); msg.err == nil {
		t.Fatal("an empty episode list with no error was passed on as a season to pick from")
	}
}

func TestFetchEpisodesReportsTheFallbackRefusalForAnEmptyList(t *testing.T) {
	refusal := errors.New("no fallback provider could confirm season 1")
	d := emptySeasonDialog(t, func(provider.Provider, media.SearchResult, int) ([]media.Episode, bool, error) {
		return nil, false, refusal
	})
	msg := d.fetchEpisodes(0)().(dlEpisodesMsg)
	if !errors.Is(msg.err, refusal) {
		t.Fatalf("err = %v, want the fallback's refusal", msg.err)
	}
}

func TestFetchEpisodesReportsAnEmptyFallbackList(t *testing.T) {
	d := emptySeasonDialog(t, func(provider.Provider, media.SearchResult, int) ([]media.Episode, bool, error) {
		return nil, false, nil
	})
	if msg := d.fetchEpisodes(0)().(dlEpisodesMsg); msg.err == nil {
		t.Fatal("an empty list from the source and from the fallback was passed on as a season to pick from")
	}
}

func TestOneEpisodeOfAnEmptySeasonShowsAnErrorInsteadOfPanicking(t *testing.T) {
	d := emptySeasonDialog(t, nil)
	d.step = dlStepLoadEpisodes
	d.Update(d.fetchEpisodes(0)())
	if d.err == nil || d.step == dlStepPickMode {
		t.Fatalf("step = %v, err = %v; want the dialog stopped on an error", d.step, d.err)
	}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	d.Update(enter)
	d.Update(enter)
}

func TestStartReportsAnEmptySeasonList(t *testing.T) {
	d := &downloadDialog{}
	cmd := d.start(media.SearchResult{ID: "x", Title: "Show", Type: media.TV}, emptyListProvider{}, nil, "")
	if msg := cmd().(dlSeasonsMsg); msg.err == nil {
		t.Fatal("an empty season list with no error was passed on as seasons to pick from")
	}
}

func TestEnterOnAnEmptySeasonListShowsAnErrorInsteadOfPanicking(t *testing.T) {
	d := &downloadDialog{}
	d.Update(d.start(media.SearchResult{ID: "x", Title: "Show", Type: media.TV}, emptyListProvider{}, nil, "")())
	if d.err == nil {
		t.Fatalf("step = %v with no error; want the dialog stopped on an error", d.step)
	}
	d.Update(tea.KeyMsg{Type: tea.KeyEnter})
}
