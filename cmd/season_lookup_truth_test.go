package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
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
			hit, failed := probeSeasons(context.Background(), c.p, req, nil)
			if (hit != nil) != c.wantHit {
				t.Fatalf("hit = %v, want hit %v", hit, c.wantHit)
			}
			if hit != nil && (hit.gap == gapCatalogue) != c.wantGapped {
				t.Errorf("gapped = %v, want %v", hit.gap == gapCatalogue, c.wantGapped)
			}
			if failed != c.wantFailed {
				t.Errorf("failed = %v, want %v", failed, c.wantFailed)
			}
		})
	}
}

func TestSeasonAnswerAfterScan(t *testing.T) {
	gappedPrimary := func() seasonLookup {
		return seasonLookup{provider: &stubProvider{}, id: "p", kind: lookupListed, gap: gapCatalogue, seasons: seasonsOf(3)}
	}
	completePrimary := func() seasonLookup {
		return seasonLookup{provider: &stubProvider{}, id: "p", kind: lookupListed, seasons: seasonsOf(3)}
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
		src         func() seasonLookup
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
			got := resolveSeason(&stubProvider{}, resolver.Request{ID: "p", Title: "Show", MediaType: media.TV}, c.src(), 2)
			if ok := got.verdict == seasonFound; ok != c.wantOK {
				t.Fatalf("verdict = %v, want found %v", got.verdict, c.wantOK)
			}
			if got.verdict != seasonFound && (got.verdict == seasonUndetermined) != c.wantPartial {
				t.Errorf("verdict = %v, want undetermined %v", got.verdict, c.wantPartial)
			}
		})
	}
}

func TestResolveSeasonKeepsAFailedScanWhenTheSeasonIsAbsent(t *testing.T) {
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
			got := resolveSeason(primary, req, lookupPrimarySeasons(primary, "p"), 2)
			if got.verdict == seasonFound {
				t.Fatal("season 2 must not be found")
			}
			if (got.verdict == seasonUndetermined) != c.wantPartial {
				t.Errorf("verdict = %v, want undetermined %v", got.verdict, c.wantPartial)
			}
		})
	}
}

type seasonsFailingStream struct {
	*recordingStreamProvider
}

func (seasonsFailingStream) GetSeasons(string) ([]media.Season, error) { return nil, errBoom }

func episodesErrCode(t *testing.T, primary provider.Provider, chain ...provider.Provider) string {
	t.Helper()
	code, _ := episodesErrEnvelope(t, primary, chain...)
	return code
}

func episodesErrEnvelope(t *testing.T, primary provider.Provider, chain ...provider.Provider) (code, message string) {
	t.Helper()
	hostileEnv(t)
	buf := captureAgentOut(t)
	chainOf(t, chain...)
	withStubProvider(t, primary)
	withEpisodesFlags(t, tvRef(t, ""), 2)
	if err := episodesRun(episodesCmd, nil); err == nil {
		t.Fatalf("episodesRun succeeded, want an error\n%s", buf.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, buf.String())
	}
	return env.Error.Code, env.Error.Message
}

