package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lobster/internal/media"
)

func vrSeal(t *testing.T, key []byte, plain string) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(iv, iv, []byte(plain), nil))
}

func vrKey(t *testing.T) []byte {
	t.Helper()
	key, err := hex.DecodeString(vidrockKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type vrSrv struct {
	Name, Enc, Lang, Type string
}

func vrJSON(servers []vrSrv) string {
	parts := make([]string, 0, len(servers))
	for _, s := range servers {
		parts = append(parts, fmt.Sprintf(`%q:{"url":%q,"language":%q,"flag":"","type":%q}`, s.Name, s.Enc, s.Lang, s.Type))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

type vrHarness struct {
	v *Vidrock

	mu        sync.Mutex
	apiPaths  []string
	playlists map[string]int
	referers  []string
}

func (h *vrHarness) apiCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.apiPaths)
}

func (h *vrHarness) playlistHits(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.playlists[name]
}

// vrNew serves apiBody at /api/... and, under /p/<name>.m3u8, a valid
// playlist for every name in good; any other name answers 404.
func vrNew(t *testing.T, apiBody func(host string) string, good ...string) *vrHarness {
	t.Helper()
	h := &vrHarness{playlists: map[string]int{}}
	okNames := map[string]bool{}
	for _, g := range good {
		okNames[g] = true
	}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			h.apiPaths = append(h.apiPaths, r.URL.Path)
			fmt.Fprint(w, apiBody(srv.URL))
		case strings.HasPrefix(r.URL.Path, "/p/"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/p/"), ".m3u8")
			h.playlists[name]++
			h.referers = append(h.referers, r.Header.Get("Referer"))
			if !okNames[name] {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nhttps://cdn.example/v.m3u8\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	h.v = NewVidrock()
	h.v.base = srv.URL
	h.v.client = srv.Client()
	h.v.allowLocal = true
	return h
}

func vrSealURL(t *testing.T, host, name string) string {
	return vrSeal(t, vrKey(t), host+"/p/"+name+".m3u8")
}

func TestVidrockKeyIsA32ByteHexConstant(t *testing.T) {
	key, err := hex.DecodeString(vidrockKeyHex)
	if err != nil || len(key) != 32 {
		t.Fatalf("vidrockKeyHex = %q: want 64 hex chars (32 bytes), got %d bytes, err %v", vidrockKeyHex, len(key), err)
	}
}

// Captured from https://vidrock.to/api/tv/219/1/1 (Orion) on 2026-11-16; it
// fails the moment the constant stops matching the site's bundle.
func TestVidrockKeyDecryptsACapturedSiteValue(t *testing.T) {
	const captured = "lo-sYTkz8aRbRBWd9QDTyTif54DDEXPKQXN4hRw7pKalbXGCnlDvrjW2CT_I95XBQlx4e0JGAV8DSFSqJzidbV6uHy-zSeCQIcw4ngt606eBNlgBglskKY_Bg95qw1t8pwiLPEVNpm3QpifFitDwKBHMxxL0N38QWWLHIkFa470xDf6AWNDAYiC8"
	got, err := vidrockDecrypt(captured)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	want := "https://stellarpathways.xyz/file2/YTFlZDVjMGU1YzQ4YjYxMmEzZDhlMTEzZWZlMjcxN2UxNzk0NTUzNDU2MTY4NzYz/master.m3u8"
	if got != want {
		t.Fatalf("decrypted = %q, want %q", got, want)
	}
}

func TestVidrockWatchResolvesATVEpisodeByTMDBID(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{
			{Name: "Nova"},
			{Name: "Orion", Enc: vrSealURL(t, host, "orion"), Lang: "English", Type: "hls"},
		})
	}, "orion")
	stream, err := h.v.Watch("tv/219", "219:1:1", "Default", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !strings.HasSuffix(stream.URL, "/p/orion.m3u8") || !strings.HasPrefix(stream.URL, "https://") {
		t.Fatalf("stream URL = %q", stream.URL)
	}
	if len(h.apiPaths) != 1 || h.apiPaths[0] != "/api/tv/219/1/1" {
		t.Fatalf("api paths = %v, want /api/tv/219/1/1", h.apiPaths)
	}
	if stream.Quality != "1080" {
		t.Fatalf("quality = %q, want the requested one passed through", stream.Quality)
	}
}

func TestVidrockWatchResolvesAMovieByTMDBID(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{{Name: "Orion", Enc: vrSealURL(t, host, "m"), Lang: "English", Type: "hls"}})
	}, "m")
	if _, err := h.v.Watch("movie/603", "", "Default", "best"); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(h.apiPaths) != 1 || h.apiPaths[0] != "/api/movie/603" {
		t.Fatalf("api paths = %v, want /api/movie/603", h.apiPaths)
	}
}

