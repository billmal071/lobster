package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/media"
)

func x1337Fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func x1337Hash(id int) string { return fmt.Sprintf("%040x", id) }

type x1337Site struct {
	mu          sync.Mutex
	noMagnet    map[int]bool
	extraRows   string
	detailPaths []string
	searchPaths []string
	mode        string
}

func (s *x1337Site) detailRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.detailPaths...)
}

func x1337HTMLRow(id int, name string, seeds int) string {
	return fmt.Sprintf(`<tr><td class="coll-1 name"><a href="/torrent/%d/r%d/">%s</a></td><td class="coll-2 seeds">%d</td><td class="coll-4 size">1.0 GB</td></tr>`, id, id, name, seeds)
}

func x1337Next(page int) string {
	return fmt.Sprintf(`<div class="pagination"><ul><li><a href="/category-search/x/TV/%d/">%d</a></li></ul></div>`, page, page)
}

func (s *x1337Site) handler(t *testing.T, hits *int32) http.Handler {
	series := x1337Fixture(t, "x1337_search_series.html")
	seriesP2 := x1337Fixture(t, "x1337_search_series_p2.html")
	movie := x1337Fixture(t, "x1337_search_movie.html")
	dune := x1337Fixture(t, "x1337_search_dune.html")
	empty := x1337Fixture(t, "x1337_search_empty.html")
	messiah := x1337Fixture(t, "x1337_search_messiah.html")
	lost := x1337Fixture(t, "x1337_search_lost.html")
	packs := x1337Fixture(t, "x1337_search_packs.html")
	detail := x1337Fixture(t, "x1337_detail.html")
	noMagnet := x1337Fixture(t, "x1337_detail_nomagnet.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/category-search/"):
			s.mu.Lock()
			s.searchPaths = append(s.searchPaths, r.URL.Path)
			extra, mode := s.extraRows, s.mode
			s.mu.Unlock()
			q := strings.ToLower(r.URL.Path)
			cat, page := "", 1
			if parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(parts) == 4 {
				cat = parts[2]
				page, _ = strconv.Atoi(parts[3])
			}
			only := func(category, body string) {
				if cat == category {
					_, _ = w.Write([]byte(body))
				} else {
					_, _ = w.Write([]byte(empty))
				}
			}
			switch {
			case strings.Contains(q, "matrix"):
				only("Movies", movie)
			case strings.Contains(q, "dune%20messiah"):
				_, _ = w.Write([]byte(messiah))
			case strings.Contains(q, "dune"):
				only("Movies", dune)
			case strings.Contains(q, "packshow"):
				only("TV", packs)
			case strings.Contains(q, "lost"):
				only("TV", lost)
			case strings.Contains(q, "nothing"):
				_, _ = w.Write([]byte(empty))
			case strings.Contains(q, "garbage"):
				_, _ = w.Write([]byte("<div><p>unclosed <b>tags <table><tr><td"))
			case strings.Contains(q, "endless"):
				if cat != "TV" {
					_, _ = w.Write([]byte(empty))
					return
				}
				_, _ = fmt.Fprintf(w, `<table class="table-list"><tbody>%s</tbody></table>%s`,
					x1337HTMLRow(5000+page, fmt.Sprintf("Endless S01E%02d 720p", page), 5), x1337Next(page+1))
			case mode == "flaky" && page > 1:
				w.WriteHeader(http.StatusServiceUnavailable)
			case mode == "paged" || mode == "flaky":
				if cat != "TV" {
					_, _ = w.Write([]byte(empty))
				} else if page == 1 {
					_, _ = w.Write([]byte(strings.Replace(series, "</body>", x1337Next(2)+"</body>", 1)))
				} else {
					_, _ = w.Write([]byte(seriesP2))
				}
			default:
				only("TV", strings.Replace(series, "</tbody>", extra+"</tbody>", 1))
			}
		case strings.HasPrefix(r.URL.Path, "/torrent/"):
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			id, _ := strconv.Atoi(parts[1])
			s.mu.Lock()
			s.detailPaths = append(s.detailPaths, r.URL.Path)
			none := id == 1006 || s.noMagnet[id]
			s.mu.Unlock()
			if none {
				_, _ = w.Write([]byte(noMagnet))
				return
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(detail, "{{HASH}}", x1337Hash(id))))
		default:
			http.NotFound(w, r)
		}
	})
}

