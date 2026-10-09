package subtitle

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// failingRoundTripper fails every request without dialling anything, so
// http.Client.Do wraps it in a *url.Error carrying the full request URL.
type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connect: connection refused")
}

// The SubDL API key travels in the query string, so every *url.Error on this
// path stringifies it. fetchAPI's error reaches debugf, which --debug prints
// and users paste into bug reports; the key must not be in it.
func TestSubDLFetchAPIErrorNeverCarriesTheAPIKey(t *testing.T) {
	const fakeKey = "notarealsubdlapikey"
	s := &SubDLClient{
		apiKey: fakeKey,
		client: &http.Client{Transport: failingRoundTripper{}},
	}
	params := url.Values{}
	params.Set("api_key", fakeKey)
	params.Set("film_name", "some film")

	_, err := s.fetchAPI(params)
	if err == nil {
		t.Fatalf("fetchAPI = nil error, want a transport failure")
	}
	if msg := err.Error(); strings.Contains(msg, fakeKey) {
		t.Errorf("fetchAPI error = %q, leaks the API key %q", msg, fakeKey)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("fetchAPI error still has a *url.Error in its chain (URL %q); anything formatting the cause re-exposes the key", ue.URL)
	}
}

// The request-construction branch is the other half of fetchAPI, and it is
// reachable: subdlAPI is a var. A control character in it makes url.Parse
// refuse the assembled string, and the *url.Error it returns carries the
// api_key exactly as the transport one does.
func TestSubDLFetchAPIRequestBuildErrorNeverCarriesTheAPIKey(t *testing.T) {
	const fakeKey = "notarealsubdlapikey"
	orig := subdlAPI
	t.Cleanup(func() { subdlAPI = orig })
	subdlAPI = "https://api.subdl.test/v1/sub\ntitles"

	s := &SubDLClient{
		apiKey: fakeKey,
		client: &http.Client{Transport: refusingRoundTripper{t: t}},
	}
	params := url.Values{}
	params.Set("api_key", fakeKey)

	_, err := s.fetchAPI(params)
	if err == nil {
		t.Fatalf("fetchAPI = nil error, want a request-construction failure")
	}
	if msg := err.Error(); strings.Contains(msg, fakeKey) {
		t.Errorf("fetchAPI error = %q, leaks the API key %q", msg, fakeKey)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("fetchAPI error still has a *url.Error in its chain (URL %q)", ue.URL)
	}
}

// refusingRoundTripper fails the test if it is reached: the construction
// branch must return before any transport is used.
type refusingRoundTripper struct{ t *testing.T }

func (d refusingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	d.t.Fatalf("RoundTrip called for %s; request construction should have failed first", r.URL)
	return nil, nil
}
