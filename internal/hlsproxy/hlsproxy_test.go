package hlsproxy

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// pngWrap prefixes payload with a minimal fake-PNG header ending in IEND,
// mimicking megaplay's segment obfuscation.
func pngWrap(payload []byte) []byte {
	hdr := []byte("\x89PNG\r\n\x1a\n" + "IHDRxxxx" + "IEND" + "crc0")
	return append(hdr, payload...)
}

func TestProxyStripsPNGPrefixFromSegment(t *testing.T) {
	tsPayload := []byte("\x47\x40\x11\x00actual-ts-bytes")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://ref.example/" {
			t.Errorf("upstream got Referer %q", r.Header.Get("Referer"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngWrap(tsPayload))
	}))
	defer upstream.Close()

	p, err := New("https://ref.example/", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	resp, err := http.Get(p.PlaylistURL(upstream.URL + "/seg0.ts"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(tsPayload) {
		t.Fatalf("segment not de-obfuscated: got %q", got)
	}
}

func TestProxyPassesThroughCleanSegment(t *testing.T) {
	clean := []byte("\x47plain-ts-no-png")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(clean)
	}))
	defer upstream.Close()

	p, _ := New("", "")
	defer p.Close()

	resp, err := http.Get(p.PlaylistURL(upstream.URL + "/seg.ts"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(clean) {
		t.Fatalf("clean segment altered: got %q", got)
	}
}

func TestProxyRewritesPlaylistURIs(t *testing.T) {
	var mux *httptest.Server
	mux = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvariant/index.m3u8\n")
		case "/variant/index.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXTINF:4.0,\nseg0.ts\n#EXTINF:4.0,\nseg1.ts\n")
		}
	}))
	defer mux.Close()

	p, _ := New("", "")
	defer p.Close()

	// Master playlist: variant URI rewritten to a proxied absolute URL.
	resp, err := http.Get(p.PlaylistURL(mux.URL + "/master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), p.base+"?u=") {
		t.Fatalf("master URIs not rewritten to proxy: %s", body)
	}
	if strings.Contains(string(body), "variant/index.m3u8\n") {
		t.Fatalf("raw variant URI leaked: %s", body)
	}

	// Follow the rewritten variant URI: its segment URIs must resolve to the
	// correct absolute upstream path (variant/seg0.ts, not /seg0.ts).
	var variantURL string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, p.base) {
			variantURL = line
		}
	}
	resp2, err := http.Get(variantURL)
	if err != nil {
		t.Fatal(err)
	}
	vbody, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	// The proxied segment URI carries the absolute upstream in its ?u= param;
	// decode it and confirm it resolved against the variant's base path.
	var segUpstream string
	for _, line := range strings.Split(string(vbody), "\n") {
		if strings.HasPrefix(line, p.base) {
			u, _ := url.Parse(line)
			raw, _ := base64.RawURLEncoding.DecodeString(u.Query().Get("u"))
			segUpstream = string(raw)
			break
		}
	}
	if !strings.HasSuffix(segUpstream, "/variant/seg0.ts") {
		t.Fatalf("segment URI not resolved against variant base: %q", segUpstream)
	}
}

// A player seeking inside a segment (or following #EXT-X-BYTERANGE) issues a
// ranged GET. The proxy must answer it against the *de-obfuscated* bytes: the
// PNG wrapper it strips shifts every offset, so forwarding the client's Range
// upstream verbatim would return the wrong bytes.
func TestProxyServesByteRangeOverDeobfuscatedPayload(t *testing.T) {
	tsPayload := []byte("0123456789abcdef")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Range"); got != "" {
			t.Errorf("upstream got Range %q; the wrapper makes offsets meaningless upstream", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngWrap(tsPayload))
	}))
	defer upstream.Close()

	p, err := New("https://ref.example/", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	req, _ := http.NewRequest(http.MethodGet, p.PlaylistURL(upstream.URL+"/seg0.ts"), nil)
	req.Header.Set("Range", "bytes=4-7")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 4-7/16"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "4567" {
		t.Fatalf("ranged body = %q, want %q", body, "4567")
	}
}

// Without a Range header the response must stay a plain 200 that advertises
// range support, so a player knows it may seek.
func TestProxyAdvertisesRangeSupport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("\x47plain-ts"))
	}))
	defer upstream.Close()

	p, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	resp, err := http.Get(p.PlaylistURL(upstream.URL + "/seg0.ts"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "video/mp2t" {
		t.Errorf("Content-Type = %q, want video/mp2t", got)
	}
}

// A body larger than the read cap must fail loudly. io.ReadAll over a
// LimitReader returns a short buffer and a nil error, so serving it would hand
// the player a silently truncated segment that decodes as a corrupt stream.
func TestProxyRejectsOversizedUpstreamBody(t *testing.T) {
	oversized := int64(maxBodyBytes) + 1
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		buf := make([]byte, 1<<20)
		for written := int64(0); written < oversized; {
			n := int64(len(buf))
			if remaining := oversized - written; remaining < n {
				n = remaining
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			written += n
		}
	}))
	defer upstream.Close()

	p, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	resp, err := http.Get(p.PlaylistURL(upstream.URL + "/huge.ts"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for an oversized upstream body", resp.StatusCode)
	}
}
