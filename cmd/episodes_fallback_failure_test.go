package cmd

import (
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func emptySeasonTwoPrimary() *stubProvider {
	return &stubProvider{seasons: seasonsOf(1, 2)}
}

func chainListing(episodes []media.Episode, episodesErr error) *scriptedLookup {
	fb := scripted([]media.SearchResult{{ID: "a", Title: "Some Show", Type: media.TV}}, seasonOutcome{seasons: seasonsOf(1, 2)})
	fb.stubProvider.episodes, fb.stubProvider.episodesErr = episodes, episodesErr
	return fb
}

func noEpisodeIndex() error {
	return fmt.Errorf("stub: %w; no index", provider.ErrNoEpisodeIndex)
}

func TestClassifyListReadsNoEpisodeIndexAsAnAnswer(t *testing.T) {
	cases := []struct {
		name string
		n    int
		err  error
		want lookupKind
	}{
		{"no list and no episode index", 0, noEpisodeIndex(), lookupAbsent},
		{"the bare sentinel", 0, provider.ErrNoEpisodeIndex, lookupAbsent},
		{"a list beside a no-index error is not an answer", 2, noEpisodeIndex(), lookupFailed},
		{"any other error with no list", 0, errBoom, lookupFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := classifyList(c.n, c.err); got != c.want {
				t.Fatalf("kind = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBatchSkipsAnEmptySeasonWhenTheOnlyFallbackHasNoEpisodeIndex(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainListing(nil, noEpisodeIndex()))
	l := newSeasonLister(emptySeasonTwoPrimary(), media.SearchResult{ID: "x/1", Title: "Some Show", Type: media.TV}, seasonsOf(1, 2)...)
	if eps, err := l.episodes(media.Season{ID: "2", Number: 2}); err != nil || len(eps) != 0 {
		t.Fatalf("episodes = %d, err = %v; want an empty season with no error, since no lookup failed", len(eps), err)
	}
}

func TestBatchCountsAnEmptySeasonAsUnlistedWhenAFallbackLookupFailed(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainListing(nil, noEpisodeIndex()), chainListing(nil, errBoom))
	l := newSeasonLister(emptySeasonTwoPrimary(), media.SearchResult{ID: "x/1", Title: "Some Show", Type: media.TV}, seasonsOf(1, 2)...)
	if _, err := l.episodes(media.Season{ID: "2", Number: 2}); err == nil {
		t.Fatal("err = nil, want the failed fallback lookup reported")
	}
}

func TestDialogFallbackDoesNotCallANoIndexSourceAFailedLookup(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainListing(nil, noEpisodeIndex()))
	_, _, err := tuiEpisodeListFallback(&stubProvider{}, media.SearchResult{ID: "x/1", Title: "Some Show", Type: media.TV}, 2)
	if err == nil || strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("err = %v, want a refusal that names no failed lookup", err)
	}
}

func TestEpisodesEmptySeasonTruthTable(t *testing.T) {
	down := func() provider.Provider { return &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom} }
	absent := func() provider.Provider {
		return &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults}
	}
	cases := []struct {
		name    string
		primary provider.Provider
		chain   []provider.Provider
		want    string
	}{
		{"source lists no episodes, a fallback search failed", emptySeasonTwoPrimary(), []provider.Provider{down()}, "providers_failed"},
		{"source lists no episodes, a fallback's episode lookup failed", emptySeasonTwoPrimary(), []provider.Provider{chainListing(nil, errBoom)}, "providers_failed"},
		{"source lists no episodes, one fallback answered and one failed", emptySeasonTwoPrimary(), []provider.Provider{absent(), down()}, "providers_failed"},
		{"source lists no episodes, every fallback answered without the show", emptySeasonTwoPrimary(), []provider.Provider{absent()}, "no_results"},
		{"source lists no episodes, a fallback lists the season with no episodes", emptySeasonTwoPrimary(), []provider.Provider{chainListing(nil, nil)}, "no_results"},
		{"source lists no episodes, a fallback has no episode index", emptySeasonTwoPrimary(), []provider.Provider{chainListing(nil, noEpisodeIndex())}, "no_results"},
		{"source lists no episodes, one fallback has no episode index and one failed", emptySeasonTwoPrimary(), []provider.Provider{chainListing(nil, noEpisodeIndex()), chainListing(nil, errBoom)}, "providers_failed"},
		{"source lists no episodes, no fallback is configured", emptySeasonTwoPrimary(), nil, "no_results"},
		{"season from a fallback with no episodes, a later fallback search failed", &stubProvider{}, []provider.Provider{chainListing(nil, nil), down()}, "providers_failed"},
		{"season from a fallback with no episodes, a later fallback's episode lookup failed", &stubProvider{}, []provider.Provider{chainListing(nil, nil), chainListing(nil, errBoom)}, "providers_failed"},
		{"season from a fallback with no episodes, the rest answered without the show", &stubProvider{}, []provider.Provider{chainListing(nil, nil), absent()}, "no_results"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := episodesErrCode(t, c.primary, c.chain...); got != c.want {
				t.Fatalf("error code = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEpisodesListsFromAFallbackBesideOneThatFailed(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)
	chainOf(t, &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom}, chainListing([]media.Episode{{ID: "e1", Number: 1}}, nil))
	withStubProvider(t, emptySeasonTwoPrimary())
	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v, want the listing fallback's episodes\n%s", err, buf.String())
	}
}
