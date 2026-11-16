package provider

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestEZTVYearNumberedSeasonsAreNotGapChecked(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{2024, 1}, [2]int{2025, 1}))
	if _, err := p.GetSeasons("eztv/1"); err != nil {
		t.Fatalf("err = %v, want nil: seasons named by year are not a count from 1", err)
	}
}

func TestEZTVPartialWalkMemoExpires(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	e := NewEZTV()
	now := time.Now()
	e.now = func() time.Time { return now }
	_, _ = e.GetServers("eztv/1", "1x120")
	_ = e.CarriesEpisode("eztv/1", 1, 120)
	fresh := atomic.LoadInt32(&hits)
	now = now.Add(eztvCacheTTL + time.Second)
	_ = e.CarriesEpisode("eztv/1", 1, 120)
	if got := atomic.LoadInt32(&hits); got == fresh {
		t.Fatal("CarriesEpisode kept refusing to look after the memo of a failed walk should have expired")
	}
}

func TestIsCatalogueGap(t *testing.T) {
	gap := eztvGapError{"x"}
	if !IsCatalogueGap(gap) || !IsCatalogueGap(fmt.Errorf("wrapped: %w", gap)) {
		t.Error("a gap error, wrapped or not, must be a catalogue gap")
	}
	if IsCatalogueGap(fmt.Errorf("%w: probe 429", ErrIncompleteEpisodeList)) {
		t.Error("an incomplete list with no catalogue verdict is a prefix, not a gap")
	}
	if IsCatalogueGap(nil) {
		t.Error("nil is not a gap")
	}
}
