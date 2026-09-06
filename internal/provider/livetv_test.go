package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lobster/internal/media"
)

var _ StreamProvider = (*LiveTV)(nil)

// statusDoer routes by URL substring to (status, body); unmatched routes return
// a transport error so "failed source" can be simulated (fakeDoer always 200s).
type statusDoer struct {
	routes map[string]struct {
		status int
		body   string
	}
}

func (d statusDoer) Do(r *http.Request) (*http.Response, error) {
	for sub, resp := range d.routes {
		if strings.Contains(r.URL.String(), sub) {
			return &http.Response{
				StatusCode: resp.status,
				Body:       io.NopCloser(strings.NewReader(resp.body)),
			}, nil
		}
	}
	return nil, fmt.Errorf("no route for %s", r.URL.String())
}

func newTestLiveTV(sources []string, d httpDoer) *LiveTV {
	p := NewLiveTV(sources)
	p.client = d
	p.fallback = d // keep tests network-free: fallback uses the same mock
	return p
}

func TestLiveTVCategoriesAndChannels(t *testing.T) {
	body := `#EXTINF:-1 group-title="Sports",A
http://a
#EXTINF:-1 group-title="News",B
http://b
#EXTINF:-1 group-title="Sports",C
http://c
`
	d := statusDoer{routes: map[string]struct {
		status int
		body   string
	}{"index.category.m3u": {200, body}}}
	p := newTestLiveTV([]string{"https://iptv-org.github.io/iptv/index.category.m3u"}, d)

	cats, err := p.Categories()
	if err != nil {
		t.Fatal(err)
	}
	// Sports surfaced before News, Sports count = 2.
	if len(cats) != 2 || cats[0].Title != "Sports" || cats[0].Episodes != 2 {
		t.Fatalf("categories wrong: %+v", cats)
	}
	chans, err := p.Channels("Sports")
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 2 || chans[0].Type != media.Movie {
		t.Fatalf("channels wrong: %+v", chans)
	}
}

func TestLiveTVWatchAndUniqueIDs(t *testing.T) {
	// Two channels, empty tvg-id, identical name -> must NOT collide.
	body := `#EXTINF:-1 tvg-id="" group-title="Sports",Dup
#EXTVLCOPT:http-user-agent=UA/9
http://first
#EXTINF:-1 tvg-id="" group-title="Sports",Dup
http://second
`
	d := statusDoer{routes: map[string]struct {
		status int
		body   string
	}{"p.m3u": {200, body}}}
	p := newTestLiveTV([]string{"https://x/p.m3u"}, d)
	chans, _ := p.Channels("Sports")
	if len(chans) != 2 {
		t.Fatalf("want 2 distinct channels, got %d", len(chans))
	}
	if chans[0].ID == chans[1].ID {
		t.Fatalf("ids collided: %q", chans[0].ID)
	}
	st, err := p.Watch(chans[0].ID, "", "", "")
	if err != nil || st.URL != "http://first" || st.UserAgent != "UA/9" {
		t.Fatalf("watch[0] wrong: %v / %+v", err, st)
	}
	st2, _ := p.Watch(chans[1].ID, "", "", "")
	if st2.URL != "http://second" {
		t.Fatalf("watch[1] resolved wrong stream: %+v", st2)
	}
}

func TestLiveTVLocalFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.m3u")
	if err := os.WriteFile(path, []byte("#EXTINF:-1 group-title=\"Movies\",Loc\nhttp://loc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// httpDoer would error on a local path; load() must use os.ReadFile.
	d := statusDoer{routes: map[string]struct {
		status int
		body   string
	}{}}
	p := newTestLiveTV([]string{path}, d)
	chans, err := p.Channels("Movies")
	if err != nil || len(chans) != 1 || chans[0].Title != "Loc" {
		t.Fatalf("local file source failed: %v / %+v", err, chans)
	}
}

func TestLiveTVSkipsFailedSource(t *testing.T) {
	ok := `#EXTINF:-1 group-title="Sports",A
http://a
`
	d := statusDoer{routes: map[string]struct {
		status int
		body   string
	}{"good.m3u": {200, ok}, "bad.m3u": {500, ""}}}
	p := newTestLiveTV([]string{"https://x/bad.m3u", "https://x/good.m3u"}, d)
	cats, err := p.Categories()
	if err != nil {
		t.Fatalf("one bad source must not fail the load: %v", err)
	}
	if len(cats) != 1 || cats[0].Title != "Sports" {
		t.Fatalf("good source did not survive: %+v", cats)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"http://h:8080/get.php?username=u&password=p&type=m3u_plus": "http://h:8080/get.php",
		"https://user:pass@host/path?q=1":                           "https://host/path",
		"https://iptv-org.github.io/iptv/index.category.m3u":        "https://iptv-org.github.io/iptv/index.category.m3u",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoLoadStampsSourceOnEveryChannel(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.m3u")
	b := filepath.Join(dir, "b.m3u")
	writeFile(t, a, "#EXTM3U\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One\nhttp://example.invalid/1.m3u8\n")
	writeFile(t, b, "#EXTM3U\n#EXTINF:-1 tvg-id=\"itv1.uk\",ITV1\nhttp://example.invalid/2.m3u8\n")

	p := NewLiveTV([]string{a, b})
	p.load()

	bySource := map[string]string{}
	for _, ch := range p.channels {
		bySource[ch.Name] = ch.Source
	}
	if bySource["BBC One"] != a {
		t.Errorf("BBC One Source = %q, want %q", bySource["BBC One"], a)
	}
	if bySource["ITV1"] != b {
		t.Errorf("ITV1 Source = %q, want %q", bySource["ITV1"], b)
	}
}

// writeFile is a helper; if internal/provider already has an equivalent,
// use that one instead of adding a second.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestLoadContextMergesInDeclaredSourceOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.m3u")
	b := filepath.Join(dir, "b.m3u")
	writeFile(t, a, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")
	writeFile(t, b, "#EXTM3U\n#EXTINF:-1,Beta\nhttp://example.invalid/2.m3u8\n")

	// Run repeatedly: a parallel loader that appends on completion would
	// pass once and fail intermittently. Order must be pinned to p.sources.
	for i := 0; i < 20; i++ {
		p := NewLiveTV([]string{a, b})
		if err := p.LoadContext(context.Background()); err != nil {
			t.Fatalf("LoadContext: %v", err)
		}
		if len(p.channels) != 2 {
			t.Fatalf("got %d channels, want 2", len(p.channels))
		}
		if p.channels[0].Name != "Alpha" || p.channels[1].Name != "Beta" {
			t.Fatalf("iteration %d: order = %q, %q; want Alpha, Beta",
				i, p.channels[0].Name, p.channels[1].Name)
		}
	}
}

// TestLoadContextSkipsImmediatelyFailingSourceAndReportsIt covers a source
// that fails fast (a missing local file, via os.ReadFile) rather than a
// context deadline — see TestLoadContextAbortsOnDeadlineAndReportsFailedSource
// for the deadline guarantee itself.
func TestLoadContextSkipsImmediatelyFailingSourceAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")
	// A path that does not exist fails fast and deterministically, standing
	// in for a source that did not load.
	bad := filepath.Join(dir, "missing.m3u")

	p := NewLiveTV([]string{good, bad})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext with one good source must succeed, got %v", err)
	}
	if len(p.channels) != 1 {
		t.Fatalf("got %d channels, want 1", len(p.channels))
	}
	failed := p.FailedSources()
	if len(failed) != 1 || failed[0] != bad {
		t.Fatalf("FailedSources() = %v, want [%s]", failed, bad)
	}
}

// TestLoadContextAbortsOnDeadlineAndReportsFailedSource proves ctx actually
// reaches and aborts an in-flight HTTP request: the handler sleeps far
// longer than the context's timeout, so a regression that drops ctx from the
// request (e.g. reverting to http.NewRequest) would hang until the handler
// wakes rather than returning near the deadline. A stub/local-file seam
// cannot demonstrate this — only a real in-flight request being cancelled
// can. The loopback httptest server never leaves the machine and the sleep
// is capped by the deadline, so this stays well under a second.
func TestLoadContextAbortsOnDeadlineAndReportsFailedSource(t *testing.T) {
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	p := NewLiveTV([]string{good, srv.URL})
	p.fallback = nil // isolate the deadline behaviour from the TLS-fallback retry path

	const budget = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	start := time.Now()
	err := p.LoadContext(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("LoadContext with one good source must succeed, got %v", err)
	}
	// Generous upper bound: proves LoadContext returned near the deadline
	// rather than after the handler's (effectively unbounded) sleep.
	if elapsed > 2*time.Second {
		t.Fatalf("LoadContext took %v, want it bounded near the %v deadline", elapsed, budget)
	}
	failed := p.FailedSources()
	if len(failed) != 1 || failed[0] != srv.URL {
		t.Fatalf("FailedSources() = %v, want [%s]", failed, srv.URL)
	}
}

func TestLoadContextAllSourcesFailingIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := NewLiveTV([]string{filepath.Join(dir, "nope1.m3u"), filepath.Join(dir, "nope2.m3u")})
	if err := p.LoadContext(context.Background()); err == nil {
		t.Fatal("LoadContext with every source failing must return an error")
	}
	if len(p.FailedSources()) != 2 {
		t.Fatalf("FailedSources() = %v, want both", p.FailedSources())
	}
}

func TestLoadContextZeroSourcesIsNotAnError(t *testing.T) {
	// Load-bearing: cmd/channels.go distinguishes "no sources configured"
	// from "sources failed", and reports not_configured for the former.
	p := NewLiveTV(nil)
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("zero sources must not be an error, got %v", err)
	}
	if len(p.FailedSources()) != 0 {
		t.Fatalf("FailedSources() = %v, want empty", p.FailedSources())
	}
}

func TestLoadContextCancelledContextDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewLiveTV([]string{good})
	done := make(chan struct{})
	go func() { _ = p.LoadContext(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LoadContext did not return on a cancelled context")
	}
}
