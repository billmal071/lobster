package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"lobster/internal/config"
	"lobster/internal/tbcpl"
)

const fakeTMDBKey = "fake-v3-key-0123456789"

func TestMain(m *testing.M) {
	os.Unsetenv(config.TMDBKeyEnv)
	os.Exit(m.Run())
}

type tmdbAPIStub struct {
	url      string
	hits     atomic.Int32
	lastAuth atomic.Value
	lastKey  atomic.Value
}

func newTMDBAPIStub(t *testing.T, handler http.HandlerFunc) *tmdbAPIStub {
	t.Helper()
	s := &tmdbAPIStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.lastAuth.Store(r.Header.Get("Authorization"))
		s.lastKey.Store(r.URL.Query().Get("api_key"))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL + "/3"
	old := tmdbAPIBase
	tmdbAPIBase = s.url
	t.Cleanup(func() { tmdbAPIBase = old })
	return s
}

func serveMultiFixture(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/multi" || r.URL.Query().Get("query") != "spiderman" {
			t.Errorf("keyed API hit %s?%s, want /3/search/multi?query=spiderman", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, tmdbMultiFixture)
	}
}

func refuseScrape(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "scrape must not be used", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	t.Cleanup(func() { tmdbBaseURL = old })
	return srv.URL, &hits
}

func tmdbSearchProviders(scrapeBase string) map[string]Provider {
	tb := NewTBCPL("tbcpl")
	tb.tmdbBaseURL = scrapeBase
	return map[string]Provider{
		"soap2day": NewSoap2Day(),
		"vidnest":  NewVidNest(),
		"vaplayer": NewVaPlayer(),
		"vidrock":  NewVidrock(),
		"tbcpl":    tb,
		"tbcplembed": NewTBCPLEmbed([]tbcpl.Site{
			{Name: "X", URL: "https://x.example/", Category: "movies", Status: "trusted", Enabled: true},
		}),
	}
}

func TestTMDBSearchUsesKeyedAPIWhenKeySet(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	api := newTMDBAPIStub(t, serveMultiFixture(t))
	scrapeBase, scrapeHits := refuseScrape(t)

	for name, p := range tmdbSearchProviders(scrapeBase) {
		t.Run(name, func(t *testing.T) {
			before := api.hits.Load()
			results, err := p.Search("spiderman")
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if api.hits.Load() != before+1 {
				t.Errorf("keyed API hits = %d, want %d", api.hits.Load(), before+1)
			}
			if got := api.lastKey.Load(); got != fakeTMDBKey {
				t.Errorf("api_key = %q, want the configured key", got)
			}
			if len(results) != 5 || results[0].ID != "movie/557" || results[4].ID != "tv/888" {
				t.Errorf("results = %+v, want the five movie/tv hits in order", results)
			}
		})
	}
	if scrapeHits.Load() != 0 {
		t.Errorf("keyless scrape hit %d times with a key set, want 0", scrapeHits.Load())
	}
}

func TestTMDBSearchSendsV4TokenAsBearer(t *testing.T) {
	const token = "eyJfake.v4.token"
	t.Setenv(config.TMDBKeyEnv, token)
	api := newTMDBAPIStub(t, serveMultiFixture(t))
	refuseScrape(t)

	if _, err := NewSoap2Day().Search("spiderman"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := api.lastAuth.Load(); got != "Bearer "+token {
		t.Errorf("Authorization = %q, want Bearer token", got)
	}
	if got := api.lastKey.Load(); got != "" {
		t.Errorf("api_key = %q, want none for a v4 token", got)
	}
}

func TestTMDBSearchUsesScrapeWhenNoKey(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, "  ")
	api := newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "keyed API must not be used", http.StatusInternalServerError)
	})
	srv, gotPath := newTMDBStub(t)
	old := tmdbBaseURL
	tmdbBaseURL = srv.URL
	t.Cleanup(func() { tmdbBaseURL = old })

	results, err := NewVidNest().Search("spiderman")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if *gotPath != "/search/multi" || len(results) != 5 {
		t.Errorf("scrape path %q gave %d results, want /search/multi with 5", *gotPath, len(results))
	}
	if api.hits.Load() != 0 {
		t.Errorf("keyed API hit %d times without a key, want 0", api.hits.Load())
	}
}

