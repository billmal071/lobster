package tui

import (
	"errors"
	"testing"

	"lobster/internal/media"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// pendingSearch submits query from m's search box and returns the model and
// the search's own message, not yet delivered.
func pendingSearch(t *testing.T, m AppModel, query string) (AppModel, tea.Msg) {
	t.Helper()
	m.searchInput = textinput.New()
	m.searchInput.SetValue(query)
	m.isSearching = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("Enter returned %T, want a batch led by the search", cmd())
	}
	return next.(AppModel), batch[0]()
}

func TestASearchAnsweringAfterATabSwitchIsIgnored(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, stale := pendingSearch(t, ownerTestModel(primary, fallback), "show")
	m.switchTab(tabMovies)

	next, _ := m.Update(stale)

	if got := next.(AppModel).results; len(got) != 0 {
		t.Fatalf("results = %+v, want the Series search that finished after the switch to Movies ignored", got)
	}
}

func TestAnEarlierSearchAnsweringAfterANewerOneIsIgnored(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, stale := pendingSearch(t, ownerTestModel(primary, fallback), "show")
	m.provider = &ownedStub{results: []media.SearchResult{{ID: "p/9", Title: "Other", Type: media.TV}}}
	m.fallbackProviders = nil
	m, fresh := pendingSearch(t, m, "other")

	next, _ := m.Update(fresh)
	next, _ = next.(AppModel).Update(stale)

	if got := next.(AppModel).results; len(got) != 1 || got[0].ID != "p/9" {
		t.Fatalf("results = %+v, want the newer search's rows kept", got)
	}
	if got := next.(AppModel).owners; len(got) != 0 {
		t.Fatalf("owners = %v, want the earlier search's fallback owner not applied to the newer rows", got)
	}
}

func TestATabListingAnsweringAfterASearchIsIgnored(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m := ownerTestModel(primary, fallback)
	tabListing := m.stampResults(fetchTabCmd(&trendingStub{}, tabSeries))()
	m, fresh := pendingSearch(t, m, "show")

	next, _ := m.Update(fresh)
	next, _ = next.(AppModel).Update(tabListing)

	if got := next.(AppModel).results; len(got) != 1 || got[0].ID != "fb/1" {
		t.Fatalf("results = %+v, want the trending list that finished after the search ignored", got)
	}
}

func TestTheCurrentSearchIsShown(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, fresh := pendingSearch(t, ownerTestModel(primary, fallback), "show")

	next, _ := m.Update(fresh)

	if got := next.(AppModel).owners; len(got) != 1 || got[0] != fallback {
		t.Fatalf("owners = %v, want the current search's fallback row accepted", got)
	}
}

type trendingStub struct{ ownedStub }

func (*trendingStub) Trending(media.MediaType) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "trend/1", Title: "Trending"}}, nil
}

func TestAFailedSearchAnsweringAfterATabSwitchShowsNoError(t *testing.T) {
	failing := &ownedStub{err: errStale}
	m, stale := pendingSearch(t, ownerTestModel(failing), "show")
	if _, ok := stale.(resultsErrMsg); !ok {
		t.Fatalf("failed search produced %T, want resultsErrMsg", stale)
	}
	m.switchTab(tabMovies)

	next, _ := m.Update(stale)

	if got := next.(AppModel).err; got != nil {
		t.Fatalf("err = %v, want the Series search's failure ignored after the switch to Movies", got)
	}
}

func TestTheCurrentSearchsFailureIsShown(t *testing.T) {
	m, msg := pendingSearch(t, ownerTestModel(&ownedStub{err: errStale}), "show")

	next, _ := m.Update(msg)

	if got := next.(AppModel).err; got != errStale {
		t.Fatalf("err = %v, want the current search's failure shown", got)
	}
}

var errStale = errors.New("search failed")
