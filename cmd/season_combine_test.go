package cmd

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func TestCombineSeasonsTruthTable(t *testing.T) {
	prim := &stubProvider{}
	other := &stubProvider{}
	list := func(gap listGap, nums ...int) seasonLookup {
		return seasonLookup{provider: prim, id: "p", kind: lookupListed, gap: gap, seasons: seasonsOf(nums...)}
	}
	hit := func(gap listGap, nums ...int) *seasonHit {
		return &seasonHit{seasonLookup: seasonLookup{provider: other, id: "h", kind: lookupListed, gap: gap, seasons: seasonsOf(nums...)}}
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
		{"gapped primary lacks it, fallback lists complete without it", list(gapCatalogue, 1, 3), []*seasonHit{hit(gapNone, 1, 3)}, false, seasonAbsent, prim, false, 0, false},
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
