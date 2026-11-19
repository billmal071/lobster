package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

type seasonOutcome struct {
	seasons []media.Season
	err     error
}

type scriptedLookup struct {
	*stubProvider
	searchResults []media.SearchResult
	searchErr     error
	outcomes      []seasonOutcome
	calls         int
}

func (s *scriptedLookup) Search(string) ([]media.SearchResult, error) {
	return s.searchResults, s.searchErr
}

func (s *scriptedLookup) GetSeasons(string) ([]media.Season, error) {
	if s.calls >= len(s.outcomes) {
		return nil, errors.New("unscripted GetSeasons call")
	}
	o := s.outcomes[s.calls]
	s.calls++
	return o.seasons, o.err
}

func showResults(ids ...string) []media.SearchResult {
	var rs []media.SearchResult
	for _, id := range ids {
		rs = append(rs, media.SearchResult{ID: id, Title: "Show", Type: media.TV})
	}
	return rs
}

func scripted(results []media.SearchResult, outcomes ...seasonOutcome) *scriptedLookup {
	return &scriptedLookup{stubProvider: &stubProvider{}, searchResults: results, outcomes: outcomes}
}

func seasonsOf(nums ...int) []media.Season {
	var ss []media.Season
	for _, n := range nums {
		ss = append(ss, media.Season{ID: fmt.Sprint(n), Number: n})
	}
	return ss
}

var errBoom = errors.New("boom")

func TestProbeSeasonsOutcomes(t *testing.T) {
	req := resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}
	cases := []struct {
		name       string
		p          *scriptedLookup
		wantHit    bool
		wantGapped bool
		wantFailed bool
	}{
		{"search ErrNoResults is a completed absence", &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults}, false, false, false},
		{"wrapped ErrNoResults is a completed absence", &scriptedLookup{stubProvider: &stubProvider{}, searchErr: fmt.Errorf("x: %w", provider.ErrNoResults)}, false, false, false},
		{"search error is a failure", &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom}, false, false, true},
		{"no candidates", scripted(nil), false, false, false},
		{"candidate for another show", scripted([]media.SearchResult{{ID: "a", Title: "Other", Type: media.TV}}), false, false, false},
		{"matching candidate with no seasons", scripted(showResults("a"), seasonOutcome{}), false, false, false},
		{"matching candidate whose lookup errors", scripted(showResults("a"), seasonOutcome{err: errBoom}), false, false, true},
		{"matching candidate whose GetSeasons says ErrNotCarried is a completed absence", scripted(showResults("a"), seasonOutcome{err: provider.ErrNotCarried}), false, false, false},
		{"wrapped ErrNotCarried is a completed absence", scripted(showResults("a"), seasonOutcome{err: fmt.Errorf("x: %w", provider.ErrNotCarried)}), false, false, false},
		{"ErrNotCarried candidate then failed candidate still fails", scripted(showResults("a", "b"), seasonOutcome{err: provider.ErrNotCarried}, seasonOutcome{err: errBoom}), false, false, true},
		{"matching candidate with seasons", scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(1)}), true, false, false},
		{"gapped hit is not a failure", scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(1), err: gapErr()}), true, true, false},
		{"failed candidate then hit keeps the failure", scripted(showResults("a", "b"), seasonOutcome{err: errBoom}, seasonOutcome{seasons: seasonsOf(1)}), true, false, true},
		{"empty candidate then hit is complete", scripted(showResults("a", "b"), seasonOutcome{}, seasonOutcome{seasons: seasonsOf(1)}), true, false, false},
		{"hit then unreached candidate is complete", scripted(showResults("a", "b"), seasonOutcome{seasons: seasonsOf(1)}, seasonOutcome{err: errBoom}), true, false, false},
		{"duplicate candidates collapse to one lookup", scripted(showResults("a", "a"), seasonOutcome{err: errBoom}, seasonOutcome{seasons: seasonsOf(1)}), false, false, true},
		{"every candidate fails", scripted(showResults("a", "b"), seasonOutcome{err: errBoom}, seasonOutcome{err: errBoom}), false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, failed := probeSeasons(context.Background(), c.p, req)
			if (hit != nil) != c.wantHit {
				t.Fatalf("hit = %v, want hit %v", hit, c.wantHit)
			}
			if hit != nil && hit.gapped != c.wantGapped {
				t.Errorf("gapped = %v, want %v", hit.gapped, c.wantGapped)
			}
			if failed != c.wantFailed {
				t.Errorf("failed = %v, want %v", failed, c.wantFailed)
			}
		})
	}
}

