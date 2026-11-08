package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/media"
)

// eztvFixture is a trimmed, real get-torrents payload for Severance
// (imdb 11280740): two S02 season packs (episode 0, empty filename) and
// S02E10 at several qualities. seeds/size_bytes arrive as numbers, season
// and episode as strings, exactly as the live API serves them.
const eztvFixture = `{"imdb_id":"11280740","torrents_count":10,"limit":100,"page":1,"torrents":[
{"id":1,"hash":"a1","filename":"","magnet_url":"magnet:?xt=urn:btih:a1&dn=Severance.S02&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02 1080p x265-ELiTE EZTV","imdb_id":"11280740","season":"2","episode":"0","seeds":95,"peers":16,"date_released_unix":1742500000,"size_bytes":6871947674},
{"id":2,"hash":"b2","filename":"Severance S02E10 Cold Harbor 1080p WebRip EAC3 5 1 x265-Lootera[EZTVx.to].mkv","magnet_url":"magnet:?xt=urn:btih:b2&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor 1080p WebRip EAC3 5 1 x265-Lootera EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":56,"peers":5,"date_released_unix":1742500001,"size_bytes":1677246584},
{"id":3,"hash":"c3","filename":"Severance.S02E10.Cold.Harbor.480p.x264-mSD[EZTVx.to].mkv","magnet_url":"magnet:?xt=urn:btih:c3&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor 480p x264-mSD EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":7,"peers":4,"date_released_unix":1742500002,"size_bytes":620447482},
{"id":4,"hash":"d4","filename":"Severance.S02E10.Cold.Harbor.720p.HEVC.x265-MeGusta[EZTVx.to].mkv","magnet_url":"magnet:?xt=urn:btih:d4&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor 720p HEVC x265-MeGusta EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":17,"peers":3,"date_released_unix":1742500003,"size_bytes":734527944},
{"id":5,"hash":"e5","filename":"Severance S02E10 Cold Harbor 720p ATVP WEB-DL DDP5 1 Atmos H 264-FLUX[EZTVx.to].mkv","magnet_url":"magnet:?xt=urn:btih:e5&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor 720p ATVP WEB-DL DDP5 1 Atmos H 264-FLUX EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":31,"peers":2,"date_released_unix":1742500004,"size_bytes":2051077409},
{"id":6,"hash":"f6","filename":"Severance.S02E10.Cold.Harbor.1080p.HEVC.x265-MeGusta[EZTVx.to].mkv","magnet_url":"magnet:?xt=urn:btih:f6&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor 1080p HEVC x265-MeGusta EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":118,"peers":9,"date_released_unix":1742500005,"size_bytes":793588054},
{"id":7,"hash":"a7","filename":"Severance.S02E10.Cold.Harbor.XviD-AFG[EZTVx.to].avi","magnet_url":"magnet:?xt=urn:btih:a7&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S02E10 Cold Harbor XviD-AFG EZTV","imdb_id":"11280740","season":"2","episode":"10","seeds":1,"peers":0,"date_released_unix":1742500006,"size_bytes":624853112},
{"id":8,"hash":"b8","filename":"Severance.S01E01.Good.News.About.Hell.720p.HDTV.x264-SYNCOPY[eztv].mkv","magnet_url":"magnet:?xt=urn:btih:b8&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S01E01 Good News About Hell 720p HDTV x264-SYNCOPY EZTV","imdb_id":"11280740","season":"1","episode":"1","seeds":3,"peers":1,"date_released_unix":1645000000,"size_bytes":900000000},
{"id":10,"hash":"d10","filename":"Severance.S01E02.720p.HDTV.x264-OTHER[eztv].mkv","magnet_url":"magnet:?xt=urn:btih:d10&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S01E02 720p HDTV x264-OTHER EZTV","imdb_id":"11280740","season":"1","episode":"2","seeds":2,"peers":0,"date_released_unix":1645000002,"size_bytes":700000000},
{"id":9,"hash":"c9","filename":"Severance.S01E02.HDTV.x264-GROUP[eztv].mkv","magnet_url":"magnet:?xt=urn:btih:c9&dn=x&tr=udp://tracker.opentrackr.org:1337/announce","title":"Severance S01E02 HDTV x264-GROUP EZTV","imdb_id":"11280740","season":"1","episode":"2","seeds":0,"peers":0,"date_released_unix":1645000001,"size_bytes":400000000}
]}`

