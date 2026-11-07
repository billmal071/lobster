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
	var gotReferer, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotUA = r.Header.Get("User-Agent")
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
	if gotUA != "Mozilla/5.0" {
		t.Fatalf("default User-Agent = %q, want Mozilla/5.0", gotUA)
	}
	// A CDN that demands the User-Agent its API returned gets it on the
	// probe too, or a playable stream is discarded as dead.
	pinned := &media.Stream{URL: srv.URL + "/live.m3u8", UserAgent: "VidzeeCDN/1.0"}
	if err := validateStream(srv.Client(), pinned); err != nil {
		t.Fatalf("expected pinned-UA stream valid, got %v", err)
	}
	if gotUA != "VidzeeCDN/1.0" {
		t.Fatalf("User-Agent not replayed: %q", gotUA)
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

// A magnet without a usable info hash is nothing the torrent server can
// open, so accepting it would end the race on a stream that cannot start.
// It is rejected at the validate stage, leaving the race to other providers.
func TestValidateStreamRejectsMagnetWithoutInfoHash(t *testing.T) {
	for _, bad := range []string{
		"magnet:?dn=Foo",
		"magnet:?xt=urn:btih:xyz&dn=Foo",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef0123456&dn=Foo", // 39 hex
		"magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234561&dn=Foo",        // 32 chars, '1' is not base32
		"magnet:?xt=urn:sha1:0123456789abcdef0123456789abcdef01234567",
	} {
		if err := validateStream(nil, &media.Stream{URL: bad}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, good := range []string{
		"magnet:?xt=urn:btih:0123456789ABCDEF0123456789abcdef01234567&dn=Foo",
		"magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234567&tr=udp://t",
		"magnet:?dn=Foo&xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	} {
		if err := validateStream(nil, &media.Stream{URL: good}); err != nil {
			t.Errorf("%s rejected: %v", good, err)
		}
	}

	f := &fakeSP{
		searchResults: []media.SearchResult{{ID: "yts/1", Title: "Foo", Type: media.Movie}},
		stream:        &media.Stream{URL: "magnet:?xt=urn:btih:xyz&dn=Foo"},
	}
	r := New([]provider.Provider{f}, NewHealthStore(), nil)
	got, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err == nil || got != nil {
		t.Fatalf("a magnet with no info hash was accepted: stream=%v report=%+v", got, rep)
	}
	validated := false
	for _, a := range rep.Attempts {
		if a.Stage == "validate" {
			validated = true
		}
	}
	if !validated {
		t.Fatalf("rejection was not at the validate stage: %+v", rep.Attempts)
	}
}

func TestMagnetHasInfoHashToleratesUnencodedDisplayName(t *testing.T) {
	m := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show;S01E01%zz"
	if !magnetHasInfoHash(m) {
		t.Fatal("a magnet with a valid btih was rejected because dn did not parse")
	}
}

func TestMagnetHasInfoHashRejectsSeveralDistinctHashes(t *testing.T) {
	const a = "0123456789abcdef0123456789abcdef01234567"
	const b = "89abcdef0123456789abcdef0123456789abcdef"
	if magnetHasInfoHash("magnet:?xt=urn:btih:" + a + "&xt=urn:btih:" + b + "&dn=Show") {
		t.Fatal("a magnet naming two different info hashes was accepted")
	}
	if !magnetHasInfoHash("magnet:?xt=urn:btih:" + a + "&xt=urn:btih:" + a + "&dn=Show") {
		t.Fatal("a magnet repeating one info hash was rejected")
	}
}