func TestSeasonAnswerAfterScan(t *testing.T) {
	gappedPrimary := func() seasonAnswer {
		return seasonAnswer{provider: &stubProvider{}, id: "p", seasons: seasonsOf(3), fromPrimary: true, gapped: true}
	}
	completePrimary := func() seasonAnswer {
		return seasonAnswer{provider: &stubProvider{}, id: "p", seasons: seasonsOf(3), fromPrimary: true}
	}
	down := &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom}
	empty := &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults}
	other := func() *scriptedLookup { return scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(3)}) }
	hasTwo := func() *scriptedLookup { return scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(2)}) }
	notCarried := func() *scriptedLookup {
		return scripted(showResults("a"), seasonOutcome{err: provider.ErrNotCarried})
	}
	flaky := func() *scriptedLookup {
		return scripted(showResults("a", "b"), seasonOutcome{err: errBoom}, seasonOutcome{seasons: seasonsOf(3)})
	}

	cases := []struct {
		name        string
		src         func() seasonAnswer
		chain       []provider.Provider
		wantOK      bool
		wantPartial bool
	}{
		{"gapped, empty chain: definitive absence", gappedPrimary, nil, false, false},
		{"gapped, fallback lists another season: definitive absence", gappedPrimary, []provider.Provider{other()}, false, false},
		{"gapped, fallback ErrNoResults: definitive absence", gappedPrimary, []provider.Provider{empty}, false, false},
		{"gapped, fallback search fails: uncertain", gappedPrimary, []provider.Provider{down}, false, true},
		{"gapped, fallback GetSeasons ErrNotCarried: definitive absence", gappedPrimary, []provider.Provider{notCarried()}, false, false},
		{"gapped, ErrNotCarried fallback and a listing fallback: definitive", gappedPrimary, []provider.Provider{notCarried(), other()}, false, false},
		{"gapped, ErrNotCarried fallback and a failing fallback: uncertain", gappedPrimary, []provider.Provider{notCarried(), down}, false, true},
		{"gapped, ErrNoResults fallback and a listing fallback: definitive", gappedPrimary, []provider.Provider{empty, other()}, false, false},
		{"gapped, earlier candidate failed then lists another season: uncertain", gappedPrimary, []provider.Provider{flaky()}, false, true},
		{"gapped, one fallback down but another has the season: accepted", gappedPrimary, []provider.Provider{down, hasTwo()}, true, false},
		{"complete primary, fallback down: primary's list stands", completePrimary, []provider.Provider{down}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chainOf(t, c.chain...)
			got, _, _, ok := seasonAcrossHits(&stubProvider{}, resolver.Request{ID: "p", Title: "Show", MediaType: media.TV}, c.src(), 2)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok && got.partial != c.wantPartial {
				t.Errorf("partial = %v, want %v", got.partial, c.wantPartial)
			}
		})
	}
}

func TestSeasonSourceKeepsAFailedScanWhenTheSeasonIsAbsent(t *testing.T) {
	req := resolver.Request{ID: "p", Title: "Show", MediaType: media.TV}
	listsThree := func() *scriptedLookup { return scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(3)}) }
	cases := []struct {
		name        string
		other       provider.Provider
		wantPartial bool
	}{
		{"another fallback times out: uncertain", &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom}, true},
		{"another fallback answers ErrNoResults: definitive", &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults}, false},
		{"another fallback answers with nothing matching: definitive", scripted(nil), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chainOf(t, listsThree(), c.other)
			primary := &stubProvider{seasonsErr: errBoom}
			src := seasonSource(primary, playRef{ID: "p", Title: "Show", Type: "tv"})
			got, _, _, ok := seasonAcrossHits(primary, req, src, 2)
			if ok {
				t.Fatal("season 2 must not be found")
			}
			if got.partial != c.wantPartial {
				t.Errorf("partial = %v, want %v", got.partial, c.wantPartial)
			}
		})
	}
}