// eztvTMDBFixture is a trimmed TMDB keyless multi-search answer: a TV hit,
// a movie (ignored), a person (ignored), a second TV hit with no IMDb link,
// and an autocomplete string the parser must skip.
const eztvTMDBFixture = `{"results":[
{"id":95396,"name":"Severance","media_type":"tv","first_air_date":"2022-02-17","poster_path":"/sev.jpg"},
{"id":12345,"title":"Severance","media_type":"movie","release_date":"2006-01-01"},
{"id":777,"name":"Some Actor","media_type":"person"},
{"id":4242,"name":"Severance Package","media_type":"tv","first_air_date":"2019-01-01"},
"<span>severance pay</span>"
]}`

// eztvStub serves TMDB search, TMDB show pages and the EZTV API from one
// server and counts EZTV hits so caching can be asserted.
func eztvStub(t *testing.T, apiHits *int32) *EZTV {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/multi":
			fmt.Fprint(w, eztvTMDBFixture)
		case r.URL.Path == "/tv/95396":
			fmt.Fprint(w, `<html><a href="https://www.imdb.com/title/tt11280740">IMDb</a></html>`)
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			fmt.Fprint(w, `<html>no external link</html>`)
		case r.URL.Path == "/api/get-torrents":
			if apiHits != nil {
				atomic.AddInt32(apiHits, 1)
			}
			if r.URL.Query().Get("imdb_id") != "11280740" {
				fmt.Fprint(w, `{"imdb_id":"0","torrents_count":0,"limit":100,"page":1}`)
				return
			}
			fmt.Fprint(w, eztvFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
	p := NewEZTV()
	p.tmdbBaseURL = srv.URL
	return p
}

func TestEZTVSearchMapsTMDBHitsToIMDB(t *testing.T) {
	p := eztvStub(t, nil)
	res, err := p.Search("severance")
	if err != nil {
		t.Fatal(err)
	}
	// The movie, the person, the suggestion string and the TV hit with no
	// IMDb link are all dropped: EZTV can only be asked by IMDb id.
	if len(res) != 1 {
		t.Fatalf("got %d results: %+v", len(res), res)
	}
	want := media.SearchResult{ID: "eztv/11280740", Title: "Severance", Year: "2022", Type: media.TV, Poster: "https://image.tmdb.org/t/p/w500/sev.jpg"}
	if res[0] != want {
		t.Errorf("got %+v, want %+v", res[0], want)
	}
}

func TestEZTVSearchNoIMDBIsNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/multi" {
			fmt.Fprint(w, `{"results":[{"id":1,"name":"X","media_type":"tv"}]}`)
			return
		}
		fmt.Fprint(w, `<html></html>`)
	}))
	defer srv.Close()
	p := NewEZTV()
	p.tmdbBaseURL = srv.URL
	_, err := p.Search("x")
	if err == nil || !strings.Contains(err.Error(), ErrNoResults.Error()) {
		t.Fatalf("want ErrNoResults, got %v", err)
	}
}

