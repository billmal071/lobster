package resolver

import (
	"context"
	"errors"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func newBlockedSearchSP() *countingSP {
	c := newCountingSP("tv/219", media.TV)
	c.searchErr = errors.New("status 403")
	return c
}

func TestResolveAsksTheOwnerByIDWithoutSearching(t *testing.T) {
	owner := newBlockedSearchSP()
	r := New([]provider.Provider{owner}, NewHealthStore(), nil)
	r.validate = false

	stream, _, err := r.Resolve(context.Background(), Request{
		ID: "tv/219", Title: "Kyle XY", Year: "2006", MediaType: media.TV,
		Season: 2, Episode: 15, Owner: owner,
	})
	if err != nil || stream == nil {
		t.Fatalf("Resolve = (%v, %v), want the owner's stream", stream, err)
	}
	if n := owner.searches.Load(); n != 0 {
		t.Errorf("owner searched %d times, want 0: its ID is already known", n)
	}
	if got := owner.episode.Load(); got != "219:2:15" {
		t.Errorf("episode ID = %v, want 219:2:15", got)
	}
}

func TestResolveStillSearchesProvidersThatDoNotOwnTheID(t *testing.T) {
	owner := newCountingSP("tv/219", media.TV)
	other := newBlockedSearchSP()
	r := New([]provider.Provider{other}, NewHealthStore(), nil)
	r.validate = false

	_, rep, err := r.Resolve(context.Background(), Request{
		ID: "tv/219", Title: "Kyle XY", MediaType: media.TV,
		Season: 2, Episode: 15, Owner: owner,
	})
	if err == nil {
		t.Fatal("Resolve succeeded through a provider whose search is blocked")
	}
	if n := other.searches.Load(); n != 1 {
		t.Errorf("non-owner searched %d times, want 1", n)
	}
	if len(rep.Attempts) != 1 || rep.Attempts[0].Stage != "search" {
		t.Errorf("attempts = %+v, want one search failure", rep.Attempts)
	}
}