func TestTMDBSearchRejectedKeyErrorsWithoutScrape(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
			newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"status_message":"Invalid API key"}`, code)
			})
			scrapeBase, scrapeHits := refuseScrape(t)

			for name, p := range tmdbSearchProviders(scrapeBase) {
				_, err := p.Search("spiderman")
				if !errors.Is(err, errTMDBKeyRejected) {
					t.Errorf("%s: err = %v, want errTMDBKeyRejected", name, err)
					continue
				}
				if !strings.Contains(err.Error(), config.TMDBKeyEnv) {
					t.Errorf("%s: err = %q, want it to name %s", name, err, config.TMDBKeyEnv)
				}
				if strings.Contains(err.Error(), fakeTMDBKey) || strings.Contains(err.Error(), "api_key") {
					t.Errorf("%s: err = %q leaks the key", name, err)
				}
			}
			if scrapeHits.Load() != 0 {
				t.Errorf("scrape hit %d times after a rejected key, want 0", scrapeHits.Load())
			}
		})
	}
}

func TestTMDBSearchErrorsNeverCarryTheKey(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	refuseScrape(t)
	cases := map[string]func(t *testing.T){
		"transport failure": func(t *testing.T) {
			srv := httptest.NewServer(http.NotFoundHandler())
			srv.Close()
			old := tmdbAPIBase
			tmdbAPIBase = srv.URL + "/3"
			t.Cleanup(func() { tmdbAPIBase = old })
		},
		"server error": func(t *testing.T) {
			newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusBadGateway)
			})
		},
		"malformed body": func(t *testing.T) {
			newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"results":[`)
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t)
			_, err := NewSoap2Day().Search("spiderman")
			if err == nil {
				t.Fatal("want an error")
			}
			if msg := err.Error(); strings.Contains(msg, fakeTMDBKey) || strings.Contains(msg, "api_key") {
				t.Errorf("err = %q leaks the key", msg)
			}
		})
	}
}

func TestTMDBAPIErrorWithholdsCausesNamingTheKey(t *testing.T) {
	for _, cause := range []string{"bad " + fakeTMDBKey, "bad api_key=x", "bad " + url.QueryEscape("k y")} {
		key := fakeTMDBKey
		if strings.Contains(cause, "k+y") {
			key = "k y"
		}
		err := tmdbAPIError("TMDB API request failed", errors.New(cause), key)
		if err.Error() != "TMDB API request failed: details withheld" {
			t.Errorf("cause %q gave %q, want details withheld", cause, err)
		}
	}
	if err := tmdbAPIError("x", context.DeadlineExceeded, fakeTMDBKey); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap a key-free cause", err)
	}
}

func TestTMDBTVIMDBIDFromExternalIDs(t *testing.T) {
	cases := []struct {
		body    string
		want    string
		wantErr bool
	}{
		{`{"imdb_id":"tt0123"}`, "0123", false},
		{`{"imdb_id":null}`, "", false},
		{`{"imdb_id":""}`, "", false},
		{`{}`, "", false},
		{`{"imdb_id":"tt"}`, "", false},
		{`{"imdb_id":"ttabc"}`, "", false},
		{`{"imdb_id":"nm0000001"}`, "", false},
		{`{"imdb_id":"tt1 tt2"}`, "", false},
		{`{"imdb_id":`, "", true},
	}
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	for _, tc := range cases {
		newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/3/tv/7/external_ids" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, tc.body)
		})
		got, err := tmdbTVIMDBID(context.Background(), "", 7, func(string) ([]byte, error) {
			t.Fatal("scrape used with a key set")
			return nil, nil
		})
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("body %s: got (%q, %v), want (%q, err=%v)", tc.body, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestTMDBSearchRefusesRedirects(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	var elsewhere atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		fmt.Fprint(w, tmdbMultiFixture)
	}))
	t.Cleanup(target.Close)
	newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal?"+r.URL.RawQuery, http.StatusFound)
	})
	refuseScrape(t)

	_, err := NewSoap2Day().Search("spiderman")
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Errorf("err = %v, want the redirect reported as HTTP 302", err)
	}
	if elsewhere.Load() != 0 {
		t.Errorf("redirect target hit %d times, want 0", elsewhere.Load())
	}
}

func TestTMDBSearchRateLimitIsNamed(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	refuseScrape(t)
	_, err := NewSoap2Day().Search("spiderman")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
}