func TestEZTVSeasonsAndEpisodes(t *testing.T) {
	p := eztvStub(t, nil)
	seasons, err := p.GetSeasons("eztv/11280740")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 2 || seasons[0].Number != 1 || seasons[1].Number != 2 || seasons[1].ID != "2" {
		t.Fatalf("bad seasons: %+v", seasons)
	}

	eps, err := p.GetEpisodes("eztv/11280740", "2")
	if err != nil {
		t.Fatal(err)
	}
	// Season packs (episode 0) are not episodes; six releases of E10 are one.
	if len(eps) != 1 {
		t.Fatalf("got %d episodes: %+v", len(eps), eps)
	}
	if eps[0].Number != 10 || eps[0].ID != "2x10" || eps[0].Title != "Cold Harbor" {
		t.Errorf("bad episode: %+v", eps[0])
	}

	eps, err = p.GetEpisodes("eztv/11280740", "1")
	if err != nil {
		t.Fatal(err)
	}
	// Two nameless releases of E02 collapse to one "Episode 2" entry.
	if len(eps) != 2 || eps[0].Title != "Good News About Hell" || eps[1].Title != "Episode 2" {
		t.Errorf("bad season 1 episodes: %+v", eps)
	}
}

func TestEZTVEpisodeTitle(t *testing.T) {
	cases := map[string]string{
		"Severance S02E10 Cold Harbor 1080p WebRip EAC3 5 1 x265-Lootera EZTV": "Cold Harbor",
		"Severance.S01E01.Good.News.About.Hell.720p.HDTV.x264-SYNCOPY":         "Good News About Hell",
		"Severance S01E02 HDTV x264-GROUP EZTV":                                "",
		"Severance S02 1080p x265-ELiTE EZTV":                                  "",
		"Show s03e04 The Web Of Lies 720p":                                     "The Web Of Lies",
	}
	for in, want := range cases {
		if got := eztvEpisodeTitle(in); got != want {
			t.Errorf("eztvEpisodeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEZTVQualityHeight(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"Show.S01E01.2160p.WEB.H265-GRP.mkv", 2160},
		{"Show.S01E01.4K.WEB-GRP.mkv", 2160},
		{"Show S01E01 1080p WEB-DL EZTV", 1080},
		{"Show.S01E01.720p.HDTV.x264-GRP.mkv", 720},
		{"Show.S01E01.480p.x264-mSD.mkv", 480},
		{"Show.S01E01.HDTV.x264-GRP.mkv", 480},
		{"Show.S01E01.XviD-AFG.avi", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := eztvQualityHeight(c.in); got != c.want {
			t.Errorf("eztvQualityHeight(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	if got := eztvQualityLabel("Show.S01E01.XviD-AFG.avi"); got != "SD" {
		t.Errorf("label for no quality = %q, want SD", got)
	}
}

func TestEZTVGetServersOrderAndNames(t *testing.T) {
	p := eztvStub(t, nil)
	servers, err := p.GetServers("eztv/11280740", "2x10")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 6 {
		t.Fatalf("got %d servers: %+v", len(servers), servers)
	}
	// Quality descending, then seeds descending within a quality; the
	// "<height>p " prefix is what cmd's preferQualityServers keys on.
	wantPrefix := []string{"1080p x265-MeGusta (", "1080p x265-Lootera (", "720p H264-FLUX (", "720p x265-MeGusta (", "480p x264-mSD (", "SD XviD-AFG ("}
	for i, w := range wantPrefix {
		if !strings.HasPrefix(servers[i].Name, w) {
			t.Errorf("server %d = %q, want prefix %q", i, servers[i].Name, w)
		}
	}
	if servers[0].Name != "1080p x265-MeGusta (756 MB, 118 seeds)" {
		t.Errorf("name = %q", servers[0].Name)
	}
	if !strings.Contains(servers[1].Name, "1.56 GB") {
		t.Errorf("size not in GB: %q", servers[1].Name)
	}
}

func TestEZTVUnseededDroppedOnlyWhenOthersExist(t *testing.T) {
	seeded := []eztvTorrent{{Filename: "a.720p.mkv", Seeds: 0}, {Filename: "b.480p.mkv", Seeds: 2}}
	if got := sortedEZTVTorrents(seeded); len(got) != 1 || got[0].Filename != "b.480p.mkv" {
		t.Errorf("unseeded not dropped: %+v", got)
	}
	none := []eztvTorrent{{Filename: "a.720p.mkv", Seeds: 0}, {Filename: "b.480p.mkv", Seeds: 0}}
	if got := sortedEZTVTorrents(none); len(got) != 2 || got[0].Filename != "a.720p.mkv" {
		t.Errorf("all-unseeded list should be kept and sorted: %+v", got)
	}
}

func TestEZTVPickTorrent(t *testing.T) {
	p := eztvStub(t, nil)
	torrents, err := p.episodeTorrents("eztv/11280740", "2x10")
	if err != nil {
		t.Fatal(err)
	}
	if got := pickEZTVTorrent(torrents, "", "best"); got.Hash != "f6" {
		t.Errorf("best = %s, want f6 (1080p, most seeds)", got.Hash)
	}
	if got := pickEZTVTorrent(torrents, "", "720"); got.Hash != "e5" {
		t.Errorf("720 = %s, want e5 (most-seeded 720p)", got.Hash)
	}
	if got := pickEZTVTorrent(torrents, "", "2160p"); got.Hash != "f6" {
		t.Errorf("unstocked quality should fall back to best, got %s", got.Hash)
	}
	// No 900p release: the closest quality below it (720p) beats the 1080p
	// one above it.
	if got := pickEZTVTorrent(torrents, "", "900"); got.Hash != "e5" {
		t.Errorf("900 = %s, want e5 (closest lower, most-seeded 720p)", got.Hash)
	}
	// Nothing at or below 300p: fall back to the best.
	if got := pickEZTVTorrent(torrents, "", "300"); got.Hash != "f6" {
		t.Errorf("300 = %s, want f6 (nothing below, best)", got.Hash)
	}
	// An explicit server name wins over the quality setting, by name or index.
	if got := pickEZTVTorrent(torrents, "480p x264-mSD (591 MB, 7 seeds)", "1080"); got.Hash != "c3" {
		t.Errorf("explicit name = %s, want c3", got.Hash)
	}
	if got := pickEZTVTorrent(torrents, "4", "1080"); got.Hash != "c3" {
		t.Errorf("explicit index = %s, want c3", got.Hash)
	}
}

func TestEZTVWatchReturnsMagnetAndCaches(t *testing.T) {
	var hits int32
	p := eztvStub(t, &hits)
	if _, err := p.GetSeasons("eztv/11280740"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetEpisodes("eztv/11280740", "2"); err != nil {
		t.Fatal(err)
	}
	st, err := p.Watch("eztv/11280740", "2x10", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:f6") || !strings.Contains(st.URL, "&tr=") {
		t.Errorf("bad magnet: %s", st.URL)
	}
	if st.Quality != "1080p" {
		t.Errorf("quality = %q", st.Quality)
	}
	// The fallback resolver's "showID:season:episode" form resolves too.
	if st2, err := p.Watch("eztv/11280740", "eztv/11280740:2:10", "", "480"); err != nil || !strings.HasPrefix(st2.URL, "magnet:?xt=urn:btih:c3") {
		t.Errorf("fallback episode ref: %v %+v", err, st2)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("EZTV fetched %d times across seasons/episodes/watch, want 1 (cached)", n)
	}
	if _, err := p.Watch("eztv/11280740", "3x1", "", ""); err == nil {
		t.Error("missing episode should fail, not play something else")
	}
	if _, err := p.Watch("eztv/9999999", "1x1", "", ""); err == nil {
		t.Error("unknown show should fail")
	}
	if _, err := p.Watch("eztv/../x", "1x1", "", ""); err == nil {
		t.Error("non-numeric id should be rejected")
	}
}

// Pagination walks until torrents_count is covered and stops on a short
// page, so a count that never reconciles cannot keep it going.
func TestEZTVPaginationStops(t *testing.T) {
	var pages int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		atomic.AddInt32(&pages, 1)
		var torrents []map[string]any
		n := eztvPageSize
		if page == "3" {
			n = 5 // short final page
		}
		if page == "4" {
			_ = json.NewEncoder(w).Encode(map[string]any{"torrents_count": 999, "page": 4})
			return
		}
		for i := 0; i < n; i++ {
			torrents = append(torrents, map[string]any{
				"id": i, "filename": fmt.Sprintf("S.S01E%02d.720p.mkv", i+1), "magnet_url": "magnet:?xt=urn:btih:x",
				"season": "1", "episode": fmt.Sprint(i + 1), "seeds": 1, "size_bytes": 1,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"torrents_count": 999, "page": page, "torrents": torrents})
	}))
	defer srv.Close()
	old := eztvBase
	eztvBase = srv.URL
	defer func() { eztvBase = old }()

	all, err := NewEZTV().torrents("eztv/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2*eztvPageSize+5 {
		t.Errorf("got %d torrents", len(all))
	}
	if atomic.LoadInt32(&pages) != 3 {
		t.Errorf("fetched %d pages, want 3 (stop on short page)", pages)
	}
}

// The resolver asks every StreamProvider for a movie with an empty episode
// ID. EZTV has no films: it must refuse before it fetches anything.
func TestEZTVWatchRefusesAMovieRequest(t *testing.T) {
	e := NewEZTV()
	if _, err := e.Watch("eztv/0903747", "", "Default", "1080"); err == nil {
		t.Fatal("Watch with no episode returned a stream; a movie request must not get S01E01")
	}
}

func TestEZTVEpisodeRef(t *testing.T) {
	for in, want := range map[string][2]int{"2x10": {2, 10}, "2:10": {2, 10}, "eztv/1:3:4": {3, 4}} {
		s, e, err := eztvEpisodeRef(in)
		if err != nil || s != want[0] || e != want[1] {
			t.Errorf("eztvEpisodeRef(%q) = %d,%d,%v want %v", in, s, e, err, want)
		}
	}
	// "" is what the resolver sends for a movie. EZTV is TV-only, and
	// defaulting to S01E01 answered a film request with a pilot episode.
	for _, bad := range []string{"", "x", "2x", "0x1", "ax1"} {
		if _, _, err := eztvEpisodeRef(bad); err == nil {
			t.Errorf("eztvEpisodeRef(%q) should fail", bad)
		}
	}
}

func eztvPagedServer(t *testing.T, hits *int32, countKey bool, failFrom int, fullPages int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		var page int
		_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)
		if failFrom > 0 && page >= failFrom {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		n := eztvPageSize
		if page > fullPages {
			n = 5
		}
		var torrents []map[string]any
		for i := 0; i < n; i++ {
			torrents = append(torrents, map[string]any{
				"id": i, "filename": "S.S01E01.720p.mkv", "magnet_url": "magnet:?xt=urn:btih:x",
				"season": "1", "episode": "1", "seeds": 1, "size_bytes": 1,
			})
		}
		body := map[string]any{"page": page, "torrents": torrents}
		if countKey {
			body["torrents_count"] = 999
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	old := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = old })
}

func TestEZTVPaginationContinuesWhenTorrentsCountIsMissing(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 0, 2)
	all, err := NewEZTV().torrents("eztv/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2*eztvPageSize+5 {
		t.Errorf("got %d torrents, want %d: a full page with no count must lead to the next page", len(all), 2*eztvPageSize+5)
	}
}

func TestEZTVDoesNotCacheACataloguePartiallyFetched(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	e := NewEZTV()
	if _, err := e.torrents("eztv/1"); err != nil {
		t.Fatal(err)
	}
	before := atomic.LoadInt32(&hits)
	if _, err := e.torrents("eztv/1"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&hits) == before {
		t.Error("a catalogue cut short by a failed page was cached and served again")
	}
}

func TestEZTVGetEpisodesFlagsACataloguePartiallyFetched(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 2, 5)
	eps, err := NewEZTV().GetEpisodes("eztv/1", "1")
	if len(eps) == 0 {
		t.Fatalf("eps empty, want the episodes of the pages that did load (err=%v)", err)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("err = %v, want ErrIncompleteEpisodeList: a requested episode may sit on the page that failed", err)
	}
}

func TestEZTVGetEpisodesFlagsACatalogueCutAtThePageLimit(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 0, eztvMaxPages+5)
	eps, err := NewEZTV().GetEpisodes("eztv/1", "1")
	if len(eps) == 0 || !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("eps=%d err=%v, want episodes plus ErrIncompleteEpisodeList at the %d-page limit", len(eps), err, eztvMaxPages)
	}
}

