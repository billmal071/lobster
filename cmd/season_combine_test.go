package cmd

import (
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type catalogueStub struct{ *stubProvider }

func (catalogueStub) CatalogueSource() bool { return true }

func TestCombineSeasonsKeepsLaterHitsAsAlternatives(t *testing.T) {
	mk := func(nums ...int) *seasonHit {
		return &seasonHit{seasonLookup: seasonLookup{provider: &stubProvider{}, kind: lookupListed, seasons: seasonsOf(nums...)}}
	}
	first, second, third := mk(1), mk(1, 2), mk(2)
	got := combineSeasons(seasonLookup{kind: lookupAbsent}, []*seasonHit{first, second, third}, false, 2)
	if got.verdict != seasonFound || got.answer.provider != second.provider {
		t.Fatalf("verdict = %v, provider is the second hit: %v; want the first hit that has season 2", got.verdict, got.answer.provider == second.provider)
	}
	if len(got.answer.alts) != 1 || got.answer.alts[0] != third {
		t.Fatalf("alts = %v, want only the hits after the one used", got.answer.alts)
	}
}

func TestCombineSeasonsWithNoSeasonRequestedUsesTheFirstListedSeason(t *testing.T) {
	cases := []struct {
		name    string
		primary seasonLookup
		hits    []*seasonHit
		from    string
	}{
		{"primary lists", seasonLookup{kind: lookupListed, seasons: seasonsOf(3, 4)}, nil, "primary"},
		{"fallback lists", seasonLookup{kind: lookupFailed}, []*seasonHit{{seasonLookup: seasonLookup{kind: lookupListed, seasons: seasonsOf(5, 6)}}}, "hit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := combineSeasons(c.primary, c.hits, false, seasonUnspecified)
			want := c.primary.seasons
			if c.from == "hit" {
				want = c.hits[0].seasons
			}
			if got.verdict != seasonFound || got.season.Number != want[0].Number {
				t.Fatalf("verdict = %v, season = %d; want found, season %d", got.verdict, got.season.Number, want[0].Number)
			}
		})
	}
	if got := combineSeasons(seasonLookup{kind: lookupAbsent}, nil, false, seasonUnspecified); got.verdict != seasonNoList {
		t.Fatalf("nothing listed: verdict = %v, want no list", got.verdict)
	}
}