func TestEpisodesSeasonLookupTruthTable(t *testing.T) {
	down := func() provider.Provider { return &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom} }
	empty := func() provider.Provider {
		return &scriptedLookup{stubProvider: &stubProvider{}, searchErr: provider.ErrNoResults}
	}
	cases := []struct {
		name    string
		primary provider.Provider
		chain   func() []provider.Provider
		want    string
	}{
		{"primary answers ErrNoResults, every fallback answers ErrNoResults", &stubProvider{seasonsErr: provider.ErrNoResults}, func() []provider.Provider { return []provider.Provider{empty()} }, "no_results"},
		{"complete catalogue primary omits the season, fallback lookup failed", catalogueStub{&stubProvider{seasons: seasonsOf(1, 3)}}, func() []provider.Provider { return []provider.Provider{down()} }, "providers_failed"},
		{"complete catalogue primary omits the season, every fallback answered", catalogueStub{&stubProvider{seasons: seasonsOf(1, 3)}}, func() []provider.Provider { return []provider.Provider{empty()} }, "no_results"},
		{"primary lists nothing, fallback lookup failed", &stubProvider{}, func() []provider.Provider { return []provider.Provider{down()} }, "providers_failed"},
		{"primary lists nothing, every fallback answers ErrNoResults", &stubProvider{}, func() []provider.Provider { return []provider.Provider{empty()} }, "no_results"},
		{"primary lists nothing, one fallback failed and one answers empty", &stubProvider{}, func() []provider.Provider { return []provider.Provider{empty(), down()} }, "providers_failed"},
		{"primary errors, fallback lookup failed", &stubProvider{seasonsErr: errBoom}, func() []provider.Provider { return []provider.Provider{down()} }, "providers_failed"},
		{"primary answers ErrNotCarried, every fallback answers ErrNoResults", &stubProvider{seasonsErr: provider.ErrNotCarried}, func() []provider.Provider { return []provider.Provider{empty()} }, "no_results"},
		{"primary answers ErrNotCarried, a fallback lookup failed", &stubProvider{seasonsErr: provider.ErrNotCarried}, func() []provider.Provider { return []provider.Provider{down()} }, "providers_failed"},
		{"complete non-empty primary omits the season, fallback down", &stubProvider{seasons: seasonsOf(1, 3)}, func() []provider.Provider { return []provider.Provider{down()} }, "no_results"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := episodesErrCode(t, c.primary, c.chain()...); got != c.want {
				t.Fatalf("error code = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveAndPlaySeasonLookupTruthTable(t *testing.T) {
	sel := media.SearchResult{ID: "tv/1403", Title: "Show", Type: media.TV}
	listsOneAndThree := func() provider.Provider {
		return scripted(showResults("a"), seasonOutcome{seasons: seasonsOf(1, 3)})
	}
	cases := []struct {
		name        string
		failedFirst bool
		wantRefused bool
	}{
		{"another fallback could not enumerate: the resolver is tried by number", true, false},
		{"every fallback answered: season is absent", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostileEnv(t)
			playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
			rec := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
			var streamer provider.Provider = rec
			if c.failedFirst {
				streamer = seasonsFailingStream{rec}
			}
			withFallbackChain(t, listsOneAndThree(), streamer)

			err := resolveAndPlay(&stubProvider{seasonsErr: errBoom}, sel, 2, 1)
			var notFound errSeasonNotFound
			if refused := errors.As(err, &notFound); refused != c.wantRefused {
				t.Fatalf("resolveAndPlay = %v, refused = %v, want %v", err, refused, c.wantRefused)
			}
			if !c.wantRefused && rec.episodeAsked() != "1403:2:1" {
				t.Fatalf("resolver asked for %q, want 1403:2:1", rec.episodeAsked())
			}
		})
	}
}

func TestEpisodesNamesThePrimaryErrorWhenNothingElseListedSeasons(t *testing.T) {
	down := &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom}
	code, message := episodesErrEnvelope(t, &stubProvider{seasonsErr: errors.New("primary exploded")}, down)
	if code != "providers_failed" {
		t.Fatalf("code = %q, want providers_failed", code)
	}
	if !strings.Contains(message, "primary exploded") || !strings.Contains(message, "reported") {
		t.Fatalf("message %q drops the primary's own error", message)
	}
}

func TestValidateSeasonEpisodeDefersForACatalogueListWhenAFallbackLookupFailed(t *testing.T) {
	hostileEnv(t)
	chainOf(t, &scriptedLookup{stubProvider: &stubProvider{}, searchErr: errBoom})
	primary := catalogueStub{&stubProvider{seasons: seasonsOf(1, 3)}}
	if err := validateSeasonEpisode(primary, playRef{ID: "p", Title: "Show", Type: "tv"}, 5, 1); err != nil {
		t.Fatalf("validateSeasonEpisode = %v, want nil: a failed fallback lookup cannot turn a catalogue's partial list into a refusal", err)
	}
}

func TestProbeSeasonsPrefersACandidateListingTheWantedSeasons(t *testing.T) {
	req := resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}
	short, full := seasonOutcome{seasons: seasonsOf(1, 2)}, seasonOutcome{seasons: seasonsOf(1, 2, 3, 4)}
	cases := []struct {
		name       string
		p          *scriptedLookup
		wants      []int
		wantID     string
		wantFailed bool
		wantCalls  int
	}{
		{"a later candidate lists the wanted season", scripted(showResults("a", "b"), short, full), []int{4}, "b", false, 2},
		{"no candidate lists it: the first listed is kept", scripted(showResults("a", "b"), short, seasonOutcome{seasons: seasonsOf(1, 2, 3)}), []int{4}, "a", false, 2},
		{"a later lookup that fails keeps the first listed and the failure", scripted(showResults("a", "b"), short, seasonOutcome{err: errBoom}), []int{4}, "a", true, 2},
		{"a later candidate with no seasons keeps the first listed", scripted(showResults("a", "b"), short, seasonOutcome{}), []int{4}, "a", false, 2},
		{"a later candidate that does not carry the show keeps the first listed", scripted(showResults("a", "b"), short, seasonOutcome{err: provider.ErrNotCarried}), []int{4}, "a", false, 2},
		{"the first listed already has it: later candidates are not probed", scripted(showResults("a", "b"), short, full), []int{2}, "a", false, 1},
		{"no wanted season takes the first listed", scripted(showResults("a", "b"), short, full), nil, "a", false, 1},
		{"an unspecified season takes the first listed", scripted(showResults("a", "b"), short, full), []int{seasonUnspecified}, "a", false, 1},
		{"every wanted season must be listed", scripted(showResults("a", "b", "c"), short, seasonOutcome{seasons: seasonsOf(4)}, full), []int{1, 4}, "c", false, 3},
		{"a duplicated wanted season", scripted(showResults("a", "b"), short, full), []int{4, 4}, "b", false, 2},
		{"season 0 is a season", scripted(showResults("a", "b"), short, seasonOutcome{seasons: seasonsOf(0, 1)}), []int{0}, "b", false, 2},
		{"a gapped later list that has it", scripted(showResults("a", "b"), short, seasonOutcome{seasons: seasonsOf(4), err: gapErr()}), []int{4}, "b", false, 2},
		{"a later list cut short that lacks it leaves the scan unsure", scripted(showResults("a", "b"), short, seasonOutcome{seasons: seasonsOf(1), err: fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)}), []int{4}, "a", true, 2},
		{"a later gapped list that lacks it is an answer", scripted(showResults("a", "b"), short, seasonOutcome{seasons: seasonsOf(1), err: gapErr()}), []int{4}, "a", false, 2},
		{"a failure ahead of both is kept", scripted(showResults("z", "a", "b"), seasonOutcome{err: errBoom}, short, full), []int{4}, "b", true, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, failed := probeSeasons(context.Background(), c.p, req, c.wants)
			if hit == nil || hit.id != c.wantID {
				t.Fatalf("hit = %+v, want candidate %q", hit, c.wantID)
			}
			if failed != c.wantFailed {
				t.Errorf("failed = %v, want %v", failed, c.wantFailed)
			}
			if c.p.calls != c.wantCalls {
				t.Errorf("GetSeasons calls = %d, want %d", c.p.calls, c.wantCalls)
			}
		})
	}
}

