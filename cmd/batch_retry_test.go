package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"lobster/internal/media"
)

func confirmRetries(t *testing.T, yes int) {
	t.Helper()
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	if err := os.WriteFile(count, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("writing the answer count: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nread n < '%s'\nif [ \"$n\" -ge %d ]; then exit 97; fi\necho $((n+1)) > '%s'\nprintf '0\\tYes\\n'\n", count, yes, count)
	if err := os.WriteFile(filepath.Join(dir, "fzf"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fzf shim: %v", err)
	}
	t.Setenv("PATH", dir)
}

type flakyChainProvider struct {
	*scanCountingChainProvider

	mu              sync.Mutex
	searchFailures  int
	listingFailures map[string]int
}

func (p *flakyChainProvider) Search(q string) ([]media.SearchResult, error) {
	p.mu.Lock()
	fail := p.searchFailures > 0
	if fail {
		p.searchFailures--
	}
	p.mu.Unlock()
	if fail {
		return nil, errors.New("search answered 503")
	}
	return p.scanCountingChainProvider.Search(q)
}

func (p *flakyChainProvider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	p.mu.Lock()
	fail := p.listingFailures[seasonID] > 0
	if fail {
		p.listingFailures[seasonID]--
	}
	p.mu.Unlock()
	if fail {
		return nil, errors.New("listing answered 503")
	}
	return p.scanCountingChainProvider.GetEpisodes(id, seasonID)
}

func flakyBatch(t *testing.T) (*flakyChainProvider, *stubProvider, media.SearchResult) {
	t.Helper()
	fb := &flakyChainProvider{scanCountingChainProvider: multiSeasonBatchHarness(t), listingFailures: map[string]int{}}
	withFallbackChain(t, fb)
	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}, {ID: "s3", Number: 3}},
		episodesErr: errProviderCannotList,
	}
	return fb, primary, media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}
}

var wholeChainShow = []string{
	"fallback-1:1:1", "fallback-1:1:2", "fallback-1:1:3",
	"fallback-1:2:1",
	"fallback-1:3:1", "fallback-1:3:2",
}

func TestMultiSeasonRetryListsAFailedSeasonAgain(t *testing.T) {
	hostileEnv(t)
	fb, primary, sel := flakyBatch(t)
	fb.listingFailures["f2"] = 1
	confirmRetries(t, 1)

	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	if got := fb.episodesAsked(); !reflect.DeepEqual(got, wholeChainShow) {
		t.Fatalf("the chain was asked to stream %v, want %v: a season that failed to list is listed again on retry, never retried as an episode 0", got, wholeChainShow)
	}
}

func TestMultiSeasonWithoutRetryLeavesAFailedSeasonAlone(t *testing.T) {
	hostileEnv(t)
	fb, primary, sel := flakyBatch(t)
	fb.listingFailures["f2"] = 1

	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	want := []string{"fallback-1:1:1", "fallback-1:1:2", "fallback-1:1:3", "fallback-1:3:1", "fallback-1:3:2"}
	if got := fb.episodesAsked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the chain was asked to stream %v, want %v", got, want)
	}
}

func TestMultiSeasonRetryKeepsASeasonThatStillCannotBeListed(t *testing.T) {
	hostileEnv(t)
	fb, primary, sel := flakyBatch(t)
	fb.listingFailures["f2"] = 2
	confirmRetries(t, 2)

	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	if got := fb.episodesAsked(); !reflect.DeepEqual(got, wholeChainShow) {
		t.Fatalf("the chain was asked to stream %v, want %v: a season that fails to list on one retry is offered again on the next", got, wholeChainShow)
	}
}

func TestMultiSeasonRetryScansTheChainAgainOncePerRound(t *testing.T) {
	hostileEnv(t)
	fb, primary, sel := flakyBatch(t)
	fb.searchFailures = 1
	confirmRetries(t, 1)

	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	if got := fb.episodesAsked(); !reflect.DeepEqual(got, wholeChainShow) {
		t.Fatalf("the chain was asked to stream %v, want %v: a retry asks the chain again after a scan that found nothing", got, wholeChainShow)
	}
	if got := fb.scanCount(); got != 1 {
		t.Fatalf("the chain's seasons were enumerated %d times, want 1: the first scan failed at search and the retry round scans once for all three seasons", got)
	}
}

func TestMultiSeasonSummaryNamesFailedEpisodesAndUnlistedSeasons(t *testing.T) {
	e := func(season, n int) failedEpisode {
		return failedEpisode{SeasonNum: season, Episode: media.Episode{Number: n}}
	}
	cases := []struct {
		name string
		run  multiSeasonRun
		want string
	}{
		{"everything downloaded", multiSeasonRun{total: 6, downloaded: 6},
			"\nDownloaded 6/6 episodes across all seasons.\n"},
		{"nothing to download", multiSeasonRun{},
			"\nDownloaded 0/0 episodes across all seasons.\n"},
		{"failed episodes", multiSeasonRun{total: 6, downloaded: 4, failed: []failedEpisode{e(1, 2), e(3, 1)}},
			"\nDownloaded 4/6 episodes. Failed: S01E02, S03E01\n"},
		{"unlisted seasons only", multiSeasonRun{total: 5, downloaded: 5, unlisted: seasonsOf(2, 4)},
			"\nDownloaded 5/5 episodes. Episode list not fetched: S02, S04\n"},
		{"both", multiSeasonRun{total: 5, downloaded: 4, failed: []failedEpisode{e(1, 2)}, unlisted: seasonsOf(2)},
			"\nDownloaded 4/5 episodes. Failed: S01E02. Episode list not fetched: S02\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.run.summary(); got != c.want {
				t.Fatalf("summary = %q, want %q", got, c.want)
			}
		})
	}
}