func TestVidrockWatchAllEmptyEntriesSaysNoStream(t *testing.T) {
	h := vrNew(t, func(string) string {
		return vrJSON([]vrSrv{{Name: "Nova"}, {Name: "Atlas"}})
	})
	_, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err == nil || err.Error() != "vidrock has no stream for this title" {
		t.Fatalf("err = %v, want exactly %q", err, "vidrock has no stream for this title")
	}
}

func TestVidrockWatchJSONNullSaysNoStream(t *testing.T) {
	h := vrNew(t, func(string) string { return "null" })
	_, err := h.v.Watch("movie/1", "", "Default", "1080")
	if err == nil || !strings.Contains(err.Error(), "vidrock has no stream for this title") {
		t.Fatalf("err = %v", err)
	}
}

func TestVidrockWatchMalformedJSONIsAnError(t *testing.T) {
	for _, body := range []string{"", "[]", "<html>blocked</html>", `{"Orion":`} {
		h := vrNew(t, func(string) string { return body })
		if _, err := h.v.Watch("movie/1", "", "Default", "1080"); err == nil {
			t.Errorf("body %q: want an error", body)
		}
	}
}

func TestVidrockWatchSkipsUndecodableEntries(t *testing.T) {
	short := base64.RawURLEncoding.EncodeToString(make([]byte, 27))
	otherKey := make([]byte, 32)
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{
			{Name: "A", Enc: "!!!not base64!!!", Lang: "English", Type: "hls"},
			{Name: "B", Enc: short, Lang: "English", Type: "hls"},
			{Name: "C", Enc: vrSeal(t, otherKey, host+"/p/wrongkey.m3u8"), Lang: "English", Type: "hls"},
			{Name: "D", Enc: vrSealURL(t, host, "good"), Lang: "English", Type: "hls"},
		})
	}, "good", "wrongkey")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !strings.HasSuffix(stream.URL, "/p/good.m3u8") {
		t.Fatalf("stream URL = %q, want the one entry that decrypts", stream.URL)
	}
	if h.playlistHits("wrongkey") != 0 {
		t.Fatalf("an entry sealed with another key was used")
	}
}

func TestVidrockWatchEveryEntryUndecodableSaysNoStream(t *testing.T) {
	short := base64.RawURLEncoding.EncodeToString(make([]byte, 27))
	h := vrNew(t, func(string) string {
		return vrJSON([]vrSrv{
			{Name: "A", Enc: "!!!", Lang: "English", Type: "hls"},
			{Name: "B", Enc: short, Lang: "English", Type: "hls"},
		})
	})
	_, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err == nil || !strings.Contains(err.Error(), "vidrock has no stream for this title") {
		t.Fatalf("err = %v", err)
	}
}

func TestVidrockDecryptRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":       "",
		"bad base64":  "%%%%",
		"too short":   base64.RawURLEncoding.EncodeToString(make([]byte, 27)),
		"tag only":    base64.RawURLEncoding.EncodeToString(make([]byte, 28)),
		"std padding": "AAAA====",
	}
	for name, in := range cases {
		if _, err := vidrockDecrypt(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	sealed := vrSeal(t, vrKey(t), "https://x.example/a.m3u8")
	raw, _ := base64.RawURLEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 1
	if _, err := vidrockDecrypt(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Error("tampered tag: want an error")
	}
}

func TestVidrockCheckURLTable(t *testing.T) {
	v := NewVidrock()
	for _, u := range []string{
		"https://cdn.example/a.m3u8",
		"https://cdn.example:8443/f/master.m3u8?x=1",
	} {
		if err := v.checkURL(u); err != nil {
			t.Errorf("checkURL(%q) = %v, want accepted", u, err)
		}
	}
	for _, u := range []string{
		"http://cdn.example/a.m3u8",
		"ftp://cdn.example/a.m3u8",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https:///a.m3u8",
		"https://user:pw@cdn.example/a.m3u8",
		"//cdn.example/a.m3u8",
		"/a.m3u8",
		"",
		"https://127.0.0.1/a.m3u8",
		"https://localhost/a.m3u8",
		"https://foo.localhost/a.m3u8",
		"https://[::1]/a.m3u8",
		"https://10.0.0.5/a.m3u8",
		"https://192.168.1.1/a.m3u8",
		"https://169.254.169.254/a.m3u8",
		"https://0.0.0.0/a.m3u8",
	} {
		if err := v.checkURL(u); err == nil {
			t.Errorf("checkURL(%q) accepted", u)
		}
	}
}

func TestVidrockWatchNeverFetchesAnUnsafeURL(t *testing.T) {
	var plainHits int
	var mu sync.Mutex
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		plainHits++
		mu.Unlock()
		fmt.Fprint(w, "#EXTM3U\n")
	}))
	defer plain.Close()

	h := vrNew(t, func(host string) string {
		tlsHost := strings.TrimPrefix(host, "https://")
		return vrJSON([]vrSrv{
			{Name: "A", Enc: vrSeal(t, vrKey(t), plain.URL+"/p/a.m3u8"), Lang: "English", Type: "hls"},
			{Name: "B", Enc: vrSeal(t, vrKey(t), "https://user:pw@"+tlsHost+"/p/cred.m3u8"), Lang: "English", Type: "hls"},
			{Name: "C", Enc: vrSeal(t, vrKey(t), "https://"+tlsHost+"/p/loop.m3u8"), Lang: "English", Type: "hls"},
		})
	}, "cred", "loop")
	h.v.allowLocal = false
	if _, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080"); err == nil {
		t.Fatal("want an error when every URL is unsafe")
	}
	mu.Lock()
	defer mu.Unlock()
	if plainHits != 0 || h.playlistHits("cred") != 0 || h.playlistHits("loop") != 0 {
		t.Fatalf("unsafe URLs were fetched: http %d, credentials %d, loopback %d", plainHits, h.playlistHits("cred"), h.playlistHits("loop"))
	}
}

