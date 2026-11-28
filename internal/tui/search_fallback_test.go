package tui

import (
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type searchStub struct {
	provider.Provider
	results []media.SearchResult
	err     error
}

func (s searchStub) Search(string) ([]media.SearchResult, error) { return s.results, s.err }

func TestSearchCmdBroadensToFallbacksWhenPrimaryAnswersNoResults(t *testing.T) {
	primary := searchStub{err: fmt.Errorf("%w for %q", provider.ErrNoResults, "show")}
	fallback := searchStub{results: []media.SearchResult{{ID: "fb/1", Title: "Show"}}}

	msg := searchCmd(primary, "show", fallback)()

	fetched, ok := msg.(resultsFetchedMsg)
	if !ok {
		t.Fatalf("got %T (%v), want resultsFetchedMsg from the fallback", msg, msg)
	}
	if got := fetched.results; len(got) != 1 || got[0].ID != "fb/1" {
		t.Fatalf("got %+v, want the fallback's result", got)
	}
}

func TestSearchCmdReportsNoResultsWithoutFallbacks(t *testing.T) {
	primary := searchStub{err: fmt.Errorf("%w for %q", provider.ErrNoResults, "show")}

	msg := searchCmd(primary, "show")()

	em, ok := msg.(errMsg)
	if !ok || !errors.Is(em.err, provider.ErrNoResults) {
		t.Fatalf("got %T (%v), want errMsg wrapping ErrNoResults", msg, msg)
	}
}

func TestSearchCmdReportsNoResultsWhenFallbacksAlsoFindNothing(t *testing.T) {
	primary := searchStub{err: fmt.Errorf("%w for %q", provider.ErrNoResults, "show")}
	fallback := searchStub{err: provider.ErrNoResults}

	msg := searchCmd(primary, "show", fallback)()

	em, ok := msg.(errMsg)
	if !ok || !errors.Is(em.err, provider.ErrNoResults) {
		t.Fatalf("got %T (%v), want errMsg wrapping ErrNoResults", msg, msg)
	}
}

func TestSearchCmdReportsLookupFailureEvenWithFallbacks(t *testing.T) {
	outage := errors.New("eztv search: tmdb: unexpected status 503")
	primary := searchStub{err: outage}
	fallback := searchStub{results: []media.SearchResult{{ID: "fb/1", Title: "Show"}}}

	msg := searchCmd(primary, "show", fallback)()

	em, ok := msg.(errMsg)
	if !ok || !errors.Is(em.err, outage) {
		t.Fatalf("got %T (%v), want errMsg carrying the lookup failure", msg, msg)
	}
}
