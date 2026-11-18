package catalog

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/media"
)

const testKey = "k3y-0123456789abcdef0123456789abcdef"

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithBase(testKey, srv.URL, srv.Client()), srv
}

func TestDecodeListMapsRows(t *testing.T) {
	body := `{"results":[
		{"id":603,"title":"The Matrix","release_date":"1999-03-30","poster_path":"/m.jpg"},
		{"id":1399,"media_type":"tv","name":"Game of Thrones","first_air_date":"2011-04-17","poster_path":"/g.jpg"}]}`
	got, err := decodeList([]byte(body), media.Movie, func() string { return "https://img/t/p/" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	m := got[0]
	if m.ID != "movie/603" || m.Title != "The Matrix" || m.Year != "1999" || m.Type != media.Movie || m.Poster != "https://img/t/p/w342/m.jpg" {
		t.Errorf("movie row = %+v", m)
	}
	g := got[1]
	if g.ID != "tv/1399" || g.Title != "Game of Thrones" || g.Year != "2011" || g.Type != media.TV {
		t.Errorf("tv row = %+v", g)
	}
}

func TestDecodeListEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"empty results", `{"results":[]}`, nil},
		{"null results", `{"results":null}`, nil},
		{"missing poster and year", `{"results":[{"id":5,"title":"No Art"}]}`, []string{"movie/5"}},
		{"malformed year kept empty", `{"results":[{"id":5,"title":"X","release_date":"soon"}]}`, []string{"movie/5"}},
		{"person row dropped", `{"results":[{"id":1,"media_type":"person","name":"Someone"},{"id":2,"media_type":"movie","title":"Real"}]}`, []string{"movie/2"}},
		{"collection row dropped", `{"results":[{"id":1,"media_type":"collection","name":"Saga"}]}`, nil},
		{"multi tv uses tv id", `{"results":[{"id":9,"media_type":"tv","name":"Show"}]}`, []string{"tv/9"}},
		{"duplicate ids collapse", `{"results":[{"id":7,"title":"A"},{"id":7,"title":"A again"}]}`, []string{"movie/7"}},
		{"same id different type kept", `{"results":[{"id":7,"media_type":"movie","title":"A"},{"id":7,"media_type":"tv","name":"A"}]}`, []string{"movie/7", "tv/7"}},
		{"zero and negative ids dropped", `{"results":[{"id":0,"title":"Z"},{"id":-3,"title":"N"}]}`, nil},
		{"blank title dropped", `{"results":[{"id":4,"title":"  "}]}`, nil},
		{"wrong shaped row skipped", `{"results":["str",{"id":8,"title":"Ok"}]}`, []string{"movie/8"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeList([]byte(tc.body), media.Movie, nil)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.want, ",") {
				t.Errorf("ids = %v, want %v", ids, tc.want)
			}
			for _, r := range got {
				if r.Poster != "" && !strings.HasPrefix(r.Poster, "http") {
					t.Errorf("poster %q is not a URL", r.Poster)
				}
			}
		})
	}
}

func TestDecodeListMalformedJSON(t *testing.T) {
	for _, body := range []string{``, `not json`, `{"results":`, `[1,2]`, `{}`, `null`, `{"results":{}}`, `{"results":"x"}`} {
		if _, err := decodeList([]byte(body), media.Movie, nil); err == nil {
			t.Errorf("decodeList(%q) = nil error, want error", body)
		}
	}
}

func TestOversizedBodyIsAnError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[`))
		w.Write([]byte(strings.Repeat(" ", maxBody+10)))
	})
	if _, err := c.Trending(media.Movie); err == nil {
		t.Fatal("oversized body decoded without error")
	}
}

func TestEndpoints(t *testing.T) {
	var paths []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.Query().Encode())
		io.WriteString(w, `{"results":[{"id":1,"title":"T","name":"T"}]}`)
	})
	calls := []struct {
		name string
		do   func() ([]media.SearchResult, error)
		want string
	}{
		{"trending movie", func() ([]media.SearchResult, error) { return c.Trending(media.Movie) }, "/trending/movie/week"},
		{"trending tv", func() ([]media.SearchResult, error) { return c.Trending(media.TV) }, "/trending/tv/week"},
		{"popular movie", func() ([]media.SearchResult, error) { return c.Popular(media.Movie) }, "/movie/popular"},
		{"popular tv", func() ([]media.SearchResult, error) { return c.Popular(media.TV) }, "/tv/popular"},
		{"recent movie", func() ([]media.SearchResult, error) { return c.Recent(media.Movie) }, "/movie/now_playing"},
		{"recent tv", func() ([]media.SearchResult, error) { return c.Recent(media.TV) }, "/tv/on_the_air"},
		{"search", func() ([]media.SearchResult, error) { return c.Search("matrix") }, "/search/multi"},
		{"anime", func() ([]media.SearchResult, error) { return c.Anime() }, "/discover/tv"},
	}
	for _, tc := range calls {
		paths = nil
		got, err := tc.do()
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: got %v, %v", tc.name, got, err)
		}
		var hit string
		for _, p := range paths {
			if strings.HasPrefix(p, tc.want+"?") {
				hit = p
			}
		}
		if hit == "" {
			t.Errorf("%s: no request to %s, saw %v", tc.name, tc.want, paths)
		}
		if tc.name == "anime" && (!strings.Contains(hit, "with_genres=16") || !strings.Contains(hit, "with_original_language=ja")) {
			t.Errorf("anime query = %s", hit)
		}
		if tc.name == "search" && !strings.Contains(hit, "query=matrix") {
			t.Errorf("search query = %s", hit)
		}
	}
}

func TestStatusErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrKeyRejected},
		{http.StatusTooManyRequests, ErrRateLimited},
	}
	for _, tc := range cases {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		})
		_, err := c.Trending(media.Movie)
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Trending(media.Movie)
	if err == nil || errors.Is(err, ErrKeyRejected) || errors.Is(err, ErrRateLimited) {
		t.Errorf("500: err = %v, want a plain error", err)
	}
}

func TestTimeoutIsAnError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	})
	hc := *srv.Client()
	hc.Timeout = 50 * time.Millisecond
	c.http = &hc
	if _, err := c.Trending(media.Movie); err == nil {
		t.Fatal("timeout returned no error")
	}
}

func TestErrorsNeverCarryTheKey(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	_, err := c.Trending(media.Movie)
	if err == nil {
		t.Fatal("want connection error")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("error leaks key: %v", err)
	}
}

func TestResponsesAreCached(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		io.WriteString(w, `{"results":[{"id":1,"title":"T"}]}`)
	})
	c.Trending(media.Movie)
	c.Trending(media.Movie)
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1 (cached)", n.Load())
	}
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Second) }
	c.Trending(media.Movie)
	if n.Load() != 2 {
		t.Errorf("requests after ttl = %d, want 2", n.Load())
	}
}

func TestFailuresAreNotCached(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"results":[{"id":1,"title":"T"}]}`)
	})
	if _, err := c.Trending(media.Movie); err == nil {
		t.Fatal("first call should fail")
	}
	if got, err := c.Trending(media.Movie); err != nil || len(got) != 1 {
		t.Fatalf("second call = %v, %v", got, err)
	}
}