func TestCombineSeasonsTruthTable(t *testing.T) {
	prim := &stubProvider{}
	other := &stubProvider{}
	list := func(gap listGap, nums ...int) seasonLookup {
		return seasonLookup{provider: prim, id: "p", kind: lookupListed, gap: gap, seasons: seasonsOf(nums...)}
	}
	hit := func(gap listGap, nums ...int) *seasonHit {
		return &seasonHit{seasonLookup: seasonLookup{provider: other, id: "h", kind: lookupListed, gap: gap, seasons: seasonsOf(nums...)}}
	}
	catalogue := func(gap listGap, nums ...int) seasonLookup {
		l := list(gap, nums...)
		l.provider = catalogueStub{prim}
		return l
	}
	absent := seasonLookup{provider: prim, id: "p", kind: lookupAbsent}
	failed := seasonLookup{provider: prim, id: "p", kind: lookupFailed, err: errBoom}

	cases := []struct {
		name       string
		primary    seasonLookup
		hits       []*seasonHit
		scanFailed bool
		want       seasonVerdict
		gapSource  provider.Provider
		fromHit    bool
		wantGap    listGap
		primLacks  bool
	}{
		{"complete primary has the season", list(gapNone, 1, 2), nil, false, seasonFound, nil, false, gapNone, false},
		{"short primary has the season and keeps its gap", list(gapShort, 1, 2), nil, false, seasonFound, nil, false, gapShort, false},
		{"complete primary lacks it", list(gapNone, 1, 3), nil, false, seasonAbsent, nil, false, 0, false},
		{"complete primary lacks it, fallback lookup failed: the primary stands", list(gapNone, 1, 3), nil, true, seasonAbsent, nil, false, 0, false},
		{"complete primary lacks it, a fallback list is partial", list(gapNone, 1, 3), []*seasonHit{hit(gapShort, 1)}, false, seasonUndetermined, nil, false, 0, false},
		{"gapped primary lacks it, chain empty", list(gapCatalogue, 1, 3), nil, false, seasonAbsent, prim, false, 0, false},
		{"gapped primary lacks it, fallback lists complete without it: generic, no source named", list(gapCatalogue, 1, 3), []*seasonHit{hit(gapNone, 1, 3)}, false, seasonAbsent, nil, false, 0, false},
		{"gapped primary lacks it, fallback gapped too: the first is named", list(gapCatalogue, 1, 3), []*seasonHit{hit(gapCatalogue, 1, 3)}, false, seasonAbsent, prim, false, 0, false},
		{"complete primary lacks it, fallback has it", list(gapNone, 1, 3), []*seasonHit{hit(gapNone, 2)}, false, seasonFound, nil, true, gapNone, true},
		{"complete primary lacks it, fallback gapped without it: no source named", list(gapNone, 1, 3), []*seasonHit{hit(gapCatalogue, 1, 3)}, false, seasonAbsent, nil, false, 0, false},
		{"absent primary, fallback complete without it", absent, []*seasonHit{hit(gapNone, 1, 3)}, false, seasonAbsent, nil, false, 0, false},
		{"absent primary, fallback partial without it", absent, []*seasonHit{hit(gapShort, 1, 3)}, false, seasonUndetermined, nil, false, 0, false},
		{"failed primary, fallback partial without it", failed, []*seasonHit{hit(gapShort, 1, 3)}, false, seasonUndetermined, nil, false, 0, false},
		{"catalogue primary complete lacks it, fallback lookup failed", catalogue(gapNone, 1, 3), nil, true, seasonUndetermined, nil, false, 0, false},
		{"catalogue primary complete lacks it, nothing failed", catalogue(gapNone, 1, 3), nil, false, seasonAbsent, nil, false, 0, false},
		{"catalogue primary complete lacks it, fallback has it", catalogue(gapNone, 1, 3), []*seasonHit{hit(gapNone, 2)}, true, seasonFound, nil, true, gapNone, true},
		{"gapped primary lacks it, fallback lookup failed", list(gapCatalogue, 1, 3), nil, true, seasonUndetermined, nil, false, 0, false},
		{"gapped primary lacks it, a fallback list is partial: partial wins over the gap", list(gapCatalogue, 1, 3), []*seasonHit{hit(gapShort, 1, 3)}, false, seasonUndetermined, nil, false, 0, false},
		{"short primary lacks it", list(gapShort, 1, 3), nil, false, seasonUndetermined, nil, false, 0, false},
		{"short primary lacks it, fallback complete without it", list(gapShort, 1, 3), []*seasonHit{hit(gapNone, 1, 3)}, false, seasonUndetermined, nil, false, 0, false},
		{"primary lists nothing, chain empty", absent, nil, false, seasonNoList, nil, false, 0, false},
		{"primary lists nothing, fallback lookup failed", absent, nil, true, seasonUndetermined, nil, false, 0, false},
		{"primary failed, chain empty", failed, nil, false, seasonLookupFailed, nil, false, 0, false},
		{"primary failed, fallback lookup failed", failed, nil, true, seasonUndetermined, nil, false, 0, false},
		{"primary failed, fallback complete without it", failed, []*seasonHit{hit(gapNone, 1, 3)}, false, seasonAbsent, nil, false, 0, false},
		{"primary failed, fallback gapped without it names the fallback", failed, []*seasonHit{hit(gapCatalogue, 1, 3)}, false, seasonAbsent, other, false, 0, false},
		{"primary failed, fallback complete without it, another lookup failed", failed, []*seasonHit{hit(gapNone, 1, 3)}, true, seasonUndetermined, nil, false, 0, false},
		{"primary failed, fallback has it", failed, []*seasonHit{hit(gapNone, 1, 2)}, true, seasonFound, nil, true, gapNone, false},
		{"primary absent, gapped fallback has it keeps its source and gap", absent, []*seasonHit{hit(gapCatalogue, 2)}, false, seasonFound, nil, true, gapCatalogue, true},
		{"gapped primary lacks it, fallback has it", list(gapCatalogue, 1, 3), []*seasonHit{hit(gapNone, 2)}, false, seasonFound, nil, true, gapNone, true},
		{"short primary lacks it, fallback has it", list(gapShort, 1, 3), []*seasonHit{hit(gapNone, 2)}, false, seasonFound, nil, true, gapNone, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := combineSeasons(c.primary, c.hits, c.scanFailed, 2)
			if got.verdict != c.want {
				t.Fatalf("verdict = %v, want %v", got.verdict, c.want)
			}
			if got.gapSource != c.gapSource {
				t.Errorf("gapSource = %v, want %v", got.gapSource, c.gapSource)
			}
			if c.want != seasonFound {
				return
			}
			if (got.answer.provider == other) != c.fromHit || got.answer.fromPrimary == c.fromHit {
				t.Errorf("answer provider/fromPrimary wrong: fromHit want %v", c.fromHit)
			}
			if got.answer.gap != c.wantGap {
				t.Errorf("answer gap = %v, want %v", got.answer.gap, c.wantGap)
			}
			if got.answer.primaryLacks != c.primLacks {
				t.Errorf("primaryLacks = %v, want %v", got.answer.primaryLacks, c.primLacks)
			}
			if got.season.Number != 2 {
				t.Errorf("season = %d, want 2", got.season.Number)
			}
		})
	}
}