func x1337Handler(t *testing.T, hits *int32) http.Handler {
	return (&x1337Site{}).handler(t, hits)
}

func x1337SiteStub(t *testing.T, site *x1337Site) *X1337 {
	t.Helper()
	srv := httptest.NewServer(site.handler(t, nil))
	t.Cleanup(srv.Close)
	setX1337Bases(t, srv.URL)
	return NewX1337()
}

func x1337Stub(t *testing.T, hits *int32) *X1337 {
	t.Helper()
	srv := httptest.NewServer(x1337Handler(t, hits))
	t.Cleanup(srv.Close)
	setX1337Bases(t, srv.URL)
	return NewX1337()
}

func setX1337Bases(t *testing.T, bases ...string) {
	t.Helper()
	prev := x1337Bases
	x1337Bases = bases
	t.Cleanup(func() { x1337Bases = prev })
}

func TestX1337SearchEmptyQueryMakesNoRequest(t *testing.T) {
	var hits int32
	x := x1337Stub(t, &hits)
	for _, q := range []string{"", "   ", "\t\n"} {
		if _, err := x.Search(q); err == nil || errors.Is(err, ErrNoResults) {
			t.Fatalf("Search(%q) err = %v, want a plain error", q, err)
		}
	}
	if hits != 0 {
		t.Fatalf("empty query reached the network %d times", hits)
	}
}

func TestX1337SearchZeroResultsIsErrNoResults(t *testing.T) {
	x := x1337Stub(t, nil)
	_, err := x.Search("nothing here")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults", err)
	}
}

func TestX1337SearchMalformedHTMLIsUnrecognisedNotEmpty(t *testing.T) {
	x := x1337Stub(t, nil)
	_, err := x.Search("garbage")
	if !errors.Is(err, ErrUnrecognisedResponse) {
		t.Fatalf("err = %v, want ErrUnrecognisedResponse", err)
	}
	if errors.Is(err, ErrNoResults) {
		t.Fatalf("a page the parser could not read was reported as an empty catalogue: %v", err)
	}
}

func TestX1337SearchSeriesReturnsOneTVResult(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("Severance")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Type != media.TV || res[0].Title != "Severance" || !strings.HasPrefix(res[0].ID, "1337x/") {
		t.Fatalf("results = %+v", res)
	}
}

func TestX1337SearchMovieReturnsMovieResult(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("The  Matrix")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Type != media.Movie || res[0].Title != "The Matrix" {
		t.Fatalf("results = %+v", res)
	}
}

func TestX1337SeasonsAndEpisodesIgnoreDuplicateAndHostileRows(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("Severance")
	seasons, err := x.GetSeasons(res[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 2 || seasons[0].Number != 1 || seasons[1].Number != 2 {
		t.Fatalf("seasons = %+v", seasons)
	}
	eps, err := x.GetEpisodes(res[0].ID, "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].Number != 10 || eps[0].ID != "2x10" {
		t.Fatalf("episodes = %+v", eps)
	}
	if _, err := x.GetEpisodes(res[0].ID, "two"); err == nil {
		t.Fatal("non-numeric season accepted")
	}
}

func TestX1337ServersAreBestPerQualityBySeeders(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("Severance")
	servers, err := x.GetServers(res[0].ID, "2x10")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %v, want one each for 2160p, 1080p, 720p (480p is unseeded)", names)
	}
	for i, prefix := range []string{"2160p ", "1080p ", "720p "} {
		if !strings.HasPrefix(names[i], prefix) {
			t.Fatalf("servers[%d] = %q, want prefix %q", i, names[i], prefix)
		}
	}
	if !strings.Contains(names[1], "120 seeds") {
		t.Fatalf("1080p server = %q, want the 120-seed release not the 50-seed one", names[1])
	}
}

func TestX1337WatchResolvesMagnetOfRequestedQuality(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("Severance")
	st, err := x.Watch(res[0].ID, "2x10", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:"+x1337Hash(1002)+"&") || st.Quality != "1080p" {
		t.Fatalf("stream = %+v", st)
	}
	if !strings.Contains(st.URL, "tracker.opentrackr.org") {
		t.Fatalf("magnet lost the trackers of the detail page: %s", st.URL)
	}
}

func TestX1337WatchSkipsAReleaseWithNoMagnet(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("Severance")
	st, err := x.Watch(res[0].ID, "2x10", "", "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(1002)) || st.Quality != "1080p" {
		t.Fatalf("best-quality release 1006 has no magnet; want 1002, got %+v", st)
	}
}

func TestX1337WatchFailsWhenNoReleaseHasAMagnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/category-search/") {
			_, _ = w.Write([]byte(x1337Fixture(t, "x1337_search_series.html")))
			return
		}
		_, _ = w.Write([]byte(x1337Fixture(t, "x1337_detail_nomagnet.html")))
	}))
	t.Cleanup(srv.Close)
	setX1337Bases(t, srv.URL)
	x := NewX1337()
	res, _ := x.Search("Severance")
	if _, err := x.Watch(res[0].ID, "2x10", "", "best"); err == nil {
		t.Fatal("Watch returned no error when no release had a magnet")
	}
}