func TestResolveSeasonFindsASeasonOnlyALaterCandidateLists(t *testing.T) {
	hostileEnv(t)
	chainOf(t, scripted(showResults("a", "b"), seasonOutcome{seasons: seasonsOf(1, 2)}, seasonOutcome{seasons: seasonsOf(1, 2, 3, 4)}))
	req := resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}
	res := resolveSeason(&stubProvider{}, req, seasonLookup{kind: lookupAbsent}, 4)
	if res.verdict != seasonFound || res.answer.id != "b" || res.season.Number != 4 {
		t.Fatalf("verdict = %v from %q season %d, want season 4 found on candidate b", res.verdict, res.answer.id, res.season.Number)
	}
}

func TestBatchListerScansForEverySeasonOfTheBatch(t *testing.T) {
	hostileEnv(t)
	fb := scripted(showResults("a", "b"), seasonOutcome{seasons: seasonsOf(1, 2)}, seasonOutcome{seasons: seasonsOf(1, 2, 3)})
	fb.stubProvider.episodes = []media.Episode{{ID: "e1", Number: 1}}
	chainOf(t, fb)
	primary := &stubProvider{episodesErr: errBoom}
	batch := seasonsOf(1, 3)
	l := newSeasonLister(primary, media.SearchResult{ID: "x/1", Title: "Show", Type: media.TV}, append(seasonsOf(-2), batch...)...)
	for _, s := range batch {
		if eps, err := l.episodes(s); err != nil || len(eps) != 1 {
			t.Fatalf("season %d: episodes = %d, err = %v; want the list of the candidate that has every season of the batch", s.Number, len(eps), err)
		}
	}
	if fb.calls != 2 {
		t.Fatalf("GetSeasons calls = %d, want 2: one scan for the whole batch", fb.calls)
	}
}

