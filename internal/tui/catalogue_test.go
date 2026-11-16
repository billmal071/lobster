package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"lobster/internal/catalog"
	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

type stubProvider struct {
	provider.Provider
	rows []media.SearchResult
	err  error
}

func (s stubProvider) Trending(media.MediaType) ([]media.SearchResult, error) { return s.rows, s.err }

type noticingProvider struct {
	stubProvider
	notice string
}

func (n *noticingProvider) TakeNotice() string {
	out := n.notice
	n.notice = ""
	return out
}

func flatten(msg tea.Msg) []tea.Msg {
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, flatten(c())...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestFetchTabWithoutNoticeIsUnchanged(t *testing.T) {
	rows := []media.SearchResult{{ID: "movie/1", Title: "A"}}
	msg := fetchTabCmd(stubProvider{rows: rows}, tabMovies)()
	got, ok := msg.(resultsFetchedMsg)
	if !ok || len(got) != 1 {
		t.Fatalf("msg = %#v, want a plain resultsFetchedMsg", msg)
	}
}

func TestFetchTabForwardsProviderNotice(t *testing.T) {
	p := &noticingProvider{stubProvider: stubProvider{rows: []media.SearchResult{{ID: "movie/1", Title: "A"}}}, notice: "tmdb_api_key rejected"}
	var results, notices int
	var text string
	for _, m := range flatten(fetchTabCmd(p, tabMovies)()) {
		switch v := m.(type) {
		case resultsFetchedMsg:
			results++
		case noticeMsg:
			notices++
			text = string(v)
		}
	}
	if results != 1 || notices != 1 || text != "tmdb_api_key rejected" {
		t.Errorf("results=%d notices=%d text=%q", results, notices, text)
	}
}

func TestFetchTabErrorStaysAnError(t *testing.T) {
	p := &noticingProvider{stubProvider: stubProvider{err: assertErr("boom")}, notice: "x"}
	msg := fetchTabCmd(p, tabMovies)()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("msg = %#v, want errMsg", msg)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestNoticeMsgSetsNoticeAndTabSwitchClearsIt(t *testing.T) {
	m := AppModel{list: list.New(nil, list.NewDefaultDelegate(), 10, 10), activeTab: tabMovies}
	updated, _ := m.Update(noticeMsg("hello"))
	m = updated.(AppModel)
	if m.notice != "hello" {
		t.Fatalf("notice = %q", m.notice)
	}
	m.switchTab(tabSeries)
	if m.notice != "" {
		t.Errorf("notice = %q after a tab switch, want cleared", m.notice)
	}
}

func TestSeriesRowWithUnknownSeasonsOmitsCount(t *testing.T) {
	unknown := listItem{result: media.SearchResult{Type: media.TV, Year: "2011"}}
	if d := unknown.Description(); strings.Contains(d, "Seasons") {
		t.Errorf("description = %q, must not claim 0 seasons", d)
	}
	known := listItem{result: media.SearchResult{Type: media.TV, Year: "2011", Seasons: 8}}
	if d := known.Description(); !strings.Contains(d, "8 Seasons") {
		t.Errorf("description = %q, want the season count", d)
	}
}

func TestWrapProvidersAndUnwrap(t *testing.T) {
	inner := &stubProvider{}
	general, anime := wrapProviders(inner, provider.NewAnimeOnsen(), &config.Config{TMDBAPIKey: "abc"})
	if _, ok := general.(*catalog.Provider); !ok {
		t.Errorf("general = %T, want *catalog.Provider", general)
	}
	if _, ok := anime.(*catalog.Provider); !ok {
		t.Errorf("anime = %T, want *catalog.Provider", anime)
	}
	if got := unwrapProvider(general); got != provider.Provider(inner) {
		t.Errorf("unwrap = %T, want the inner provider", got)
	}
	if got := unwrapProvider(inner); got != provider.Provider(inner) {
		t.Errorf("unwrap of a plain provider changed it: %T", got)
	}
}

func TestWrapProvidersWithNilConfig(t *testing.T) {
	general, _ := wrapProviders(&stubProvider{}, provider.NewAnimeOnsen(), nil)
	if general == nil {
		t.Fatal("nil general provider")
	}
}
