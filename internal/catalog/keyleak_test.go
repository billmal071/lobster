package catalog

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"lobster/internal/media"
)

type recordingTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	respond  func(*http.Request) *http.Response
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.requests = append(rt.requests, r)
	rt.mu.Unlock()
	return rt.respond(r), nil
}

func jsonResp(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}
}

func TestKeyOnlyGoesToTMDBAPIHost(t *testing.T) {
	rt := &recordingTransport{respond: func(r *http.Request) *http.Response {
		return jsonResp(r, `{"results":[{"id":1,"title":"T","poster_path":"/p.jpg"}]}`)
	}}
	c := New(testKey)
	c.http.Transport = rt

	c.Trending(media.Movie)
	c.Anime()
	c.Search("x")
	c.Details("movie/1")

	if len(rt.requests) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, r := range rt.requests {
		if r.URL.Host != "api.themoviedb.org" || r.URL.Scheme != "https" {
			t.Errorf("request went to %s://%s", r.URL.Scheme, r.URL.Host)
		}
		if !strings.HasPrefix(r.URL.Path, "/3/") {
			t.Errorf("path = %s", r.URL.Path)
		}
	}
}

func TestV4TokenGoesInHeaderNotURL(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.payload.sig"
	rt := &recordingTransport{respond: func(r *http.Request) *http.Response { return jsonResp(r, `{"results":[]}`) }}
	c := New(token)
	c.http.Transport = rt
	c.Trending(media.Movie)
	r := rt.requests[0]
	if r.Header.Get("Authorization") != "Bearer "+token {
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
	if strings.Contains(r.URL.String(), token) {
		t.Error("token placed in the URL")
	}
}

func TestV3KeyGoesInQuery(t *testing.T) {
	rt := &recordingTransport{respond: func(r *http.Request) *http.Response { return jsonResp(r, `{"results":[]}`) }}
	c := New(testKey)
	c.http.Transport = rt
	c.Trending(media.Movie)
	if got := rt.requests[0].URL.Query().Get("api_key"); got != testKey {
		t.Errorf("api_key = %q", got)
	}
	if rt.requests[0].Header.Get("Authorization") != "" {
		t.Error("v3 key must not be sent as a bearer header")
	}
}

func TestRedirectOffHostIsNotFollowed(t *testing.T) {
	rt := &recordingTransport{respond: func(r *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://evil.example/steal?api_key=" + testKey}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}
	}}
	c := New(testKey)
	c.http.Transport = rt
	if _, err := c.Trending(media.Movie); err == nil {
		t.Fatal("redirect off-host should fail")
	}
	for _, r := range rt.requests {
		if r.URL.Host != "api.themoviedb.org" {
			t.Errorf("followed redirect to %s", r.URL.Host)
		}
	}
}

func TestEmptyKeyClientIsNil(t *testing.T) {
	if c := New(""); c != nil {
		t.Error("New(\"\") should return nil so no request can be made keyless")
	}
	if c := New("   "); c != nil {
		t.Error("blank key should return nil")
	}
}

func TestCacheKeyExcludesSecret(t *testing.T) {
	rt := &recordingTransport{respond: func(r *http.Request) *http.Response { return jsonResp(r, `{"results":[]}`) }}
	c := New(testKey)
	c.http.Transport = rt
	c.Trending(media.Movie)
	for k := range c.cache {
		if strings.Contains(k, testKey) {
			t.Errorf("cache key holds the secret: %s", k)
		}
	}
}

type failingBody struct{ err error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b failingBody) Close() error             { return nil }

func TestNoClientErrorOrNoticeCarriesTheKey(t *testing.T) {
	keyed := "https://api.themoviedb.org/3/trending/movie/week?api_key=" + testKey
	cases := map[string]func(*http.Request) (*http.Response, error){
		"malformed redirect": func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {"https://api.themoviedb.org/%zz?api_key=" + testKey}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		},
		"transport error quoting the url": func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed for " + keyed)
		},
		"body read error quoting the url": func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
				Body: failingBody{errors.New("read " + keyed + ": reset")}}, nil
		},
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			c := New(testKey)
			c.http.Transport = roundTripFunc(respond)
			calls := map[string]func() error{
				"list":    func() error { _, err := c.Trending(media.Movie); return err },
				"details": func() error { _, err := c.Details("movie/1"); return err },
				"season":  func() error { _, err := c.Season("tv/1", 1); return err },
			}
			for call, do := range calls {
				err := do()
				if err == nil {
					t.Fatalf("%s: no error", call)
				}
				for _, text := range []string{err.Error(), fmt.Sprintf("%+v", err), fallbackNotice(err)} {
					if strings.Contains(text, testKey) || strings.Contains(text, "api_key") {
						t.Errorf("%s: %q carries the key or the keyed URL", call, text)
					}
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