func laterCandidateHasSeason(season int) *scriptedLookup {
	var earlier []int
	for n := 1; n < season; n++ {
		earlier = append(earlier, n)
	}
	results := []media.SearchResult{{ID: "a", Title: "Some Show", Type: media.TV}, {ID: "b", Title: "Some Show", Type: media.TV}}
	fb := scripted(results, seasonOutcome{seasons: seasonsOf(earlier...)}, seasonOutcome{seasons: seasonsOf(append(earlier, season)...)})
	fb.stubProvider.episodes = []media.Episode{{ID: "e1", Number: 1}}
	return fb
}

func TestSingleSeasonScansAskForTheirSeason(t *testing.T) {
	sel := media.SearchResult{ID: "x/1", Title: "Some Show", Type: media.TV}

	t.Run("the interactive episode-list recovery", func(t *testing.T) {
		hostileEnv(t)
		chainOf(t, laterCandidateHasSeason(4))
		a := fallbackEpisodeList(&stubProvider{}, sel, 4)
		if a == nil || a.hit.id != "b" || a.season.Number != 4 {
			t.Fatalf("answer = %+v, want season 4 from candidate b", a)
		}
	})

	t.Run("the download dialog", func(t *testing.T) {
		hostileEnv(t)
		chainOf(t, laterCandidateHasSeason(4))
		eps, _, err := tuiEpisodeListFallback(&stubProvider{}, sel, 4)
		if err != nil || len(eps) != 1 {
			t.Fatalf("episodes = %d, err = %v; want candidate b's list of season 4", len(eps), err)
		}
	})

	t.Run("the episodes command", func(t *testing.T) {
		hostileEnv(t)
		captureAgentOut(t)
		primary := twoSeasonStub()
		primary.episodesErr = errBoom
		withStubProvider(t, primary)
		chainOf(t, laterCandidateHasSeason(2))
		withEpisodesFlags(t, tvRef(t, ""), 2)
		if err := episodesRun(episodesCmd, nil); err != nil {
			t.Fatalf("episodesRun = %v, want season 2 listed from candidate b", err)
		}
	})
}

func TestResolveSeasonIsUnsureWhenAPassedOverCandidateListWasCutShort(t *testing.T) {
	hostileEnv(t)
	cut := seasonOutcome{seasons: seasonsOf(1), err: fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)}
	chainOf(t, scripted(showResults("a", "b"), seasonOutcome{seasons: seasonsOf(1)}, cut))
	req := resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}
	res := resolveSeason(&stubProvider{}, req, seasonLookup{kind: lookupAbsent}, 2)
	if res.verdict != seasonUndetermined {
		t.Fatalf("verdict = %v, want undetermined: candidate b's list stopped early, so season 2 may exist", res.verdict)
	}
}
