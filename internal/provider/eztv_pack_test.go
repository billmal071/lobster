package provider

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEZTVNegativeProbeYieldsToReleasesALaterWalkFetched(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	e := NewEZTV().AsPrimary()
	e.probes["1"] = eztvProbe{has: false, at: e.now()}

	if _, err := e.GetServers("eztv/1", "1x1"); err != nil {
		t.Fatalf("GetServers: %v, want the release page 1 listed before page 2 failed", err)
	}
	before := atomic.LoadInt32(&hits)
	if !e.hasTorrents("1") {
		t.Fatal("search would hide a show whose releases this instance fetched after the empty probe")
	}
	if got := atomic.LoadInt32(&hits); got != before {
		t.Errorf("API calls %d -> %d, want the fetched prefix to answer without a new probe", before, got)
	}
}

func TestEZTVNegativeProbeOutlivesAnExpiredPartialWalk(t *testing.T) {
	e := NewEZTV().AsPrimary()
	now := time.Now()
	e.now = func() time.Time { return now }
	e.partial["1"] = eztvPartial{
		torrents: []eztvTorrent{{Season: 1, Episode: 1, MagnetURL: "magnet:?xt=urn:btih:x"}},
		at:       now.Add(-eztvCacheTTL - time.Second),
	}
	e.probes["1"] = eztvProbe{has: false, at: now}

	if e.hasTorrents("1") {
		t.Fatal("a prefix older than the cache TTL overrode a fresh empty probe")
	}
}

func TestEZTVGetSeasonsCountsASeasonPackAsCarried(t *testing.T) {
	cases := []struct {
		name        string
		releases    [][2]int
		wantSeasons []int
		wantMissing string
	}{
		{"a season held only as a pack is not reported missing", [][2]int{{1, 0}, {2, 1}}, []int{2}, ""},
		{"a season with neither a pack nor a release still is", [][2]int{{1, 0}, {3, 1}}, []int{3}, "season 2 is not on EZTV"},
		{"a pack above the highest listed season bounds the check", [][2]int{{2, 1}, {5, 0}}, []int{2}, "seasons 1, 3-4 are not on EZTV"},
		{"packs alone list nothing and report nothing", [][2]int{{2, 0}}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := eztvServeBody(t, eztvEpisodesBody(c.releases...))
			seasons, err := p.GetSeasons("eztv/1")
			var got []int
			for _, s := range seasons {
				got = append(got, s.Number)
			}
			if len(got) != len(c.wantSeasons) {
				t.Fatalf("seasons = %v, want %v: a pack is never navigable", got, c.wantSeasons)
			}
			for i := range got {
				if got[i] != c.wantSeasons[i] {
					t.Fatalf("seasons = %v, want %v", got, c.wantSeasons)
				}
			}
			if c.wantMissing == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !IsCatalogueGap(err) || !strings.HasSuffix(err.Error(), "; "+c.wantMissing) {
				t.Fatalf("err = %v, want a catalogue gap ending %q", err, c.wantMissing)
			}
		})
	}
}

func TestEZTVGetEpisodesDoesNotCallAnEpisodeAbsentBesideASeasonPack(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{1, 0}, [2]int{1, 1}, [2]int{1, 3}))
	eps, err := p.GetEpisodes("eztv/1", "1")
	if len(eps) != 2 || !IsCatalogueGap(err) {
		t.Fatalf("episodes = %d, err = %v; want episodes 1 and 3 with a catalogue gap", len(eps), err)
	}
	if strings.Contains(err.Error(), "not on EZTV") || !strings.Contains(err.Error(), "episode 2 has no standalone release on EZTV") {
		t.Errorf("err = %q, want it to claim only that episode 2 has no standalone release: the season pack may hold it", err)
	}
}

func TestEZTVGetEpisodesSeasonPackOfAnotherSeasonDoesNotSoftenTheGap(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{2, 0}, [2]int{1, 1}, [2]int{1, 3}, [2]int{1, 5}))
	_, err := p.GetEpisodes("eztv/1", "1")
	if !IsCatalogueGap(err) || !strings.HasSuffix(err.Error(), "; episodes 2, 4 are not on EZTV") {
		t.Fatalf("err = %v, want episodes 2 and 4 reported as not on EZTV", err)
	}
}
