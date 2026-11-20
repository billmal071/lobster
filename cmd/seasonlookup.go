package cmd

import (
	"errors"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type lookupKind int

const (
	lookupAbsent lookupKind = iota
	lookupFailed
	lookupListed
)

type listGap int

const (
	gapNone listGap = iota
	gapShort
	gapCatalogue
)

// seasonLookup is one source's answer to "which seasons of this work do you
// have". Absent is an answer (ErrNoResults, ErrNotCarried, an empty list);
// failed is not. A listed answer keeps its gap: gapShort means the source may
// not have measured every season, gapCatalogue means it lists every season it
// carries and carries only some.
type seasonLookup struct {
	provider provider.Provider
	id       string
	kind     lookupKind
	gap      listGap
	seasons  []media.Season
	err      error
}

func classifyList(n int, err error) (lookupKind, listGap) {
	switch {
	case err == nil && n > 0:
		return lookupListed, gapNone
	case err == nil, errors.Is(err, provider.ErrNoResults), errors.Is(err, provider.ErrNotCarried) && n == 0:
		return lookupAbsent, gapNone
	case n > 0 && provider.IsCatalogueGap(err):
		return lookupListed, gapCatalogue
	case n > 0 && errors.Is(err, provider.ErrIncompleteEpisodeList):
		return lookupListed, gapShort
	}
	return lookupFailed, gapNone
}

func classifySeasonLookup(seasons []media.Season, err error) seasonLookup {
	kind, gap := classifyList(len(seasons), err)
	return seasonLookup{kind: kind, gap: gap, seasons: seasons, err: err}
}

type seasonVerdict int

const (
	seasonUndetermined seasonVerdict = iota
	seasonFound
	seasonAbsent
	seasonLookupFailed
	seasonNoList
)

// seasonResolution is combineSeasons' answer. gapSource names the provider whose
// catalogue gap makes an absence "does not carry" rather than "does not exist".
type seasonResolution struct {
	verdict   seasonVerdict
	answer    seasonAnswer
	season    media.Season
	gapSource provider.Provider
	anyListed bool
}

// combineSeasons is the one place a primary's season answer and the fallback
// chain's are weighed for want. A failed lookup never yields a refusal; a
// complete primary list is authoritative; every other absence needs a lookup
// that listed seasons and no failed or partial one beside it.
func combineSeasons(primary seasonLookup, hits []*seasonHit, scanFailed bool, want int) seasonResolution {
	res := combineLists(primary, hits, scanFailed, want)
	res.anyListed = primary.kind == lookupListed || len(hits) > 0
	return res
}

func combineLists(primary seasonLookup, hits []*seasonHit, scanFailed bool, want int) seasonResolution {
	if primary.kind == lookupListed {
		if sel, ok := pickSeason(primary.seasons, want); ok {
			return seasonResolution{verdict: seasonFound, season: sel, answer: seasonAnswer{
				seasonLookup: primary, fromPrimary: true, primary: primary, alts: hits, scanFailed: scanFailed,
			}}
		}
	}
	for i, h := range hits {
		if sel, ok := pickSeason(h.seasons, want); ok {
			lacks := primary.kind == lookupAbsent || (primary.kind == lookupListed && primary.gap != gapShort)
			return seasonResolution{verdict: seasonFound, season: sel, answer: seasonAnswer{
				seasonLookup: h.seasonLookup, primary: primary, alts: hits[i+1:], scanFailed: scanFailed, primaryLacks: lacks,
			}}
		}
	}

	lists := make([]seasonLookup, 0, len(hits)+1)
	if primary.kind == lookupListed {
		lists = append(lists, primary)
	}
	for _, h := range hits {
		lists = append(lists, h.seasonLookup)
	}
	for _, l := range lists {
		if l.gap == gapShort {
			return seasonResolution{verdict: seasonUndetermined}
		}
	}
	if primary.kind == lookupListed && primary.gap == gapNone {
		return seasonResolution{verdict: seasonAbsent}
	}
	if scanFailed {
		return seasonResolution{verdict: seasonUndetermined}
	}
	for _, l := range lists {
		if l.gap == gapCatalogue {
			return seasonResolution{verdict: seasonAbsent, gapSource: l.provider}
		}
	}
	switch {
	case len(lists) > 0:
		return seasonResolution{verdict: seasonAbsent}
	case primary.kind == lookupFailed:
		return seasonResolution{verdict: seasonLookupFailed}
	}
	return seasonResolution{verdict: seasonNoList}
}
