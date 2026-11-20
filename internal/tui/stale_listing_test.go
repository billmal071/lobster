package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func browseModel(active tab, p provider.Provider) AppModel {
	return AppModel{
		list:          list.New(nil, list.NewDefaultDelegate(), 10, 10),
		searchInput:   textinput.New(),
		activeTab:     active,
		provider:      p,
		animeProvider: p,
	}
}

func oneRow(title string) []media.SearchResult {
	return []media.SearchResult{{ID: "tv/1", Title: title, Type: media.TV}}
}

func apply(t *testing.T, m AppModel, msg tea.Msg) AppModel {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(AppModel)
}

func shownTitles(m AppModel) []string {
	var out []string
	for _, it := range m.list.Items() {
		out = append(out, it.(listItem).result.Title)
	}
	return out
}

func TestListingFromAnEarlierRequestIsIgnored(t *testing.T) {
	cases := []struct {
		name   string
		moves  []tab
		stale  func(m AppModel) resultsFetchedMsg
		active tab
	}{
		{"another tab, earlier generation", []tab{tabAnime}, func(AppModel) resultsFetchedMsg {
			return resultsFetchedMsg{results: oneRow("Series row"), tab: tabSeries, gen: 0}
		}, tabAnime},
		{"another tab, current generation", []tab{tabAnime}, func(m AppModel) resultsFetchedMsg {
			return resultsFetchedMsg{results: oneRow("Series row"), tab: tabSeries, gen: m.tabReqGen}
		}, tabAnime},
		{"same tab, earlier generation", []tab{tabAnime, tabSeries}, func(AppModel) resultsFetchedMsg {
			return resultsFetchedMsg{results: oneRow("Series row"), tab: tabSeries, gen: 0}
		}, tabSeries},
		{"live tv is open", []tab{tabLiveTV}, func(AppModel) resultsFetchedMsg {
			return resultsFetchedMsg{results: oneRow("Series row"), tab: tabSeries, gen: 0}
		}, tabLiveTV},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := browseModel(tabSeries, stubProvider{})
			for _, next := range tc.moves {
				m.switchTab(next)
			}
			m.err = assertErr("the current request's error")

			m = apply(t, m, tc.stale(m))

			if len(m.results) != 0 || len(m.list.Items()) != 0 || m.currentItem != nil {
				t.Fatalf("results = %v, shown = %v, current = %v; a late Series listing must not land on %v", m.results, shownTitles(m), m.currentItem, tc.active)
			}
			if m.err == nil {
				t.Errorf("a late listing cleared the current request's error")
			}
		})
	}
}

func TestListingFromTheCurrentRequestIsShown(t *testing.T) {
	m := browseModel(tabSeries, stubProvider{})
	m.switchTab(tabAnime)

	m = apply(t, m, resultsFetchedMsg{results: oneRow("Anime row"), tab: tabAnime, gen: m.tabReqGen})

	if got := shownTitles(m); len(got) != 1 || got[0] != "Anime row" || m.currentItem == nil || m.currentItem.Title != "Anime row" {
		t.Fatalf("shown = %v, current = %v, want the current request's listing", got, m.currentItem)
	}
}

func TestFetchErrorFromAnEarlierRequestIsIgnored(t *testing.T) {
	cases := []struct {
		name  string
		moves []tab
		stale func(m AppModel) fetchErrMsg
	}{
		{"another tab, earlier generation", []tab{tabAnime}, func(AppModel) fetchErrMsg {
			return fetchErrMsg{err: assertErr("series boom"), tab: tabSeries, gen: 0}
		}},
		{"another tab, current generation", []tab{tabAnime}, func(m AppModel) fetchErrMsg {
			return fetchErrMsg{err: assertErr("series boom"), tab: tabSeries, gen: m.tabReqGen}
		}},
		{"same tab, earlier generation", []tab{tabAnime, tabSeries}, func(AppModel) fetchErrMsg {
			return fetchErrMsg{err: assertErr("series boom"), tab: tabSeries, gen: 0}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := browseModel(tabSeries, stubProvider{})
			for _, next := range tc.moves {
				m.switchTab(next)
			}

			m = apply(t, m, tc.stale(m))

			if m.err != nil {
				t.Fatalf("err = %v, a late Series failure must not show on the open tab", m.err)
			}
		})
	}
}

func TestFetchErrorFromTheCurrentRequestIsShown(t *testing.T) {
	m := browseModel(tabSeries, stubProvider{})
	m.switchTab(tabAnime)

	m = apply(t, m, fetchErrMsg{err: assertErr("anime boom"), tab: tabAnime, gen: m.tabReqGen})

	if m.err == nil || m.err.Error() != "anime boom" {
		t.Fatalf("err = %v, want the current request's failure", m.err)
	}
}

func TestFetchTabStampsEveryListingWithItsRequest(t *testing.T) {
	rows := oneRow("A")
	providers := map[string]provider.Provider{
		"plain":                      stubProvider{rows: rows},
		"pending notice":             &noticingProvider{stubProvider: stubProvider{rows: rows}, notice: "n"},
		"notice returned with fetch": &taggedProvider{stubProvider: stubProvider{rows: rows}, notice: "n"},
		"no notice returned":         &taggedProvider{stubProvider: stubProvider{rows: rows}},
	}
	for name, p := range providers {
		t.Run(name, func(t *testing.T) {
			var listings int
			for _, msg := range flatten(fetchTabCmd(p, tabSeries, 7)()) {
				if v, ok := msg.(resultsFetchedMsg); ok {
					listings++
					if v.tab != tabSeries || v.gen != 7 || len(v.results) != 1 {
						t.Errorf("listing = %+v, want one row stamped tab %v gen 7", v, tabSeries)
					}
				}
			}
			if listings != 1 {
				t.Fatalf("listings = %d, want 1", listings)
			}
		})
	}
}

