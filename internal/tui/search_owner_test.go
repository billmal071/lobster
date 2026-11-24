package tui

import (
	"fmt"
	"testing"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type ownedStub struct {
	provider.Provider
	results      []media.SearchResult
	err          error
	detailsAsked []string
}

func (s *ownedStub) Search(string) ([]media.SearchResult, error) { return s.results, s.err }

func (s *ownedStub) GetDetails(id string) (*media.ContentDetail, error) {
	s.detailsAsked = append(s.detailsAsked, id)
	return &media.ContentDetail{}, nil
}

func ownerTestModel(primary provider.Provider, fallbacks ...provider.Provider) AppModel {
	return AppModel{
		provider:          primary,
		fallbackProviders: fallbacks,
		activeTab:         tabSeries,
		config:            &config.Config{DownloadDir: "/tmp/lobster-test"},
		list:              list.New(nil, list.NewDefaultDelegate(), 80, 20),
		dlManager:         dlmanager.New(nil, nil, nil, 1, 0),
		dlDialog:          newDownloadDialog(),
	}
}

func searchedModel(t *testing.T, primary, fallback *ownedStub) (AppModel, tea.Cmd) {
	t.Helper()
	m := ownerTestModel(primary, fallback)
	msg := searchCmd(primary, "show", fallback)()
	next, cmd := m.Update(msg)
	return next.(AppModel), cmd
}

func noResultsPrimary() *ownedStub {
	return &ownedStub{err: fmt.Errorf("%w for %q", provider.ErrNoResults, "show")}
}

func fallbackWithShow() *ownedStub {
	return &ownedStub{results: []media.SearchResult{{ID: "fb/1", Title: "Show", Type: media.TV}}}
}

func TestSelectingAFallbackRowHandsBackTheFallbackProvider(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, _ := searchedModel(t, primary, fallback)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := next.(AppModel).selectedProvider; got != provider.Provider(fallback) {
		t.Fatalf("selectedProvider = %p, want the fallback %p that produced the row", got, fallback)
	}
}

func TestSelectingAPrimaryRowHandsBackThePrimary(t *testing.T) {
	primary := &ownedStub{results: []media.SearchResult{{ID: "p/1", Title: "Show", Type: media.TV}}}
	fallback := &ownedStub{results: []media.SearchResult{{ID: "fb/2", Title: "Other", Type: media.TV}}}
	m, _ := searchedModel(t, primary, fallback)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := next.(AppModel).selectedProvider; got != provider.Provider(primary) {
		t.Fatalf("selectedProvider = %p, want the primary %p", got, primary)
	}
}

func TestFallbackRowDetailsAreAskedOfTheFallback(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	_, cmd := searchedModel(t, primary, fallback)

	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("cmd returned %T, want a batch led by the detail fetch", cmd())
	}
	if _, ok := batch[0]().(detailFetchedMsg); !ok {
		t.Fatal("first command did not fetch details")
	}
	if len(primary.detailsAsked) != 0 || len(fallback.detailsAsked) != 1 || fallback.detailsAsked[0] != "fb/1" {
		t.Fatalf("primary asked %q, fallback asked %q; want only the fallback asked for fb/1", primary.detailsAsked, fallback.detailsAsked)
	}
}

func TestDownloadDialogForAFallbackRowUsesTheFallback(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, _ := searchedModel(t, primary, fallback)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	if got := next.(AppModel).dlDialog.provider; got != provider.Provider(fallback) {
		t.Fatalf("download dialog provider = %p, want the fallback %p", got, fallback)
	}
}

func TestAFallbackRowThatWinsDedupKeepsItsProvider(t *testing.T) {
	primary := &ownedStub{results: []media.SearchResult{{ID: "p/1", Title: "Show"}}}
	fallback := &ownedStub{results: []media.SearchResult{{ID: "fb/1", Title: "Show", Year: "2020", Type: media.TV}}}
	m, _ := searchedModel(t, primary, fallback)

	if len(m.results) != 1 || m.results[0].ID != "fb/1" {
		t.Fatalf("results = %+v, want the richer fallback row to replace the primary's", m.results)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := next.(AppModel).selectedProvider; got != provider.Provider(fallback) {
		t.Fatalf("selectedProvider = %p, want the fallback %p whose ID the row carries", got, fallback)
	}
}

func TestMovingToAFallbackRowAsksTheFallbackForDetails(t *testing.T) {
	primary := &ownedStub{results: []media.SearchResult{{ID: "p/1", Title: "Show", Type: media.TV}}}
	fallback := &ownedStub{results: []media.SearchResult{{ID: "fb/2", Title: "Other", Type: media.TV}}}
	m, _ := searchedModel(t, primary, fallback)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})

	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("cmd returned %T, want a batch led by the detail fetch", cmd())
	}
	if _, ok := batch[0]().(detailFetchedMsg); !ok {
		t.Fatal("first command did not fetch details")
	}
	if len(primary.detailsAsked) != 0 || len(fallback.detailsAsked) != 1 || fallback.detailsAsked[0] != "fb/2" {
		t.Fatalf("primary asked %q, fallback asked %q; want only the fallback asked for fb/2", primary.detailsAsked, fallback.detailsAsked)
	}
}

func TestLiveRowsDoNotInheritAnEarlierSearchsOwners(t *testing.T) {
	primary, fallback := noResultsPrimary(), fallbackWithShow()
	m, _ := searchedModel(t, primary, fallback)
	m.activeTab = tabLiveTV

	m.setLiveRows([]liveRow{{result: media.SearchResult{ID: "ch/1", Title: "Channel"}}})

	if got := m.providerForCurrentItem(); got == provider.Provider(fallback) {
		t.Fatal("a live row resolved to the fallback that produced the previous search's row at the same index")
	}
}