func TestClassifySeasonLookup(t *testing.T) {
	cases := []struct {
		name    string
		seasons int
		err     error
		kind    lookupKind
		gap     listGap
	}{
		{"seasons, no error", 2, nil, lookupListed, gapNone},
		{"empty, no error", 0, nil, lookupAbsent, gapNone},
		{"ErrNoResults", 0, provider.ErrNoResults, lookupAbsent, gapNone},
		{"ErrNotCarried", 0, provider.ErrNotCarried, lookupAbsent, gapNone},
		{"transport error", 0, errBoom, lookupFailed, gapNone},
		{"incomplete list", 2, provider.ErrIncompleteEpisodeList, lookupListed, gapShort},
		{"catalogue gap", 2, gapErr(), lookupListed, gapCatalogue},
		{"incomplete error with no seasons", 0, provider.ErrIncompleteEpisodeList, lookupFailed, gapNone},
		{"transport error beside seasons", 2, errBoom, lookupFailed, gapNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifySeasonLookup(seasonsOf(make([]int, c.seasons)...), c.err)
			if got.kind != c.kind || got.gap != c.gap {
				t.Fatalf("got kind=%v gap=%v, want kind=%v gap=%v", got.kind, got.gap, c.kind, c.gap)
			}
		})
	}
}

func TestTUIEpisodeListFallbackCarriesTheGapStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"a gapped chain list is partial", gapErr(), true},
		{"a complete chain list is not", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			fb := &stubProvider{
				results:         []media.SearchResult{{ID: "tv/1", Title: "Show", Type: media.TV}},
				seasons:         seasonsOf(2),
				episodes:        []media.Episode{{ID: "a", Number: 2}, {ID: "b", Number: 5}},
				episodesWithErr: []media.Episode{{ID: "a", Number: 2}, {ID: "b", Number: 5}},
				episodesErr:     c.err,
			}
			chainOf(t, fb)
			_, partial, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "tv/1", Title: "Show", Type: media.TV}, 2)
			if err != nil {
				t.Fatal(err)
			}
			if partial != c.want {
				t.Fatalf("partial = %v, want %v", partial, c.want)
			}
		})
	}
}

func TestTUIEpisodeListFallbackRefusesAPrefixThatMayBeTruncated(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"a prefix cut short by a failed probe", fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)},
		{"a prefix whose end was not confirmed", fmt.Errorf("%w: %w: episode 3 answered 404 once", provider.ErrIncompleteEpisodeList, provider.ErrUnconfirmedEpisodeList)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			fb := &stubProvider{
				results:         []media.SearchResult{{ID: "tv/1", Title: "Show", Type: media.TV}},
				seasons:         seasonsOf(2),
				episodesWithErr: []media.Episode{{ID: "a", Number: 1}, {ID: "b", Number: 2}},
				episodesErr:     c.err,
			}
			chainOf(t, fb)
			eps, partial, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "tv/1", Title: "Show", Type: media.TV}, 2)
			if err == nil {
				t.Fatalf("offered %d episodes (partial=%v) as the season; want an error so the dialog reports its own provider's failure", len(eps), partial)
			}
			if len(eps) != 0 {
				t.Fatalf("returned %d episodes beside the error; want none", len(eps))
			}
		})
	}
}

func TestTUIEpisodeListFallbackPassesOverATruncatedPrefixForALaterList(t *testing.T) {
	truncated := fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)
	cases := []struct {
		name        string
		later       *stubProvider
		wantEps     int
		wantPartial bool
	}{
		{"a later complete list", chainMember("b", numbered(1, 2, 3), nil), 3, false},
		{"a later gapped list stays partial", chainMember("b", numbered(1, 5), gapErr()), 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			chainOf(t, chainMember("a", numbered(1), truncated), c.later)
			eps, partial, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV}, 1)
			if err != nil || len(eps) != c.wantEps || partial != c.wantPartial {
				t.Fatalf("episodes = %d, partial = %v, err = %v; want %d episodes, partial %v", len(eps), partial, err, c.wantEps, c.wantPartial)
			}
		})
	}
}

func TestTUIEpisodeListFallbackRefusesANegativeSeason(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainMember("a", numbered(1, 2), nil))
	eps, _, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV}, -1)
	if err == nil || len(eps) != 0 {
		t.Fatalf("episodes = %d, err = %v; want an error and no list for a negative season", len(eps), err)
	}
}

func TestTUIEpisodeListFallbackSaysWhenAFallbackLookupFailed(t *testing.T) {
	cases := []struct {
		name   string
		member *stubProvider
		want   string
	}{
		{"the search failed", &stubProvider{searchErr: errBoom}, "a fallback lookup failed"},
		{"the listing failed", chainMember("a", nil, errBoom), "a fallback lookup failed"},
		{"nothing failed", chainMember("a", nil, nil), `could list season 1 of "Some Show"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			chainOf(t, c.member)
			_, _, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "x", Title: "Some Show", Type: media.TV}, 1)
			if err == nil || !strings.HasSuffix(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to end with %q", err, c.want)
			}
		})
	}
}
