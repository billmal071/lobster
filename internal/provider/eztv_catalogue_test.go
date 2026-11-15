package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func eztvEpisodesBody(eps ...[2]int) string {
	var parts []string
	for i, se := range eps {
		parts = append(parts, fmt.Sprintf(
			`{"id":%d,"filename":"Show.S%02dE%02d.720p.HDTV.x264[eztv].mkv","magnet_url":"magnet:?xt=urn:btih:h%d","title":"Show S%02dE%02d 720p HDTV","season":"%d","episode":"%d","seeds":5,"size_bytes":1}`,
			i+1, se[0], se[1], i, se[0], se[1], se[0], se[1]))
	}
	return fmt.Sprintf(`{"torrents_count":%d,"limit":100,"page":1,"torrents":[%s]}`, len(eps), strings.Join(parts, ","))
}

func eztvServeBody(t *testing.T, body string) *EZTV {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
	return NewEZTV()
}

func TestEZTVGetSeasonsFlagsMissingSeasonsBetweenListed(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{5, 1}, [2]int{6, 1}, [2]int{9, 1}, [2]int{11, 1}))
	seasons, err := p.GetSeasons("eztv/121955")
	if len(seasons) != 4 {
		t.Fatalf("seasons = %+v, want the 4 that exist", seasons)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("err = %v, want ErrIncompleteEpisodeList for a catalogue with seasons missing", err)
	}
	if !strings.Contains(err.Error(), "1-4") || !strings.Contains(err.Error(), "7-8") || !strings.Contains(err.Error(), "10") {
		t.Errorf("err = %q, want it to name the missing seasons 1-4, 7-8 and 10", err)
	}
}

func TestEZTVGetSeasonsSingleLaterSeasonIsFlagged(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{9, 1}, [2]int{9, 2}))
	seasons, err := p.GetSeasons("eztv/386676")
	if len(seasons) != 1 || seasons[0].Number != 9 {
		t.Fatalf("seasons = %+v, want only season 9", seasons)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) || !strings.Contains(err.Error(), "1-8") {
		t.Fatalf("err = %v, want ErrIncompleteEpisodeList naming seasons 1-8", err)
	}
}

func TestEZTVGetSeasonsContiguousFromOneIsNotFlagged(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{1, 1}, [2]int{2, 1}, [2]int{3, 1}))
	if _, err := p.GetSeasons("eztv/1"); err != nil {
		t.Fatalf("err = %v, want nil for seasons 1-3", err)
	}
}

func TestEZTVGetEpisodesFlagsMissingEpisode(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{5, 1}, [2]int{5, 2}, [2]int{5, 3}, [2]int{5, 5}, [2]int{5, 6}))
	eps, err := p.GetEpisodes("eztv/121955", "5")
	if len(eps) != 5 {
		t.Fatalf("eps = %+v, want the 5 that exist", eps)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) || !strings.Contains(err.Error(), "episode 4") {
		t.Fatalf("err = %v, want ErrIncompleteEpisodeList naming episode 4", err)
	}
}

func TestEZTVGetEpisodesSingleLaterEpisodeIsFlagged(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{2, 10}))
	eps, err := p.GetEpisodes("eztv/1", "2")
	if len(eps) != 1 {
		t.Fatalf("eps = %+v, want the one that exists", eps)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) || !strings.Contains(err.Error(), "episodes 1-9") {
		t.Fatalf("err = %v, want ErrIncompleteEpisodeList naming episodes 1-9", err)
	}
}

func TestEZTVGetEpisodesContiguousIsNotFlagged(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{1, 1}, [2]int{1, 2}, [2]int{1, 3}))
	if _, err := p.GetEpisodes("eztv/1", "1"); err != nil {
		t.Fatalf("err = %v, want nil for episodes 1-3", err)
	}
}

func TestEZTVCarriesEpisode(t *testing.T) {
	p := eztvServeBody(t, eztvEpisodesBody([2]int{5, 1}, [2]int{5, 2}))
	if err := p.CarriesEpisode("eztv/121955", 5, 2); err != nil {
		t.Fatalf("present episode: err = %v, want nil", err)
	}
	err := p.CarriesEpisode("eztv/121955", 5, 4)
	if !errors.Is(err, ErrNotCarried) {
		t.Fatalf("absent episode: err = %v, want ErrNotCarried", err)
	}
}

func TestEZTVCarriesEpisodeShowWithNoTorrents(t *testing.T) {
	p := eztvServeBody(t, `{"imdb_id":"0","torrents_count":0,"limit":100,"page":1}`)
	if err := p.CarriesEpisode("eztv/756509", 1, 1); !errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want ErrNotCarried", err)
	}
}

func TestEZTVCarriesEpisodeAPIErrorIsNotACatalogueVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
	err := NewEZTV().CarriesEpisode("eztv/1", 1, 1)
	if err == nil || errors.Is(err, ErrNotCarried) {
		t.Fatalf("err = %v, want a non-nil error that is not ErrNotCarried: a rate limit says nothing about the catalogue", err)
	}
}

const eztvProbeTMDB = `{"results":[
{"id":1,"name":"Kyle XY","media_type":"tv","first_air_date":"2006-06-26"},
{"id":2,"name":"South Park","media_type":"tv","first_air_date":"1997-08-13"},
{"id":3,"name":"The Office","media_type":"tv","first_air_date":"2005-03-24"}
]}`

type eztvProbeStub struct {
	p       *EZTV
	apiHits *int32
}

