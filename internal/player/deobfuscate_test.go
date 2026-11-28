package player

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"lobster/internal/media"
)

func TestWrapDeobfuscatedSendsStreamOriginUpstream(t *testing.T) {
	var origin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = r.Header.Get("Origin")
		_, _ = w.Write([]byte("#EXTM3U\n"))
	}))
	defer upstream.Close()

	wrapped, cleanup, err := wrapDeobfuscated(&media.Stream{
		URL:         upstream.URL + "/master.m3u8",
		Referer:     "https://player.example/",
		Origin:      "https://player.example",
		Deobfuscate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	resp, err := http.Get(wrapped.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if origin != "https://player.example" {
		t.Errorf("upstream Origin = %q, want https://player.example", origin)
	}
}