func TestTMDBPosterUsesKeyedAPIWhenKeySet(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"media_type":"movie","id":9,"title":"Keyed Poster Film","release_date":"2001-01-01","poster_path":"/k.jpg"}]}`)
	})
	refuseScrape(t)
	if got := TMDBPoster("Keyed Poster Film", "2001", false); got != "https://image.tmdb.org/t/p/w500/k.jpg" {
		t.Errorf("TMDBPoster = %q, want the keyed API poster", got)
	}
}

func TestEZTVUsesKeyedAPIForSearchAndIMDbIDs(t *testing.T) {
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, eztvTMDBFixture)
		case "/3/tv/95396/external_ids":
			fmt.Fprint(w, `{"id":95396,"imdb_id":"tt11280740"}`)
		case "/3/tv/4242/external_ids":
			fmt.Fprint(w, `{"id":4242,"imdb_id":null}`)
		default:
			http.NotFound(w, r)
		}
	})
	eztv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get-torrents" && r.URL.Query().Get("imdb_id") == "11280740" {
			fmt.Fprint(w, eztvFixture)
			return
		}
		fmt.Fprint(w, `{"imdb_id":"0","torrents_count":0,"limit":100,"page":1}`)
	}))
	t.Cleanup(eztv.Close)
	old := eztvBase
	eztvBase = eztv.URL
	t.Cleanup(func() { eztvBase = old })
	scrapeBase, scrapeHits := refuseScrape(t)

	p := NewEZTV()
	p.tmdbBaseURL = scrapeBase
	res, err := p.Search("severance")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "eztv/11280740" {
		t.Errorf("results = %+v, want only eztv/11280740", res)
	}
	if scrapeHits.Load() != 0 {
		t.Errorf("scrape hit %d times with a key set, want 0", scrapeHits.Load())
	}
}

// eztvKeyedStub serves keyed TMDB search plus external_ids answered by ids,
// and points the EZTV scrape path at a server that refuses every request.
func eztvKeyedStub(t *testing.T, ids func(w http.ResponseWriter, r *http.Request)) *EZTV {
	t.Helper()
	t.Setenv(config.TMDBKeyEnv, fakeTMDBKey)
	newTMDBAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/search/multi" {
			fmt.Fprint(w, eztvTMDBFixture)
			return
		}
		ids(w, r)
	})
	scrapeBase, _ := refuseScrape(t)
	p := NewEZTV()
	p.tmdbBaseURL = scrapeBase
	return p
}

func TestEZTVKeyedIMDbLookupRateLimitIsReported(t *testing.T) {
	var limited atomic.Bool
	limited.Store(true)
	p := eztvKeyedStub(t, func(w http.ResponseWriter, r *http.Request) {
		if limited.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"imdb_id":"tt11280740"}`)
	})
	_, err := p.Search("severance")
	if err == nil || !errors.Is(err, errTMDBRateLimited) || errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want the rate-limit error, not ErrNoResults", err)
	}
	limited.Store(false)
	res, err := p.Search("severance")
	if err != nil || len(res) == 0 {
		t.Fatalf("after the limit lifts: res=%v err=%v, want results (a 429 must not be memoised)", res, err)
	}
}

func TestEZTVKeyedIMDbLookupRejectedKeyIsReported(t *testing.T) {
	p := eztvKeyedStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := p.Search("severance")
	if err == nil || !errors.Is(err, errTMDBKeyRejected) || errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want the rejected-key error, not ErrNoResults", err)
	}
}

func TestEZTVKeyedIMDbLookupPartialFailureKeepsTheHitsFound(t *testing.T) {
	p := eztvKeyedStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/tv/95396/external_ids" {
			fmt.Fprint(w, `{"imdb_id":"tt11280740"}`)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	})
	res, err := p.Search("severance")
	if err != nil || len(res) != 1 || res[0].ID != "eztv/11280740" {
		t.Fatalf("res=%+v err=%v, want the one resolved hit and no error", res, err)
	}
}

func TestEZTVKeyedIMDbLookupOtherFailureIsNoResults(t *testing.T) {
	p := eztvKeyedStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := p.Search("severance")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults for a non-key, non-limit failure", err)
	}
}

func TestEZTVScrapeIMDbLookupFailureStaysNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search") {
			fmt.Fprint(w, eztvTMDBFixture)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	p := NewEZTV()
	p.tmdbBaseURL = srv.URL
	_, err := p.Search("severance")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults on the keyless path", err)
	}
}