func TestX1337QueryEpisodeMarkerSelectsThatEpisode(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("Severance S01E01")
	if err != nil {
		t.Fatal(err)
	}
	st, err := x.Watch(res[0].ID, "", "", "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(1005)) {
		t.Fatalf("query asked for S01E01 (release 1005), got %s", st.URL)
	}
}

func TestX1337SeriesWithoutAnEpisodeIsRefused(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("Severance")
	if _, err := x.Watch(res[0].ID, "", "", "best"); err == nil {
		t.Fatal("a series with no episode requested resolved to some release")
	}
	if _, err := x.GetServers(res[0].ID, ""); err == nil {
		t.Fatal("GetServers on a series with no episode succeeded")
	}
}

func TestX1337MovieWatchPicksBestSeededOfQuality(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("The Matrix")
	st, err := x.Watch(res[0].ID, "", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(2001)) {
		t.Fatalf("want the 300-seed 1080p release 2001, got %s", st.URL)
	}
	st, err = x.Watch(res[0].ID, "", "", "720")
	if err != nil || !strings.Contains(st.URL, x1337Hash(2002)) {
		t.Fatalf("720p = %+v, %v", st, err)
	}
}

func TestX1337RejectsForeignAndMalformedIDs(t *testing.T) {
	x := x1337Stub(t, nil)
	for _, id := range []string{"", "1337x/", "1337x/%zz", "yts/12", "1337x/" + strings.Repeat("a", 500)} {
		if _, err := x.Watch(id, "", "", ""); err == nil {
			t.Errorf("Watch(%q) succeeded", id)
		}
		if _, err := x.GetSeasons(id); err == nil {
			t.Errorf("GetSeasons(%q) succeeded", id)
		}
	}
}

func TestX1337MirrorDownThenUp(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	live := httptest.NewServer(x1337Handler(t, nil))
	t.Cleanup(live.Close)
	setX1337Bases(t, deadURL, live.URL)
	x := NewX1337()
	res, err := x.Search("The Matrix")
	if err != nil || len(res) != 1 {
		t.Fatalf("results = %v, err = %v; want the second mirror to answer", res, err)
	}
	if _, err := x.Watch(res[0].ID, "", "", "1080"); err != nil {
		t.Fatalf("detail fetch after mirror failover: %v", err)
	}
}

func TestX1337RemembersTheMirrorThatAnswered(t *testing.T) {
	var deadHits int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&deadHits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	live := httptest.NewServer(x1337Handler(t, nil))
	t.Cleanup(live.Close)
	setX1337Bases(t, dead.URL, live.URL)
	x := NewX1337()
	res, err := x.Search("The Matrix")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Watch(res[0].ID, "", "", "1080"); err != nil {
		t.Fatal(err)
	}
	if deadHits != 1 {
		t.Fatalf("dead mirror was asked %d times, want 1", deadHits)
	}
}

