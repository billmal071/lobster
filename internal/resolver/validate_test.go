package resolver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/media"
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
