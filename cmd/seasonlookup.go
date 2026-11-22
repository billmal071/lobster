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
	case err == nil, errors.Is(err, provider.ErrNoResults), n == 0 && (errors.Is(err, provider.ErrNotCarried) || errors.Is(err, provider.ErrNoEpisodeIndex)):
		return lookupAbsent, gapNone
	case provider.ListedWithCatalogueGap(n, err):
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
// chain's are weighed for want. A fallback lookup that failed, or a partial list
// anywhere, never yields a refusal. Only a complete list from a non-catalogue
// primary is final on its own; any other absence needs a list from some source
// and no failed fallback lookup beside it. The primary's own failure does not
// block that: lists that did answer refuse, which keeps the resolver from
// streaming a season no source lists. gapSource is set only when every list
// consulted is a catalogue gap.
func combineSeasons(primary seasonLookup, hits []*seasonHit, scanFailed bool, want int) seasonResolution {
	res := combineLists(primary, hits, scanFailed, want)
	res.anyListed = primary.kind == lookupListed || len(hits) > 0
	return res
}

func combineLists(primary seasonLookup, hits []*seasonHit, scanFailed bool, want int) seasonResolution {
	if primary.kind == lookupListed {
		if sel, ok := pickSeason(primary.seasons, want); ok {
			return seasonResolution{verdict: seasonFound, season: sel, answer: seasonAnswer{
				seasonLookup: primary, fromPrimary: true, alts: hits, scanFailed: scanFailed,
			}}
		}
	}
	for i, h := range hits {
		if sel, ok := pickSeason(h.seasons, want); ok {
			lacks := primary.kind == lookupAbsent || (primary.kind == lookupListed && primary.gap != gapShort)
			return seasonResolution{verdict: seasonFound, season: sel, answer: seasonAnswer{
				seasonLookup: h.seasonLookup, alts: hits[i+1:], scanFailed: scanFailed, primaryLacks: lacks,
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
	if primary.kind == lookupListed && primary.gap == gapNone && !provider.IsCatalogueSource(primary.provider) {
		return seasonResolution{verdict: seasonAbsent}
	}
	if scanFailed {
		return seasonResolution{verdict: seasonUndetermined}
	}
	if len(lists) == 0 {
		if primary.kind == lookupFailed {
			return seasonResolution{verdict: seasonLookupFailed}
		}
		return seasonResolution{verdict: seasonNoList}
	}
	for _, l := range lists {
		if l.gap != gapCatalogue {
			return seasonResolution{verdict: seasonAbsent}
		}
	}
	return seasonResolution{verdict: seasonAbsent, gapSource: lists[0].provider}
}
