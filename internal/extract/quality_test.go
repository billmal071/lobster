package extract

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// masterPlaylist serves an HLS master with the given heights, in the order given.
func masterPlaylist(t *testing.T, heights ...int) *httptest.Server {
	t.Helper()
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, h := range heights {
		w := h * 16 / 9
		b.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=")
		b.WriteString(itoa(w))
		b.WriteString("x")
		b.WriteString(itoa(h))
		b.WriteString("\n")
		b.WriteString(itoa(h) + "p.m3u8\n")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// "best" must take the highest rung on offer, including above 1080 — the whole
// point is not to be capped by a preset number.
func TestSelectHLSVariantBestPicksHighest(t *testing.T) {
	srv := masterPlaylist(t, 2160, 1440, 1080, 720)
	m := NewMegaCloud()
	m.client = srv.Client()

	got, err := m.selectHLSVariant(srv.URL+"/master.m3u8", "best", "")
	if err != nil {
		t.Fatalf("selectHLSVariant: %v", err)
	}
	if !strings.HasSuffix(got, "2160p.m3u8") {
		t.Fatalf("got %q, want the 2160p variant", got)
	}
}

// Order in the playlist must not matter.
func TestSelectHLSVariantBestIgnoresPlaylistOrder(t *testing.T) {
	srv := masterPlaylist(t, 720, 2160, 1080)
	m := NewMegaCloud()
	m.client = srv.Client()

	got, err := m.selectHLSVariant(srv.URL+"/master.m3u8", "best", "")
	if err != nil {
		t.Fatalf("selectHLSVariant: %v", err)
	}
	if !strings.HasSuffix(got, "2160p.m3u8") {
		t.Fatalf("got %q, want the 2160p variant", got)
	}
}

// A numeric preference keeps capping, so existing behaviour is unchanged.
func TestSelectHLSVariantNumericStillCaps(t *testing.T) {
	srv := masterPlaylist(t, 2160, 1080, 720)
	m := NewMegaCloud()
	m.client = srv.Client()

	got, err := m.selectHLSVariant(srv.URL+"/master.m3u8", "1080", "")
	if err != nil {
		t.Fatalf("selectHLSVariant: %v", err)
	}
	if !strings.HasSuffix(got, "1080p.m3u8") {
		t.Fatalf("got %q, want the 1080p variant", got)
	}
}

// The direct-URL shortcut matches the quality string inside a source URL. With
// "best" that is meaningless text and must not decide the source.
func TestBestDoesNotStringMatchSourceURLs(t *testing.T) {
	if pickSourceByQualityString([]string{"a/best-of-times.mp4", "b/2160.mp4"}, "best") != "" {
		t.Fatal(`"best" must not be matched as literal text in a source URL`)
	}
	if got := pickSourceByQualityString([]string{"a/720.mp4", "b/1080.mp4"}, "1080"); got != "b/1080.mp4" {
		t.Fatalf("numeric match broke: got %q", got)
	}
}
