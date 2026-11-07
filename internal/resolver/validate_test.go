package resolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func TestValidateStream(t *testing.T) {
	var gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		if r.URL.Path == "/dead" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer srv.Close()

	ok := &media.Stream{URL: srv.URL + "/live.m3u8", Referer: "https://ref/"}
	if err := validateStream(srv.Client(), ok); err != nil {
		t.Fatalf("expected live stream valid, got %v", err)
	}
	if gotReferer != "https://ref/" {
		t.Fatalf("Referer not replayed: %q", gotReferer)
	}
	dead := &media.Stream{URL: srv.URL + "/dead", Referer: "https://ref/"}
	if err := validateStream(srv.Client(), dead); err == nil {
		t.Fatal("expected dead (403) stream invalid")
	}
}

// A magnet cannot be fetched over HTTP; its reachability is only known once
// the swarm answers, which the torrent server bounds itself. validate=true
// must therefore pass a magnet through untouched.
func TestResolveWithValidateAcceptsMagnet(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Foo"
	f := &fakeSP{
		searchResults: []media.SearchResult{{ID: "yts/1", Title: "Foo", Type: media.Movie}},
		stream:        &media.Stream{URL: magnet},
	}
	r := New([]provider.Provider{f}, NewHealthStore(), nil)
	if !r.validate {
		t.Fatal("precondition: New must enable validation")
	}
	got, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err != nil || got == nil || got.URL != magnet {
		t.Fatalf("expected magnet to pass validation, got stream=%v err=%v report=%+v", got, err, rep)
	}
	for _, a := range rep.Attempts {
		if a.Stage == "validate" {
			t.Fatalf("magnet reached the HTTP validate stage: %+v", a)
		}
	}
}
