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
	msg := fetchTabCmd(stubProvider{rows: rows}, tabMovies, 0)()
	got, ok := msg.(resultsFetchedMsg)
	if !ok || len(got) != 1 {
		t.Fatalf("msg = %#v, want a plain resultsFetchedMsg", msg)
	}
}

func TestFetchTabForwardsProviderNotice(t *testing.T) {
	p := &noticingProvider{stubProvider: stubProvider{rows: []media.SearchResult{{ID: "movie/1", Title: "A"}}}, notice: "tmdb_api_key rejected"}
	var results, notices int
	var text string
	for _, m := range flatten(fetchTabCmd(p, tabMovies, 3)()) {
		switch v := m.(type) {
		case resultsFetchedMsg:
			results++
		case noticeMsg:
			notices++
			text = v.text
			if v.tab != tabMovies || v.gen != 3 {
				t.Errorf("notice origin = tab %v gen %d, want the request's", v.tab, v.gen)
			}
		}
	}
	if results != 1 || notices != 1 || text != "tmdb_api_key rejected" {
		t.Errorf("results=%d notices=%d text=%q", results, notices, text)
	}
}

func TestFetchTabErrorStaysAnError(t *testing.T) {
	p := &noticingProvider{stubProvider: stubProvider{err: assertErr("boom")}, notice: "x"}
	msg := fetchTabCmd(p, tabMovies, 3)()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("msg = %#v, want errMsg", msg)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestNoticeMsgSetsNoticeAndTabSwitchClearsIt(t *testing.T) {
	m := AppModel{list: list.New(nil, list.NewDefaultDelegate(), 10, 10), activeTab: tabMovies}
	updated, _ := m.Update(noticeMsg{text: "hello", tab: tabMovies})
	m = updated.(AppModel)
	if m.notice != "hello" {
		t.Fatalf("notice = %q", m.notice)
	}
	m.switchTab(tabSeries)
	if m.notice != "" {
		t.Errorf("notice = %q after a tab switch, want cleared", m.notice)
	}
}

func TestNoticeFromAnEarlierTabRequestIsIgnored(t *testing.T) {
	m := AppModel{list: list.New(nil, list.NewDefaultDelegate(), 10, 10), activeTab: tabMovies}
	m.switchTab(tabAnime)
	updated, _ := m.Update(noticeMsg{text: "movies notice", tab: tabMovies, gen: 0})
	m = updated.(AppModel)
	if m.notice != "" {
		t.Fatalf("notice = %q, a late Movies notice must not show on Anime", m.notice)
	}
	updated, _ = m.Update(noticeMsg{text: "anime notice", tab: tabAnime, gen: m.tabReqGen})
	m = updated.(AppModel)
	if m.notice != "anime notice" {
		t.Fatalf("notice = %q, want the current request's notice", m.notice)
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

type tvSearchStub struct {
	stubProvider
}

func (tvSearchStub) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"}}, nil
}

func TestSelectionResolvesCatalogueTVRowForThePlaybackProvider(t *testing.T) {
	inner := &tvSearchStub{}
	general, _ := wrapProviders(inner, provider.NewAnimeOnsen(), &config.Config{TMDBAPIKey: "abc"})
	row := &media.SearchResult{ID: "tv/37854", Title: "One Piece", Type: media.TV, Year: "1999", FromCatalogue: true}
	gotRow, gotProv := finishSelection(row, general)
	if gotRow.ID != "tv/37854" || gotRow.ProviderID != "tv/one-piece-100" {
		t.Errorf("row = %+v, want the catalogue id kept and the provider's id named", gotRow)
	}
	if gotProv != provider.Provider(inner) {
		t.Errorf("provider = %T, want the inner provider", gotProv)
	}
	if r, _ := finishSelection(nil, inner); r != nil {
		t.Errorf("nil selection became %+v", r)
	}
}

func TestSelectionKeepsANativeProviderRowUnresolved(t *testing.T) {
	inner := &tvSearchStub{}
	general, _ := wrapProviders(inner, provider.NewAnimeOnsen(), &config.Config{})
	row := &media.SearchResult{ID: "movie/550", Title: "Not Listed Here", Type: media.Movie}
	got, _ := finishSelection(row, general)
	if *got != *row {
		t.Fatalf("native row = %+v, want it unchanged", *got)
	}
}
