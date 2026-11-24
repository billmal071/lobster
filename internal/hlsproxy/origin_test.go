package hlsproxy

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lobster/internal/media"
)

func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

func vercettiSegment(payloads ...[]byte) []byte {
	body := append([]byte(nil), pngSig...)
	body = append(body, pngChunk("IHDR", make([]byte, 13))...)
	body = append(body, pngChunk("IDAT", []byte{0x78, 0x9c, 0x63, 0x60})...)
	for _, p := range payloads {
		body = append(body, pngChunk("tsDa", p)...)
	}
	return append(body, pngChunk("IEND", nil)...)
}

func fetchThrough(t *testing.T, p *Proxy, upstream string) []byte {
	t.Helper()
	resp, err := http.Get(p.PlaylistURL(upstream))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body
}

func TestForStreamSendsOriginRefererAndUserAgentUpstream(t *testing.T) {
	var origin, referer, ua string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, referer, ua = r.Header.Get("Origin"), r.Header.Get("Referer"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("\x47ts"))
	}))
	defer upstream.Close()

	p, err := forStream(&media.Stream{Referer: "https://player.example/", UserAgent: "ua/1", Origin: "https://player.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fetchThrough(t, p, upstream.URL+"/seg0")

	if origin != "https://player.example" || referer != "https://player.example/" || ua != "ua/1" {
		t.Errorf("upstream headers: Origin=%q Referer=%q User-Agent=%q", origin, referer, ua)
	}
}

func TestProxyOmitsOriginWhenStreamHasNone(t *testing.T) {
	sent := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sent = r.Header["Origin"]
		_, _ = w.Write([]byte("\x47ts"))
	}))
	defer upstream.Close()

	p, err := forStream(&media.Stream{Referer: "https://player.example/"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fetchThrough(t, p, upstream.URL+"/seg0")

	if sent {
		t.Error("an Origin header was sent for a stream that names none")
	}
}

func TestProxyExtractsTSFromPNGDataChunks(t *testing.T) {
	first := []byte("\x47\x40\x00\x10first-packet")
	second := []byte("\x47\x40\x00\x11second-packet")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(vercettiSegment(first, second))
	}))
	defer upstream.Close()

	p, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if got, want := string(fetchThrough(t, p, upstream.URL+"/seg0")), string(first)+string(second); got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestStripPNGEdgeCases(t *testing.T) {
	ts := []byte("\x47payload")
	truncated := vercettiSegment(ts)
	truncated = truncated[:len(truncated)-20]
	oversized := append([]byte(nil), pngSig...)
	oversized = append(oversized, 0xff, 0xff, 0xff, 0xff, 't', 's', 'D', 'a', 0x47)
	for name, tc := range map[string]struct {
		in   []byte
		want string
	}{
		"empty":                     {nil, ""},
		"signature only":            {pngSig, string(pngSig)},
		"image without data chunks": {vercettiSegment(), string(vercettiSegment())},
		"payload after IEND wins":   {append(vercettiSegment(), ts...), string(ts)},
		"truncated chunk":           {truncated, string(truncated)},
		"oversized chunk length":    {oversized, string(oversized)},
		"duplicated data chunks":    {vercettiSegment(ts, ts), string(ts) + string(ts)},
	} {
		if got := string(stripPNG(tc.in)); got != tc.want {
			t.Errorf("%s: stripPNG = %q, want %q", name, got, tc.want)
		}
	}
}

func TestProxySegmentURIsEndInTSForFFmpegExtensionCheck(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master":
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/variant\n")
		case "/variant":
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:8.0,\n/seg/abc\n#EXT-X-ENDLIST\n")
		case "/seg/abc":
			_, _ = w.Write(vercettiSegment([]byte("\x47ts")))
		}
	}))
	defer upstream.Close()

	p, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	childURI := func(playlist []byte) *url.URL {
		for _, line := range strings.Split(string(playlist), "\n") {
			if strings.HasPrefix(line, "http") {
				u, err := url.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				return u
			}
		}
		t.Fatalf("no child URI in %q", playlist)
		return nil
	}

	variant := childURI(fetchThrough(t, p, upstream.URL+"/master"))
	if strings.HasSuffix(variant.Path, ".ts") {
		t.Errorf("variant playlist URI %q ends in .ts", variant)
	}
	variantBody, err := http.Get(variant.String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(variantBody.Body)
	variantBody.Body.Close()

	seg := childURI(body)
	if !strings.HasSuffix(seg.Path, ".ts") {
		t.Errorf("segment URI path %q does not end in .ts", seg.Path)
	}
	resp, err := http.Get(seg.String())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "\x47ts" {
		t.Errorf("segment through .ts URI = %q", got)
	}
}
