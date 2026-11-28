package cmd

import (
	"context"
	"sync"
	"testing"

	"lobster/internal/media"
	"lobster/internal/resolver"
)

type byIDLookup struct {
	*stubProvider
	results  []media.SearchResult
	seasons  map[string][]media.Season
	episodes map[string][]media.Episode
	errs     map[string]error
	mu       sync.Mutex
	asked    []string
}

func (b *byIDLookup) Search(string) ([]media.SearchResult, error) { return b.results, nil }

func (b *byIDLookup) GetSeasons(id string) ([]media.Season, error) {
	b.mu.Lock()
	b.asked = append(b.asked, "seasons:"+id)
	b.mu.Unlock()
	return b.seasons[id], b.errs["seasons:"+id]
}

func (b *byIDLookup) GetEpisodes(id, _ string) ([]media.Episode, error) {
	b.mu.Lock()
	b.asked = append(b.asked, "episodes:"+id)
	b.mu.Unlock()
	return b.episodes[id], b.errs["episodes:"+id]
}

func oneEpisode(id string) []media.Episode {
	return []media.Episode{{ID: id, Number: 1}}
}

func scanFor(t *testing.T, p *byIDLookup, wants []int) ([]*seasonHit, bool) {
	t.Helper()
	hostileEnv(t)
	chainOf(t, p)
	return fallbackSeasonScan(&stubProvider{}, resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}, wants)
}

func TestALaterCandidateListsTheEpisodesThePreferredCandidateCannot(t *testing.T) {
	p := &byIDLookup{
		stubProvider: &stubProvider{},
		results:      showResults("a", "b"),
		seasons:      map[string][]media.Season{"a": seasonsOf(1, 2), "b": seasonsOf(1)},
		episodes:     map[string][]media.Episode{"a": nil, "b": oneEpisode("b-s1e1")},
	}
	hits, failed := scanFor(t, p, []int{1, 2})
	a, err := confirmedEpisodeList(hits, failed, 1, "Show")
	if err != nil || a == nil || a.hit.id != "b" || len(a.episodes) != 1 {
		t.Fatalf("answer = %+v, err = %v; want b's season 1 list, since a lists no episodes for it", a, err)
	}
}

func TestACandidateDroppedForCoverageStillListsTheEpisodesTheKeptOneCannot(t *testing.T) {
	p := &byIDLookup{
		stubProvider: &stubProvider{},
		results:      showResults("a", "b", "c"),
		seasons:      map[string][]media.Season{"a": seasonsOf(1), "b": seasonsOf(1), "c": seasonsOf(2)},
		episodes:     map[string][]media.Episode{"b": oneEpisode("b-s1e1"), "c": oneEpisode("c-s2e1")},
	}
	hits, failed := scanFor(t, p, []int{1, 2})
	a, err := confirmedEpisodeList(hits, failed, 1, "Show")
	if err != nil || a == nil || a.hit.id != "b" {
		t.Fatalf("answer = %+v, err = %v; want b's season 1 list, since a lists no episodes for it", a, err)
	}
}

func TestTheReserveIsNotAskedWhenThePreferredCandidateListsTheEpisodes(t *testing.T) {
	p := &byIDLookup{
		stubProvider: &stubProvider{},
		results:      showResults("a", "b"),
		seasons:      map[string][]media.Season{"a": seasonsOf(1, 2), "b": seasonsOf(1)},
		episodes:     map[string][]media.Episode{"a": oneEpisode("a-s1e1"), "b": oneEpisode("b-s1e1")},
	}
	hits, failed := scanFor(t, p, []int{1, 2})
	a, err := confirmedEpisodeList(hits, failed, 1, "Show")
	if err != nil || a == nil || a.hit.id != "a" {
		t.Fatalf("answer = %+v, err = %v; want a's list", a, err)
	}
	for _, call := range p.asked {
		if call == "seasons:b" || call == "episodes:b" {
			t.Fatalf("calls = %v, want b left unasked", p.asked)
		}
	}
}

func TestTheReserveKeepsToTheWorkAlreadyKept(t *testing.T) {
	p := &byIDLookup{
		stubProvider: &stubProvider{},
		results: []media.SearchResult{
			{ID: "a", Title: "Show", Year: "2015", Type: media.TV},
			{ID: "b", Title: "Show", Year: "2024", Type: media.TV},
		},
		seasons:  map[string][]media.Season{"a": seasonsOf(1, 2), "b": seasonsOf(1, 2)},
		episodes: map[string][]media.Episode{"b": oneEpisode("b-s1e1")},
	}
	hostileEnv(t)
	chainOf(t, p)
	hits, failed := fallbackSeasonScan(&stubProvider{}, resolver.Request{ID: "x/1", Title: "Show", Year: "2015", MediaType: media.TV}, []int{1, 2})
	if a, _ := confirmedEpisodeList(hits, failed, 1, "Show"); a != nil {
		t.Fatalf("answer from %q, want none: the 2024 title is another work", a.hit.id)
	}
}

func TestProbeSeasonsHitsAreUnchangedByTheReserve(t *testing.T) {
	p := &byIDLookup{
		stubProvider: &stubProvider{},
		results:      showResults("a", "b", "c"),
		seasons:      map[string][]media.Season{"a": seasonsOf(1), "b": seasonsOf(1, 2), "c": seasonsOf(2)},
	}
	hits, _ := probeSeasons(context.Background(), p, resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}, []int{1, 2})
	if got := hitIDs(hits); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("hits = %v, want [b a]", got)
	}
}

func TestAReserveLookupThatFailsIsReportedAsAFailedLookup(t *testing.T) {
	for _, call := range []string{"seasons:b", "episodes:b"} {
		t.Run(call, func(t *testing.T) {
			p := &byIDLookup{
				stubProvider: &stubProvider{},
				results:      showResults("a", "b"),
				seasons:      map[string][]media.Season{"a": seasonsOf(1, 2), "b": seasonsOf(1)},
				errs:         map[string]error{call: errBoom},
			}
			hits, failed := scanFor(t, p, []int{1, 2})
			if a, err := confirmedEpisodeList(hits, failed, 1, "Show"); a != nil || err == nil {
				t.Fatalf("answer = %+v, err = %v; want a failed-lookup error, since b might list the season", a, err)
			}
		})
	}
}