func TestEZTVGetEpisodesCompleteCatalogueHasNoError(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, true, 0, 1)
	if _, err := NewEZTV().GetEpisodes("eztv/1", "1"); err != nil {
		t.Fatalf("err = %v, want nil for a fully fetched catalogue", err)
	}
}

func TestEZTVReturnsACatalogueLongerThanTenPages(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 0, 14)
	all, err := NewEZTV().torrents("eztv/1")
	if err != nil {
		t.Fatal(err)
	}
	if want := 14*eztvPageSize + 5; len(all) != want {
		t.Fatalf("got %d torrents, want %d: a show past 1,000 releases lost its tail", len(all), want)
	}
	if _, err := NewEZTV().GetEpisodes("eztv/1", "1"); err != nil {
		t.Fatalf("GetEpisodes err = %v, want nil for a catalogue fully walked", err)
	}
}

func TestEZTVEndlessCatalogueStopsAtTheSafetyCap(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 0, 1<<30)
	e := NewEZTV()
	eps, err := e.GetEpisodes("eztv/1", "1")
	if len(eps) == 0 || !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("eps=%d err=%v, want episodes plus ErrIncompleteEpisodeList", len(eps), err)
	}
	if got := atomic.LoadInt32(&hits); got != eztvMaxPages {
		t.Fatalf("fetched %d pages, want exactly the cap %d", got, eztvMaxPages)
	}
	if _, err := e.GetEpisodes("eztv/1", "1"); !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Errorf("second call err = %v, a catalogue cut at the cap was remembered as complete", err)
	}
}