func TestVidrockWatchRefusesAnOversizedAPIBody(t *testing.T) {
	h := vrNew(t, func(string) string {
		return `{"Nova":{"url":"","language":"","flag":"","type":"` + strings.Repeat("a", vidrockMaxBody) + `"}}`
	})
	_, err := h.v.Watch("movie/1", "", "Default", "1080")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a too-large error", err)
	}
}

func TestVidrockWatchDeduplicatesServersSharingAURL(t *testing.T) {
	h := vrNew(t, func(host string) string {
		same := vrSealURL(t, host, "dup")
		return vrJSON([]vrSrv{
			{Name: "A", Enc: same, Lang: "English", Type: "hls"},
			{Name: "B", Enc: vrSealURL(t, host, "dup"), Lang: "English", Type: "hls"},
			{Name: "C", Enc: vrSealURL(t, host, "next"), Lang: "English", Type: "hls"},
		})
	}, "next")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if h.playlistHits("dup") != 1 {
		t.Fatalf("duplicate playlist probed %d times, want once", h.playlistHits("dup"))
	}
	if !strings.HasSuffix(stream.URL, "/p/next.m3u8") {
		t.Fatalf("stream URL = %q", stream.URL)
	}
}

func TestVidrockWatchPrefersTheConfiguredAudioLanguage(t *testing.T) {
	body := func(host string) string {
		return vrJSON([]vrSrv{
			{Name: "A", Enc: vrSealURL(t, host, "hindi"), Lang: "Hindi", Type: "hls"},
			{Name: "B", Enc: vrSealURL(t, host, "english"), Lang: "English", Type: "hls"},
		})
	}
	h := vrNew(t, body, "hindi", "english")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil || !strings.HasSuffix(stream.URL, "/p/english.m3u8") {
		t.Fatalf("default: stream %v err %v, want english", stream, err)
	}
	h = vrNew(t, body, "hindi", "english")
	h.v.SetAudioLanguage("hindi")
	stream, err = h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil || !strings.HasSuffix(stream.URL, "/p/hindi.m3u8") {
		t.Fatalf("hindi: stream %v err %v, want hindi", stream, err)
	}
}

func TestVidrockWatchFallsBackToAnotherLanguageWhenThePreferredIsMissing(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{{Name: "A", Enc: vrSealURL(t, host, "hindi"), Lang: "Hindi", Type: "hls"}})
	}, "hindi")
	if _, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080"); err != nil {
		t.Fatalf("a dub is better than no stream: %v", err)
	}
}

func TestVidrockWatchPrefersHLSOverMP4InTheSameLanguage(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{
			{Name: "A", Enc: vrSeal(t, vrKey(t), host+"/p/direct.mp4"), Lang: "English", Type: "mp4"},
			{Name: "B", Enc: vrSealURL(t, host, "hls"), Lang: "English", Type: "hls"},
		})
	}, "hls")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil || !strings.HasSuffix(stream.URL, "/p/hls.m3u8") {
		t.Fatalf("stream %v err %v, want the hls entry", stream, err)
	}
}

func TestVidrockWatchSkipsAServerWhosePlaylistDoesNotAnswer(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{
			{Name: "A", Enc: vrSealURL(t, host, "dead"), Lang: "English", Type: "hls"},
			{Name: "B", Enc: vrSealURL(t, host, "alive"), Lang: "English", Type: "hls"},
		})
	}, "alive")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil || !strings.HasSuffix(stream.URL, "/p/alive.m3u8") {
		t.Fatalf("stream %v err %v, want the second server", stream, err)
	}
	if h.playlistHits("dead") != 1 {
		t.Fatalf("dead playlist probed %d times", h.playlistHits("dead"))
	}
}

