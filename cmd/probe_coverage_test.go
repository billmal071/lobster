package cmd

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

func hitIDs(hits []*seasonHit) []string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.id)
	}
	return ids
}

func TestProbeSeasonsKeepsEachCandidateThatAddsAWantedSeason(t *testing.T) {
	req := resolver.Request{ID: "x/1", Title: "Show", MediaType: media.TV}
	only := func(nums ...int) seasonOutcome { return seasonOutcome{seasons: seasonsOf(nums...)} }
	cut := seasonOutcome{seasons: seasonsOf(2), err: fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList)}
	cases := []struct {
		name       string
		p          *scriptedLookup
		wants      []int
		wantIDs    []string
		wantFailed bool
	}{
		{"two candidates each list one wanted season", scripted(showResults("a", "b"), only(1), only(2)), []int{1, 2}, []string{"a", "b"}, false},
		{"a later candidate that adds no wanted season is dropped", scripted(showResults("a", "b"), only(1), only(1)), []int{1, 2}, []string{"a"}, false},
		{"a later candidate listing only unwanted seasons is dropped", scripted(showResults("a", "b"), only(1), only(7)), []int{1, 2}, []string{"a"}, false},
		{"a season already covered is not kept twice", scripted(showResults("a", "b", "c"), only(1), only(2), only(2)), []int{1, 2}, []string{"a", "b"}, false},
		{"the first listed stays first when it lists no wanted season", scripted(showResults("a", "b"), only(7), only(2)), []int{1, 2}, []string{"a", "b"}, false},
		{"three candidates cover three seasons in provider order", scripted(showResults("a", "b", "c"), only(3), only(1), only(2)), []int{1, 2, 3}, []string{"a", "b", "c"}, false},
		{"a failed lookup between two kept candidates is reported", scripted(showResults("a", "b", "c"), only(1), seasonOutcome{err: errBoom}, only(2)), []int{1, 2}, []string{"a", "c"}, true},
		{"a later list cut short is kept for its season and leaves the scan unsure", scripted(showResults("a", "b"), only(1), cut), []int{1, 2}, []string{"a", "b"}, true},
		{"a candidate listing every wanted season is returned alone", scripted(showResults("a", "b", "c"), only(1), only(1, 2, 3), only(2)), []int{1, 2, 3}, []string{"b"}, false},
		{"one wanted season keeps a single candidate", scripted(showResults("a", "b"), only(1, 2), only(1, 2, 3)), []int{4}, []string{"a"}, false},
		{"a duplicated wanted season", scripted(showResults("a", "b"), only(1), only(2)), []int{2, 1, 2}, []string{"a", "b"}, false},
		{"no wanted season keeps the first listed", scripted(showResults("a", "b"), only(1), only(2)), nil, []string{"a"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits, failed := probeSeasons(context.Background(), c.p, req, c.wants)
			if got := hitIDs(hits); !reflect.DeepEqual(got, c.wantIDs) {
				t.Fatalf("candidates kept = %v, want %v", got, c.wantIDs)
			}
			if failed != c.wantFailed {
				t.Errorf("failed = %v, want %v", failed, c.wantFailed)
			}
		})
	}
}

func splitShowBatch(t *testing.T) (*scriptedLookup, *seasonLister) {
	t.Helper()
	hostileEnv(t)
	fb := scripted(showResults("a", "b"), seasonOutcome{seasons: seasonsOf(1)}, seasonOutcome{seasons: seasonsOf(2)})
	fb.stubProvider.episodes = []media.Episode{{ID: "e1", Number: 1}}
	chainOf(t, fb)
	return fb, newSeasonLister(&stubProvider{episodesErr: errBoom}, media.SearchResult{ID: "x/1", Title: "Show", Type: media.TV}, seasonsOf(1, 2)...)
}

func TestBatchListerReachesASeasonOnlyALaterCandidateLists(t *testing.T) {
	fb, l := splitShowBatch(t)
	for _, s := range seasonsOf(1, 2) {
		if eps, err := l.episodes(s); err != nil || len(eps) != 1 {
			t.Fatalf("season %d: episodes = %d, err = %v; want the list of the candidate that has this season", s.Number, len(eps), err)
		}
	}
	if fb.calls != 2 {
		t.Fatalf("GetSeasons calls = %d, want 2: one scan for the whole batch", fb.calls)
	}
}

func TestBatchListerRescansASplitShowAfterARetryForgetsTheScan(t *testing.T) {
	fb, l := splitShowBatch(t)
	if _, err := l.episodes(media.Season{ID: "2", Number: 2}); err != nil {
		t.Fatalf("first listing: %v", err)
	}
	l.forgetScan()
	fb.calls = 0
	if eps, err := l.episodes(media.Season{ID: "2", Number: 2}); err != nil || len(eps) != 1 {
		t.Fatalf("after a retry: episodes = %d, err = %v; want season 2 from the later candidate again", len(eps), err)
	}
}