func TestEZTVSlowCatalogueStopsAtTheTimeBudget(t *testing.T) {
	old := eztvBudget
	eztvBudget = 300 * time.Millisecond
	t.Cleanup(func() { eztvBudget = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		var torrents []map[string]any
		for i := 0; i < eztvPageSize; i++ {
			torrents = append(torrents, map[string]any{
				"id": i, "filename": "S.S01E01.720p.mkv", "magnet_url": "magnet:?xt=urn:btih:x",
				"season": "1", "episode": "1", "seeds": 1, "size_bytes": 1,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"torrents": torrents})
	}))
	t.Cleanup(srv.Close)
	oldBase := eztvBase
	eztvBase = srv.URL
	t.Cleanup(func() { eztvBase = oldBase })

	start := time.Now()
	eps, err := NewEZTV().GetEpisodes("eztv/1", "1")
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("walk took %v, the budget did not bound it", elapsed)
	}
	if len(eps) == 0 || !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("eps=%d err=%v, want episodes plus ErrIncompleteEpisodeList", len(eps), err)
	}
}

func TestEZTVFailedLaterPageOfALongCatalogueIsFlagged(t *testing.T) {
	var hits int32
	eztvPagedServer(t, &hits, false, 12, 14)
	e := NewEZTV()
	eps, err := e.GetEpisodes("eztv/1", "1")
	if len(eps) == 0 || !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("eps=%d err=%v, want ErrIncompleteEpisodeList after page 12 failed", len(eps), err)
	}
	if got := atomic.LoadInt32(&hits); got != 12 {
		t.Fatalf("fetched %d pages, want 12 (walk ends at the failure)", got)
	}
}
