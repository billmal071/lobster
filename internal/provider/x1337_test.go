package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

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

func x1337Handler(t *testing.T, hits *int32) http.Handler {
	series := x1337Fixture(t, "x1337_search_series.html")
	movie := x1337Fixture(t, "x1337_search_movie.html")
	empty := x1337Fixture(t, "x1337_search_empty.html")
	detail := x1337Fixture(t, "x1337_detail.html")
	noMagnet := x1337Fixture(t, "x1337_detail_nomagnet.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/"):
			q := strings.ToLower(r.URL.Path)
			switch {
			case strings.Contains(q, "matrix"):
				_, _ = w.Write([]byte(movie))
			case strings.Contains(q, "nothing"):
				_, _ = w.Write([]byte(empty))
			case strings.Contains(q, "garbage"):
				_, _ = w.Write([]byte("<div><p>unclosed <b>tags <table><tr><td"))
			default:
				_, _ = w.Write([]byte(series))
			}
		case strings.HasPrefix(r.URL.Path, "/torrent/"):
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			id, _ := strconv.Atoi(parts[1])
			if id == 1006 {
				_, _ = w.Write([]byte(noMagnet))
				return
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(detail, "{{HASH}}", x1337Hash(id))))
		default:
			http.NotFound(w, r)
		}
	})
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
		if strings.HasPrefix(r.URL.Path, "/search/") {
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
	res, _ := x.Search("matrix")
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
	res, err := x.Search("matrix")
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
	res, err := x.Search("matrix")
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
			res, err := NewX1337().Search("matrix")
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
	_, err := NewX1337().Search("matrix")
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
	if _, err := NewX1337().Search("matrix"); err == nil || errors.Is(err, ErrNoResults) {
		t.Fatalf("oversized page err = %v, want a failure that is not ErrNoResults", err)
	}
	setX1337Bases(t, big.URL, live.URL)
	if res, err := NewX1337().Search("matrix"); err != nil || len(res) != 1 {
		t.Fatalf("oversized first mirror: results = %v, err = %v", res, err)
	}
}

func TestX1337SearchIsCachedAcrossTheFourStepSequence(t *testing.T) {
	var hits int32
	x := x1337Stub(t, &hits)
	res, _ := x.Search("Severance")
	_, _ = x.GetSeasons(res[0].ID)
	_, _ = x.GetEpisodes(res[0].ID, "2")
	_, _ = x.GetServers(res[0].ID, "2x10")
	if hits != 1 {
		t.Fatalf("search page fetched %d times, want 1", hits)
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