type failingTaggedProvider struct{ stubProvider }

func (failingTaggedProvider) TrendingWithNotice(media.MediaType) ([]media.SearchResult, string, error) {
	return nil, "", assertErr("boom")
}

func TestFetchTabStampsEveryFailureWithItsRequest(t *testing.T) {
	providers := map[string]provider.Provider{
		"plain":       stubProvider{err: assertErr("boom")},
		"with notice": failingTaggedProvider{},
	}
	for name, p := range providers {
		t.Run(name, func(t *testing.T) {
			got, ok := fetchTabCmd(p, tabSeries, 7)().(fetchErrMsg)
			if !ok || got.err == nil || got.err.Error() != "boom" || got.tab != tabSeries || got.gen != 7 {
				t.Fatalf("msg = %#v, want boom stamped tab %v gen 7", got, tabSeries)
			}
		})
	}
}

type searchStub struct {
	stubProvider
	found []media.SearchResult
	err   error
}

func (s searchStub) Search(string) ([]media.SearchResult, error) { return s.found, s.err }

func TestSearchStampsItsResultsWithItsRequest(t *testing.T) {
	few := searchStub{found: oneRow("A")}
	cases := map[string]tea.Cmd{
		"primary only":          searchCmd(few, "a", tabAnime, 4),
		"merged with fallbacks": searchCmd(few, "a", tabAnime, 4, searchStub{found: oneRow("B")}),
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := cmd().(resultsFetchedMsg)
			if !ok || len(got.results) == 0 || got.tab != tabAnime || got.gen != 4 {
				t.Fatalf("msg = %#v, want results stamped tab %v gen 4", got, tabAnime)
			}
		})
	}
}

func TestSearchStampsItsFailureWithItsRequest(t *testing.T) {
	got, ok := searchCmd(searchStub{err: assertErr("boom")}, "a", tabAnime, 4)().(fetchErrMsg)
	if !ok || got.err == nil || got.err.Error() != "boom" || got.tab != tabAnime || got.gen != 4 {
		t.Fatalf("msg = %#v, want boom stamped tab %v gen 4", got, tabAnime)
	}
}

func submitSearch(t *testing.T, m AppModel, query string) (AppModel, tea.Cmd) {
	t.Helper()
	m.isSearching = true
	m.searchInput.SetValue(query)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(AppModel), cmd
}

func listingsOf(cmd tea.Cmd) []resultsFetchedMsg {
	var out []resultsFetchedMsg
	for _, msg := range flatten(cmd()) {
		if v, ok := msg.(resultsFetchedMsg); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestSearchSupersedesTheListingStillInFlight(t *testing.T) {
	m := browseModel(tabSeries, searchStub{found: oneRow("Search hit")})
	inFlight := resultsFetchedMsg{results: oneRow("Trending row"), tab: m.activeTab, gen: m.tabReqGen}

	m, cmd := submitSearch(t, m, "hit")
	found := listingsOf(cmd)
	if len(found) != 1 {
		t.Fatalf("search produced %d result messages, want 1", len(found))
	}
	m = apply(t, m, found[0])
	m = apply(t, m, inFlight)

	if got := shownTitles(m); len(got) != 1 || got[0] != "Search hit" {
		t.Fatalf("shown = %v, a trending listing that lands after the search must not replace its results", got)
	}
}

func TestSearchSupersedesAnEarlierSearch(t *testing.T) {
	m := browseModel(tabSeries, searchStub{found: oneRow("First hit")})
	m, first := submitSearch(t, m, "first")
	m.provider = searchStub{found: oneRow("Second hit")}
	m, second := submitSearch(t, m, "second")

	m = apply(t, m, listingsOf(second)[0])
	m = apply(t, m, listingsOf(first)[0])

	if got := shownTitles(m); len(got) != 1 || got[0] != "Second hit" {
		t.Fatalf("shown = %v, an earlier search that lands late must not replace the newer one", got)
	}
}

func TestTabSwitchListingIsShown(t *testing.T) {
	m := browseModel(tabMovies, stubProvider{rows: oneRow("Series row")})
	cmd := m.switchTab(tabSeries)

	found := listingsOf(cmd)
	if len(found) != 1 {
		t.Fatalf("tab switch produced %d listings, want 1", len(found))
	}
	m = apply(t, m, found[0])

	if got := shownTitles(m); len(got) != 1 || got[0] != "Series row" {
		t.Fatalf("shown = %v, want the listing the tab switch asked for", got)
	}
}

func TestRefusedDownloadsSwitchKeepsTheListingInFlight(t *testing.T) {
	m := browseModel(tabSeries, stubProvider{})
	inFlight := resultsFetchedMsg{results: oneRow("Series row"), tab: m.activeTab, gen: m.tabReqGen}

	m.switchTab(tabDownloads)
	m = apply(t, m, inFlight)

	if got := shownTitles(m); m.activeTab != tabSeries || len(got) != 1 || got[0] != "Series row" {
		t.Fatalf("tab = %v, shown = %v; with no download manager the Series listing is still the current request", m.activeTab, got)
	}
}