// eztvProbeServer serves three TV hits: imdb 0756509 has no torrents, 0121955 has two, 0386676 has one. A non-zero status answers every API call with it.
func eztvProbeServer(t *testing.T, status int, delay time.Duration, inflight, peak *int32) eztvProbeStub {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/multi":
			fmt.Fprint(w, eztvProbeTMDB)
		case r.URL.Path == "/tv/1":
			fmt.Fprint(w, `imdb.com/title/tt0756509`)
		case r.URL.Path == "/tv/2":
			fmt.Fprint(w, `imdb.com/title/tt0121955`)
		case r.URL.Path == "/tv/3":
			fmt.Fprint(w, `imdb.com/title/tt0386676`)
		case r.URL.Path == "/api/get-torrents":
			atomic.AddInt32(&hits, 1)
			if inflight != nil {
				n := atomic.AddInt32(inflight, 1)
				for {
					old := atomic.LoadInt32(peak)
					if n <= old || atomic.CompareAndSwapInt32(peak, old, n) {
						break
					}
				}
				defer atomic.AddInt32(inflight, -1)
			}
			time.Sleep(delay)
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			switch r.URL.Query().Get("imdb_id") {
			case "0756509":
				fmt.Fprint(w, `{"imdb_id":"0","torrents_count":0,"limit":100,"page":1}`)
			case "0121955":
				fmt.Fprint(w, eztvEpisodesBody([2]int{5, 1}, [2]int{5, 2}))
			default:
				fmt.Fprint(w, eztvEpisodesBody([2]int{9, 1}))
			}
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
	return eztvProbeStub{p: p, apiHits: &hits}
}

func eztvResultIDs(t *testing.T, p *EZTV) []string {
	t.Helper()
	res, err := p.Search("show")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range res {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestEZTVPrimarySearchDropsShowsWithNoTorrents(t *testing.T) {
	s := eztvProbeServer(t, 0, 0, nil, nil)
	ids := eztvResultIDs(t, s.p)
	want := []string{"eztv/0121955", "eztv/0386676"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v: Kyle XY has no torrents and must not be offered", ids, want)
	}
}

func TestEZTVNonPrimarySearchKeepsEveryShow(t *testing.T) {
	s := eztvProbeServer(t, 0, 0, nil, nil)
	p := NewEZTV()
	p.tmdbBaseURL = s.p.tmdbBaseURL
	if ids := eztvResultIDs(t, p); len(ids) != 3 {
		t.Fatalf("ids = %v, want all 3: only the primary probes", ids)
	}
	if n := atomic.LoadInt32(s.apiHits); n != 0 {
		t.Errorf("a non-primary search made %d API calls, want 0", n)
	}
}

func TestEZTVPrimarySearchKeepsAShowWithOneTorrent(t *testing.T) {
	s := eztvProbeServer(t, 0, 0, nil, nil)
	ids := eztvResultIDs(t, s.p)
	found := false
	for _, id := range ids {
		found = found || id == "eztv/0386676"
	}
	if !found {
		t.Fatalf("ids = %v, want the one-torrent show kept", ids)
	}
}

func TestEZTVPrimarySearchFailsOpenOnAPIError(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		s := eztvProbeServer(t, status, 0, nil, nil)
		if ids := eztvResultIDs(t, s.p); len(ids) != 3 {
			t.Fatalf("status %d: ids = %v, want all 3 when the probe errors", status, ids)
		}
	}
}

func TestEZTVPrimarySearchAllDroppedIsNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/multi":
			fmt.Fprint(w, `{"results":[{"id":1,"name":"Kyle XY","media_type":"tv"}]}`)
		case r.URL.Path == "/tv/1":
			fmt.Fprint(w, `imdb.com/title/tt0756509`)
		default:
			fmt.Fprint(w, `{"torrents_count":0,"limit":100,"page":1}`)
		}
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
	p := NewEZTV().AsPrimary()
	p.tmdbBaseURL = srv.URL
	_, err := p.Search("kyle xy")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults", err)
	}
}

func TestEZTVPrimarySearchCachesTheProbe(t *testing.T) {
	s := eztvProbeServer(t, 0, 0, nil, nil)
	eztvResultIDs(t, s.p)
	first := atomic.LoadInt32(s.apiHits)
	eztvResultIDs(t, s.p)
	if got := atomic.LoadInt32(s.apiHits); got != first {
		t.Fatalf("API calls %d -> %d, want the second search served from the probe cache", first, got)
	}
}

func TestEZTVPrimarySearchDoesNotCacheAProbeError(t *testing.T) {
	s := eztvProbeServer(t, http.StatusTooManyRequests, 0, nil, nil)
	eztvResultIDs(t, s.p)
	first := atomic.LoadInt32(s.apiHits)
	eztvResultIDs(t, s.p)
	if got := atomic.LoadInt32(s.apiHits); got == first {
		t.Fatal("a failed probe was cached; a rate limit must be retried on the next search")
	}
}

func TestEZTVPrimarySearchBoundsProbeConcurrency(t *testing.T) {
	var inflight, peak int32
	s := eztvProbeServer(t, 0, 40*time.Millisecond, &inflight, &peak)
	eztvResultIDs(t, s.p)
	if got := atomic.LoadInt32(&peak); got > eztvProbeConcurrency {
		t.Fatalf("peak concurrent probes = %d, want at most %d", got, eztvProbeConcurrency)
	}
}