func TestVidrockWatchRejectsAPlaylistThatIsNotM3U8(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fmt.Fprint(w, vrJSON([]vrSrv{{Name: "A", Enc: vrSealURL(t, srv.URL, "x"), Lang: "English", Type: "hls"}}))
			return
		}
		fmt.Fprint(w, "<html>captcha</html>")
	}))
	defer srv.Close()
	v := NewVidrock()
	v.base, v.client, v.allowLocal = srv.URL, srv.Client(), true
	if _, err := v.Watch("tv/1", "1:1:1", "Default", "1080"); err == nil {
		t.Fatal("an HTML page was accepted as a playlist")
	}
}

func TestVidrockWatchProbesThePlaylistWithoutAReferer(t *testing.T) {
	h := vrNew(t, func(host string) string {
		return vrJSON([]vrSrv{{Name: "A", Enc: vrSealURL(t, host, "ok"), Lang: "English", Type: "hls"}})
	}, "ok")
	stream, err := h.v.Watch("tv/1", "1:1:1", "Default", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.referers) != 1 || h.referers[0] != "" || stream.Referer != "" {
		t.Fatalf("probe referers %v, stream referer %q: the CDN answers without one", h.referers, stream.Referer)
	}
}

func TestVidrockWatchRefusesAPlaylistRedirectedToPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n")
	}))
	defer plain.Close()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fmt.Fprint(w, vrJSON([]vrSrv{{Name: "A", Enc: vrSealURL(t, srv.URL, "r"), Lang: "English", Type: "hls"}}))
			return
		}
		http.Redirect(w, r, plain.URL+"/x.m3u8", http.StatusFound)
	}))
	defer srv.Close()
	v := NewVidrock()
	v.base, v.client, v.allowLocal = srv.URL, srv.Client(), true
	if _, err := v.Watch("tv/1", "1:1:1", "Default", "1080"); err == nil {
		t.Fatal("a redirect to http:// was followed")
	}
}

func TestVidrockWatchAPIErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	v := NewVidrock()
	v.base, v.client, v.allowLocal = srv.URL, srv.Client(), true
	if _, err := v.Watch("movie/1", "", "Default", "1080"); err == nil {
		t.Fatal("want an error for a 403")
	}
}

func TestVidrockWatchRejectsBadSeasonEpisodeAndIDsWithoutARequest(t *testing.T) {
	h := vrNew(t, func(string) string { return vrJSON(nil) })
	cases := []struct{ media, episode string }{
		{"tv/1", "1:0:1"},
		{"tv/1", "1:1:0"},
		{"tv/1", "1:-1:1"},
		{"tv/1", "1:1:-2"},
		{"tv/1", "1:x:1"},
		{"tv/1", "1:99999999999999999999:1"},
		{"tv/1", "garbage"},
		{"tv/abc", "abc:1:1"},
		{"movie/", ""},
		{"movie/../etc", ""},
		{"movie/1?x=1", ""},
	}
	for _, c := range cases {
		if _, err := h.v.Watch(c.media, c.episode, "Default", "1080"); err == nil {
			t.Errorf("Watch(%q,%q): want an error", c.media, c.episode)
		}
	}
	if n := h.apiCalls(); n != 0 {
		t.Fatalf("%d API requests for invalid input, want 0", n)
	}
}

func TestVidrockWatchEpisodeBeyondTheSeasonSaysNoStream(t *testing.T) {
	h := vrNew(t, func(string) string { return vrJSON([]vrSrv{{Name: "Nova"}}) })
	_, err := h.v.Watch("tv/219", "219:99:99", "Default", "1080")
	if err == nil || !strings.Contains(err.Error(), "vidrock has no stream for this title") {
		t.Fatalf("err = %v", err)
	}
}

func TestVidrockIsAStreamProviderThatCannotEnumerate(t *testing.T) {
	var p Provider = NewVidrock()
	if _, ok := p.(StreamProvider); !ok {
		t.Fatal("Vidrock must be a StreamProvider so the resolver reaches it through Watch")
	}
	if _, err := NewVidrock().GetSeasons("tv/1"); err == nil {
		t.Error("GetSeasons must not invent a season list")
	}
	if _, err := NewVidrock().GetEpisodes("tv/1", "1:1"); err == nil {
		t.Error("GetEpisodes must not invent an episode list")
	}
}

func TestVidrockSearchReturnsTMDBIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"id":219,"name":"Kyle XY","media_type":"tv","first_air_date":"2006-06-26"},{"id":5,"name":"Someone","media_type":"person"}]}`)
	}))
	defer srv.Close()
	prev := tmdbBaseURL
	tmdbBaseURL = srv.URL
	defer func() { tmdbBaseURL = prev }()

	got, err := NewVidrock().Search("kyle xy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "tv/219" || got[0].Type != media.TV {
		t.Fatalf("results = %+v", got)
	}
}