func TestX1337ChallengePageFallsThroughToNextMirror(t *testing.T) {
	challenge := x1337Fixture(t, "x1337_challenge.html")
	for name, handler := range map[string]http.HandlerFunc{
		"200 challenge body": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(challenge)) },
		"403 challenge": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(challenge))
		},
	} {
		t.Run(name, func(t *testing.T) {
			blocked := httptest.NewServer(handler)
			t.Cleanup(blocked.Close)
			live := httptest.NewServer(x1337Handler(t, nil))
			t.Cleanup(live.Close)
			setX1337Bases(t, blocked.URL, live.URL)
			res, err := NewX1337().Search("The Matrix")
			if err != nil || len(res) != 1 {
				t.Fatalf("results = %v, err = %v", res, err)
			}
		})
	}
}

func TestX1337EveryMirrorChallengedIsNotAnEmptyCatalogue(t *testing.T) {
	challenge := x1337Fixture(t, "x1337_challenge.html")
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(challenge))
	}))
	t.Cleanup(blocked.Close)
	setX1337Bases(t, blocked.URL, blocked.URL)
	_, err := NewX1337().Search("The Matrix")
	if !errors.Is(err, ErrUnrecognisedResponse) || errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrUnrecognisedResponse and not ErrNoResults", err)
	}
}

func TestX1337OversizedPageFallsThrough(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("x", 1<<20))
		for i := 0; i < 8; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	t.Cleanup(big.Close)
	live := httptest.NewServer(x1337Handler(t, nil))
	t.Cleanup(live.Close)

	setX1337Bases(t, big.URL)
	if _, err := NewX1337().Search("The Matrix"); err == nil || errors.Is(err, ErrNoResults) {
		t.Fatalf("oversized page err = %v, want a failure that is not ErrNoResults", err)
	}
	setX1337Bases(t, big.URL, live.URL)
	if res, err := NewX1337().Search("The Matrix"); err != nil || len(res) != 1 {
		t.Fatalf("oversized first mirror: results = %v, err = %v", res, err)
	}
}

func TestX1337SearchIsCachedAcrossTheFourStepSequence(t *testing.T) {
	var hits int32
	x := x1337Stub(t, &hits)
	res, _ := x.Search("Severance")
	afterSearch := atomic.LoadInt32(&hits)
	_, _ = x.GetSeasons(res[0].ID)
	_, _ = x.GetEpisodes(res[0].ID, "2")
	_, _ = x.GetServers(res[0].ID, "2x10")
	if got := atomic.LoadInt32(&hits); got != afterSearch {
		t.Fatalf("later steps fetched %d more pages, want 0", got-afterSearch)
	}
}

func TestX1337MagnetValidation(t *testing.T) {
	if _, ok := x1337MagnetHash("magnet:?dn=nohash"); ok {
		t.Fatal("magnet without btih accepted")
	}
	if _, ok := x1337MagnetHash("http://x/?xt=urn:btih:" + x1337Hash(1)); ok {
		t.Fatal("non-magnet accepted")
	}
	if _, ok := x1337MagnetHash("magnet:?xt=urn:btih:" + x1337Hash(1)); !ok {
		t.Fatal("valid magnet refused")
	}
}

