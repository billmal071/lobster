package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEZTVGapErrorsAreCatalogueVerdicts(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{5, 1}, [2]int{5, 3}, [2]int{7, 1}))
	if _, err := p.GetEpisodes("eztv/1", "5"); !errors.Is(err, ErrNotCarried) {
		t.Errorf("GetEpisodes gap err = %v, want it to wrap ErrNotCarried", err)
	}
	if _, err := p.GetSeasons("eztv/1"); !errors.Is(err, ErrNotCarried) {
		t.Errorf("GetSeasons gap err = %v, want it to wrap ErrNotCarried", err)
	}
}

func TestEZTVPartialFetchErrorIsNotACatalogueVerdict(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	_, err := NewEZTV().GetEpisodes("eztv/1", "1")
	if !errors.Is(err, ErrIncompleteEpisodeList) || errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want incomplete without ErrNotCarried: page 2 never loaded", err)
	}
}

func TestEZTVCarriesEpisodeMakesNoClaimOnAPartiallyFetchedCatalogue(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	e := NewEZTV()
	err := e.CarriesEpisode("eztv/1", 1, 120)
	if err == nil || errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want a lookup error: page 2 failed, so S01E120 may be on it", err)
	}
	if _, err := e.GetServers("eztv/1", "1x120"); errors.Is(err, ErrNotCarried) {
		t.Errorf("GetServers err = %v, must not claim ErrNotCarried on a partial catalogue", err)
	}
}

func TestEZTVCarriesEpisodeDoesNotRewalkAfterAPartialFetch(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	e := NewEZTV()
	_, _ = e.GetServers("eztv/1", "1x120")
	before := atomic.LoadInt32(&hits)
	_ = e.CarriesEpisode("eztv/1", 1, 120)
	if got := atomic.LoadInt32(&hits); got != before {
		t.Fatalf("API calls %d -> %d, want CarriesEpisode to reuse what the failed walk learned", before, got)
	}
}

func TestEZTVCarriesEpisodeMakesNoClaimOnACatalogueCutAtThePageLimit(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 0, eztvMaxPages+5)
	err := NewEZTV().CarriesEpisode("eztv/1", 2, 1)
	if err == nil || errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want a lookup error: the walk was cut at the page cap", err)
	}
}

func TestEZTVReleaseWithoutAMagnetIsUnknownNotAbsent(t *testing.T) {
	body := `{"torrents_count":1,"limit":100,"page":1,"torrents":[{"id":1,"filename":"S.S01E01.mkv","magnet_url":"","season":"1","episode":"1","seeds":5}]}`
	err := eztvServeBody(t, body).CarriesEpisode("eztv/1", 1, 1)
	if err == nil || errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want a lookup error: EZTV lists the release, it just has no magnet", err)
	}
}

func TestEZTVDateNumberedSeasonIsNotGapChecked(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{2025, 107}, [2]int{2025, 108}))
	if _, err := p.GetEpisodes("eztv/1", "2025"); err != nil {
		t.Fatalf("err = %v, want nil: season 2025 numbers its episodes by date, not from 1", err)
	}
}

const eztvFiveShowsTMDB = `{"results":[
{"id":11,"name":"A","media_type":"tv"},{"id":12,"name":"B","media_type":"tv"},
{"id":13,"name":"C","media_type":"tv"},{"id":14,"name":"D","media_type":"tv"},
{"id":15,"name":"E","media_type":"tv"}]}`

// eztvFiveShowServer serves five TV hits with imdb ids 1000011..1000015, each holding one release, and counts API calls per imdb id.
func eztvFiveShowServer(t *testing.T, delay time.Duration, hang bool, inflight, peak *int32) (*EZTV, *sync.Map) {
	t.Helper()
	perID := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/multi":
			fmt.Fprint(w, eztvFiveShowsTMDB)
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			fmt.Fprintf(w, `imdb.com/title/tt10000%s`, strings.TrimPrefix(r.URL.Path, "/tv/"))
		case r.URL.Path == "/api/get-torrents":
			id := r.URL.Query().Get("imdb_id")
			c, _ := perID.LoadOrStore(id, new(int32))
			atomic.AddInt32(c.(*int32), 1)
			n := atomic.AddInt32(inflight, 1)
			for {
				old := atomic.LoadInt32(peak)
				if n <= old || atomic.CompareAndSwapInt32(peak, old, n) {
					break
				}
			}
			defer atomic.AddInt32(inflight, -1)
			if hang {
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
				}
				return
			}
			time.Sleep(delay)
			fmt.Fprint(w, eztvEpisodesBody([2]int{1, 1}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
	p := NewEZTV().AsPrimary()
	p.tmdbBaseURL = srv.URL
	return p, perID
}

func eztvHitsFor(perID *sync.Map, id string) int32 {
	c, ok := perID.Load(id)
	if !ok {
		return 0
	}
	return atomic.LoadInt32(c.(*int32))
}

func TestEZTVProbeRunsAtMostThreeAtATime(t *testing.T) {
	var inflight, peak int32
	p, _ := eztvFiveShowServer(t, 60*time.Millisecond, false, &inflight, &peak)
	if ids := eztvResultIDs(t, p); len(ids) != 5 {
		t.Fatalf("ids = %v, want all 5", ids)
	}
	if got := atomic.LoadInt32(&peak); got > 3 || got < 2 {
		t.Fatalf("peak concurrent probes = %d, want 2 or 3", got)
	}
}

func TestEZTVProbeCacheExpires(t *testing.T) {
	var inflight, peak int32
	p, perID := eztvFiveShowServer(t, 0, false, &inflight, &peak)
	now := time.Now()
	p.now = func() time.Time { return now }
	eztvResultIDs(t, p)
	eztvResultIDs(t, p)
	if got := eztvHitsFor(perID, "1000011"); got != 1 {
		t.Fatalf("probes for a fresh entry = %d, want 1", got)
	}
	now = now.Add(eztvCacheTTL + time.Second)
	eztvResultIDs(t, p)
	if got := eztvHitsFor(perID, "1000011"); got != 2 {
		t.Fatalf("probes after the TTL = %d, want 2: the cache must expire", got)
	}
}

func TestEZTVProbeUsesAnAlreadyFetchedCatalogue(t *testing.T) {
	var inflight, peak int32
	p, perID := eztvFiveShowServer(t, 0, false, &inflight, &peak)
	if _, err := p.GetSeasons("eztv/1000012"); err != nil {
		t.Fatal(err)
	}
	before := eztvHitsFor(perID, "1000012")
	eztvResultIDs(t, p)
	if got := eztvHitsFor(perID, "1000012"); got != before {
		t.Fatalf("API calls for a show whose catalogue is cached went %d -> %d, want no probe", before, got)
	}
}

func TestEZTVProbeTimesOutAndFailsOpen(t *testing.T) {
	old := eztvProbeTimeout
	eztvProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { eztvProbeTimeout = old })
	var inflight, peak int32
	p, _ := eztvFiveShowServer(t, 0, true, &inflight, &peak)
	start := time.Now()
	ids := eztvResultIDs(t, p)
	if len(ids) != 5 {
		t.Fatalf("ids = %v, want all 5 kept when every probe times out", ids)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("search took %s, want it bounded by the probe timeout", took)
	}
}
