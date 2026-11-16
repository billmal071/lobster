package catalog

import (
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