func TestX1337Size(t *testing.T) {
	for in, want := range map[string]int64{
		"1.5 GB": 1610612736, "800 MB": 800 << 20, "700 KB": 700 << 10, "": 0, "huge": 0, "-3 GB": 0,
	} {
		if got := x1337Size(in); got != want {
			t.Errorf("x1337Size(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestX1337ServerIDsSurviveACacheExpiryThatReordersReleases(t *testing.T) {
	site := &x1337Site{}
	x := x1337SiteStub(t, site)
	res, _ := x.Search("Severance")
	servers, err := x.GetServers(res[0].ID, "2x10")
	if err != nil {
		t.Fatal(err)
	}
	chosen := servers[1]
	if !strings.HasPrefix(chosen.Name, "1080p ") {
		t.Fatalf("servers[1] = %+v, want the 1080p server", chosen)
	}

	site.mu.Lock()
	site.extraRows = x1337HTMLRow(1007, "Severance S02E10 2160p 4K-Z", 900) + x1337HTMLRow(1008, "Severance S02E10 1080p x265-Z", 500)
	site.mu.Unlock()
	x.mu.Lock()
	x.cache = map[string]x1337CacheEntry{}
	x.mu.Unlock()

	st, err := x.Watch(res[0].ID, "2x10", chosen.ID, "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(1002)) {
		t.Fatalf("server %q resolved to a different release after the list changed: %s", chosen.ID, st.URL)
	}
}

func TestX1337WatchTriesSameQualityAlternativesBeforeChangingQuality(t *testing.T) {
	site := &x1337Site{noMagnet: map[int]bool{1002: true}}
	x := x1337SiteStub(t, site)
	res, _ := x.Search("Severance")
	st, err := x.Watch(res[0].ID, "2x10", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(1001)) || st.Quality != "1080p" {
		t.Fatalf("stream = %+v, want the other 1080p release 1001", st)
	}
	got := site.detailRequests()
	if len(got) != 2 || !strings.HasPrefix(got[0], "/torrent/1002/") || !strings.HasPrefix(got[1], "/torrent/1001/") {
		t.Fatalf("detail pages asked = %v, want 1002 then 1001", got)
	}
}

func TestX1337ListsEpisodesFromLaterSearchPages(t *testing.T) {
	site := &x1337Site{mode: "paged"}
	x := x1337SiteStub(t, site)
	res, err := x.Search("Severance")
	if err != nil {
		t.Fatal(err)
	}
	seasons, _ := x.GetSeasons(res[0].ID)
	if len(seasons) != 3 || seasons[2].Number != 3 {
		t.Fatalf("seasons = %+v, want season 3 from page 2", seasons)
	}
	eps, _ := x.GetEpisodes(res[0].ID, "3")
	if len(eps) != 1 || eps[0].ID != "3x4" {
		t.Fatalf("episodes = %+v", eps)
	}
	st, err := x.Watch(res[0].ID, "3x4", "", "best")
	if err != nil || !strings.Contains(st.URL, x1337Hash(1010)) {
		t.Fatalf("stream = %+v, err = %v", st, err)
	}
}

func TestX1337SearchPagesAreCapped(t *testing.T) {
	site := &x1337Site{}
	x := x1337SiteStub(t, site)
	if _, err := x.Search("endless"); err != nil {
		t.Fatal(err)
	}
	site.mu.Lock()
	n := 0
	for _, p := range site.searchPaths {
		if strings.Contains(p, "/TV/") {
			n++
		}
	}
	site.mu.Unlock()
	if n != x1337MaxPages {
		t.Fatalf("search pages fetched = %d, want the cap %d", n, x1337MaxPages)
	}
}

func TestX1337KeepsEarlierPagesWhenALaterPageFailsAndDoesNotCacheThem(t *testing.T) {
	site := &x1337Site{mode: "flaky"}
	x := x1337SiteStub(t, site)
	res, err := x.Search("Severance")
	if err != nil || len(res) != 1 {
		t.Fatalf("results = %v, err = %v; page 1 should still answer", res, err)
	}
	x.mu.Lock()
	cached := len(x.cache)
	x.mu.Unlock()
	if cached != 0 {
		t.Fatal("a partially fetched result list was cached")
	}
}

func TestX1337DifferentMoviesAreSeparateSelectableResults(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("Dune")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("results = %+v, want one per release year", res)
	}
	byYear := map[string]media.SearchResult{}
	for _, r := range res {
		byYear[r.Year] = r
	}
	old, ok := byYear["1984"]
	if !ok || byYear["2021"].ID == old.ID {
		t.Fatalf("results = %+v, want distinct 1984 and 2021 entries", res)
	}
	st, err := x.Watch(old.ID, "", "", "best")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(3001)) {
		t.Fatalf("1984 entry played another film: %s", st.URL)
	}
	servers, err := x.GetServers(byYear["2021"].ID, "")
	if err != nil || len(servers) != 2 {
		t.Fatalf("2021 servers = %+v, err = %v", servers, err)
	}
}

func TestX1337SearchUsesTheMoviesAndTVCategoryPaths(t *testing.T) {
	site := &x1337Site{}
	x := x1337SiteStub(t, site)
	if _, err := x.Search("The Matrix"); err != nil {
		t.Fatal(err)
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	var movies, tv bool
	for _, p := range site.searchPaths {
		if !strings.HasPrefix(p, "/category-search/") {
			t.Fatalf("search went to %q, an all-category path that returns games, books and music", p)
		}
		movies = movies || strings.Contains(p, "/Movies/")
		tv = tv || strings.Contains(p, "/TV/")
	}
	if !movies || !tv {
		t.Fatalf("paths = %v, want both the Movies and TV categories", site.searchPaths)
	}
}

func TestX1337NearMissTitleIsNotACandidate(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("Lost")
	if err != nil {
		t.Fatal(err)
	}
	st, err := x.Watch(res[0].ID, "1x1", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.URL, x1337Hash(4002)) {
		t.Fatalf("Lost resolved to another show's release: %s", st.URL)
	}
	servers, _ := x.GetServers(res[0].ID, "1x1")
	if len(servers) != 1 {
		t.Fatalf("servers = %+v, want only the release of Lost itself", servers)
	}
}

func TestX1337NonVideoRowsAreNotResults(t *testing.T) {
	x := x1337Stub(t, nil)
	_, err := x.Search("Dune Messiah")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults: the only rows are an ebook and a game", err)
	}
}

func TestX1337ReleaseYearIsTheReleaseNotATitleNumber(t *testing.T) {
	for name, want := range map[string]string{
		"Blade Runner 2049 2017 1080p BluRay": "2017",
		"Dune 1984 1080p":                     "1984",
		"2012 1080p BluRay":                   "",
		"Dune (2021) 720p WEB":                "2021",
		"The.Matrix.1999.1080p.BluRay":        "1999",
		"Some Show S01E01 720p":               "",
	} {
		if got := x1337ReleaseYear(name); got != want {
			t.Errorf("x1337ReleaseYear(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestX1337TitleMatchRequiresTheWholeTitle(t *testing.T) {
	for _, c := range []struct {
		query, name string
		want        bool
	}{
		{"Lost", "Lost S01E01 720p", true},
		{"Lost", "Lost in Space S01E01 1080p", false},
		{"Blade Runner 2049", "Blade Runner 2049 2017 1080p BluRay", true},
		{"Dune", "Dune 1984 1080p", true},
		{"Dune", "Dune Messiah 1080p", false},
		{"Greys Anatomy", "Grey's.Anatomy.S01E01.720p", true},
		{"Severance S02E10", "Severance S01E01 720p", true},
	} {
		if got := x1337TitleMatches(c.query, c.name); got != c.want {
			t.Errorf("x1337TitleMatches(%q, %q) = %v, want %v", c.query, c.name, got, c.want)
		}
	}
}

func TestX1337SeasonPacksMakeASeriesNotAFilm(t *testing.T) {
	x := x1337Stub(t, nil)
	res, err := x.Search("Packshow")
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Type != media.TV {
		t.Fatalf("type = %v, want TV", res[0].Type)
	}
	if _, err := x.Watch(res[0].ID, "", "", "best"); err == nil {
		t.Fatal("a season pack was played as a film")
	}
}

func TestX1337MagnetIsRebuiltWithoutWebSeedsOrForeignSources(t *testing.T) {
	x := x1337Stub(t, nil)
	res, _ := x.Search("The Matrix")
	st, err := x.Watch(res[0].ID, "", "", "1080")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"ws=", "xs=", "as=", "evil.example", "tracker.example", "Scraped"} {
		if strings.Contains(st.URL, bad) {
			t.Fatalf("scraped magnet parameter %q leaked into %s", bad, st.URL)
		}
	}
	if !strings.HasPrefix(st.URL, "magnet:?xt=urn:btih:"+x1337Hash(2001)+"&dn=The+Matrix+1999+1080p+BluRay+x264&tr=") {
		t.Fatalf("magnet = %s", st.URL)
	}
}

func TestX1337TotalTimeIsBoundedAcrossMirrors(t *testing.T) {
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hung.Close() })
	setX1337Bases(t, hung.URL, hung.URL, hung.URL)
	prevReq, prevBudget := x1337RequestTimeout, x1337Budget
	x1337RequestTimeout, x1337Budget = 200*time.Millisecond, 250*time.Millisecond
	t.Cleanup(func() { x1337RequestTimeout, x1337Budget = prevReq, prevBudget })

	start := time.Now()
	if _, err := NewX1337().Search("The Matrix"); err == nil {
		t.Fatal("hung mirrors produced a result")
	}
	if d := time.Since(start); d > 450*time.Millisecond {
		t.Fatalf("Search took %v across hung mirrors, want it bounded by the %v budget", d, x1337Budget)
	}
}