func TestDetailsUseExternalIDs(t *testing.T) {
	var query string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Path + "?" + r.URL.RawQuery
		io.WriteString(w, `{"overview":"A hacker.","vote_average":8.2,"runtime":136,"release_date":"1999-03-30",
			"genres":[{"name":"Action"},{"name":"Sci-Fi"}],"production_countries":[{"name":"United States of America"}],
			"external_ids":{"imdb_id":"tt0133093"}}`)
	})
	d, err := c.Details("movie/603")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(query, "/movie/603?") || !strings.Contains(query, "append_to_response=external_ids") {
		t.Errorf("request = %s", query)
	}
	if d.Description != "A hacker." || d.Rating != "8.2" || d.Duration != "136 min" || d.Released != "1999-03-30" ||
		len(d.Genre) != 2 || d.Country != "United States of America" || d.IMDbID != "tt0133093" {
		t.Errorf("detail = %+v", d)
	}
}

func TestDetailsRejectBadIDs(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request expected for a malformed id")
	})
	for _, id := range []string{"", "movie/", "movie/abc", "person/1", "movie/1/../2", "tv/-4", "flixhq-slug-123", "movie/free-the-exorcist-hd-75043"} {
		if _, err := c.Details(id); err == nil {
			t.Errorf("Details(%q) = nil error", id)
		}
	}
}

func TestSeasonEpisodes(t *testing.T) {
	var path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(w, `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":0,"name":"bad"},{"episode_number":2,"name":""}]}`)
	})
	eps, err := c.Season("tv/1399", 2)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tv/1399/season/2" {
		t.Errorf("path = %s", path)
	}
	if len(eps) != 2 || eps[0].Number != 1 || eps[0].Title != "Pilot" || eps[1].Number != 2 {
		t.Errorf("episodes = %+v", eps)
	}
	if _, err := c.Season("movie/603", 1); err == nil {
		t.Error("Season on a movie id should error")
	}
	if _, err := c.Season("tv/1399", 0); err == nil {
		t.Error("Season 0 should error")
	}
}

func TestImageBaseFromConfiguration(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/configuration" {
			io.WriteString(w, `{"images":{"secure_base_url":"https://img.example/t/p/"}}`)
			return
		}
		io.WriteString(w, `{"results":[{"id":1,"title":"T","poster_path":"/p.jpg"}]}`)
	})
	got, err := c.Trending(media.Movie)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Poster != "https://img.example/t/p/w342/p.jpg" {
		t.Errorf("poster = %q", got[0].Poster)
	}
}

func TestImageBaseFallsBackWhenConfigurationFails(t *testing.T) {
	for _, cfgBody := range []string{`{"images":{"secure_base_url":"http://insecure/"}}`, `junk`, `{}`} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/configuration" {
				io.WriteString(w, cfgBody)
				return
			}
			io.WriteString(w, `{"results":[{"id":1,"title":"T","poster_path":"/p.jpg"}]}`)
		})
		got, err := c.Trending(media.Movie)
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Poster != "https://image.tmdb.org/t/p/w342/p.jpg" {
			t.Errorf("config %q: poster = %q", cfgBody, got[0].Poster)
		}
	}
}

func TestTruncatedResponseIsNotCachedAndTheNextCallRecovers(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, `{"results":[{"id":603,"tit`)
			return
		}
		io.WriteString(w, `{"results":[{"id":603,"title":"The Matrix","release_date":"1999-03-30"}]}`)
	})

	if _, err := c.Trending(media.Movie); err == nil {
		t.Fatal("truncated JSON decoded without error")
	}
	got, err := c.Trending(media.Movie)
	if err != nil || len(got) != 1 || got[0].ID != "movie/603" {
		t.Fatalf("retry = %v, %v; want the valid body, not the cached truncated one", got, err)
	}
}
