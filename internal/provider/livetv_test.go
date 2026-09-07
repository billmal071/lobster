package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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
// reaches and aborts an in-flight HTTP request: the handler waits on
// whichever comes first, the request's own context finishing or a fixed 1s
// timer. Correct code aborts at the ~50ms LoadContext deadline, so elapsed
// stays well under 1s; a regression that drops ctx from the request (e.g.
// reverting to http.NewRequest) leaves the handler to run out its full 1s
// timer, which the elapsed-time assertion below then fails on with a
// readable message — no reliance on go test's own -timeout, no goroutine
// dump. A stub/local-file seam cannot demonstrate this — only a real
// in-flight request being cancelled can. The loopback httptest server and
// the 1s cap keep this well under the "probing something real" line.
func TestLoadContextAbortsOnDeadlineAndReportsFailedSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(1 * time.Second):
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
	// Comfortably above the 50ms budget (so a slow CI box does not flake) and
	// comfortably below the handler's 1s ceiling (so a regression that
	// ignores ctx and lets the handler run its full course trips this
	// assertion cleanly, instead of the suite hanging).
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Fatalf("LoadContext took %v, want it bounded near the %v deadline (max %v)", elapsed, budget, maxElapsed)
	}
	failed := p.FailedSources()
	if len(failed) != 1 || failed[0] != srv.URL {
		t.Fatalf("FailedSources() = %v, want [%s]", failed, srv.URL)
	}
}

// FailedSources hands out provider state, so it must hand out a copy: a
// caller that sorts or rewrites the slice it gets — reasonable enough for a
// list it is about to print — would otherwise be editing the provider's own
// record, which cmd/liveref.go reads again afterwards to tell "the channel is
// gone" from "its playlist is down".
func TestFailedSourcesReturnsACopy(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")
	bad := filepath.Join(dir, "nope.m3u")

	p := NewLiveTV([]string{good, bad})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	got := p.FailedSources()
	if len(got) != 1 {
		t.Fatalf("FailedSources() = %v, want one entry", got)
	}
	got[0] = "clobbered"

	if again := p.FailedSources(); len(again) != 1 || again[0] != bad {
		t.Fatalf("FailedSources() = %v after a caller wrote to its result, want [%s]", again, bad)
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

func liveTVFixture(t *testing.T, bodies map[string]string) (*LiveTV, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	paths := map[string]string{}
	var sources []string
	names := make([]string, 0, len(bodies))
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic source order
	for _, n := range names {
		p := filepath.Join(dir, n)
		writeFile(t, p, bodies[n])
		paths[n] = p
		sources = append(sources, p)
	}
	lt := NewLiveTV(sources)
	if err := lt.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	return lt, paths
}

func TestLookupByTVGID(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One\nhttp://example.invalid/1.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{TVGID: "bbc1.uk", Source: paths["a.m3u"]})
	if len(got) != 1 || got[0].Name != "BBC One" {
		t.Fatalf("Lookup = %v, want one BBC One", got)
	}
}

func TestLookupByNameIsCaseFoldedAndTrimmed(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Sky News\nhttp://example.invalid/1.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{Name: "  sky news  ", Source: paths["a.m3u"]})
	if len(got) != 1 {
		t.Fatalf("Lookup = %v, want one match", got)
	}
}

func TestLookupReturnsEveryMatchSoCallersCanDetectAmbiguity(t *testing.T) {
	// Two channels, same name, no tvg-id, same playlist. The caller must be
	// able to see both and refuse. A (Channel, bool) signature would have
	// hidden this behind a first-wins pick — the playlist-order dependence
	// this whole design exists to escape.
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n" +
			"#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n" +
			"#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{Name: "Sports HD", Source: paths["a.m3u"]})
	if len(got) != 2 {
		t.Fatalf("Lookup returned %d matches, want 2", len(got))
	}
}

func TestLookupSourceNarrowsTheMatch(t *testing.T) {
	lt, paths := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n",
		"b.m3u": "#EXTM3U\n#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n",
	})
	if got := lt.Lookup(ChannelKey{Name: "Sports HD"}); len(got) != 2 {
		t.Fatalf("unfiltered Lookup = %d matches, want 2", len(got))
	}
	got := lt.Lookup(ChannelKey{Name: "Sports HD", Source: paths["a.m3u"]})
	if len(got) != 1 || got[0].URL != "http://example.invalid/1.m3u8" {
		t.Fatalf("source-filtered Lookup = %v, want only the a.m3u entry", got)
	}
}

func TestAllChannelsIsInMergeOrder(t *testing.T) {
	// liveTVFixture sorts sources lexically before constructing the provider,
	// which would make "declared order" indistinguishable from "sorted
	// order" if we used it here (a.m3u, b.m3u). Construct the provider
	// directly instead, declaring the lexically-later source ("z") first, so
	// only a real merge-order bug (not accidental sorting) can pass this.
	dir := t.TempDir()
	z := filepath.Join(dir, "z.m3u")
	a := filepath.Join(dir, "a.m3u")
	writeFile(t, z, "#EXTM3U\n#EXTINF:-1,Beta\nhttp://example.invalid/2.m3u8\n")
	writeFile(t, a, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	lt := NewLiveTV([]string{z, a}) // declared order: z (Beta) then a (Alpha)
	if err := lt.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	got := lt.AllChannels()
	if len(got) != 2 || got[0].Name != "Beta" || got[1].Name != "Alpha" {
		t.Fatalf("AllChannels = %v, want Beta then Alpha (declared order, not sorted order)", got)
	}
}

// TestLookupEmptyKeyMatchesNothing pins the guard that keeps a zero-value
// ChannelKey from matching every channel. It matches nothing today only
// because the `name != ""` check happens to short-circuit; nothing else
// tests that. If that guard were ever relaxed, a ref carrying no identity
// would silently resolve to the first channel in the playlist -- exactly
// the failure the return-every-match design exists to prevent.
func TestLookupEmptyKeyMatchesNothing(t *testing.T) {
	lt, _ := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n",
	})
	got := lt.Lookup(ChannelKey{})
	if len(got) != 0 {
		t.Fatalf("Lookup(ChannelKey{}) = %v, want no matches", got)
	}
}

// TestAllChannelsAndLookupDeepCopyCategories proves a caller cannot reach
// through a returned Channel's Categories slice to mutate the provider's
// own state. A plain struct copy duplicates the slice header, not its
// backing array, so this must be tested explicitly rather than trusted from
// the outer-slice copy alone.
func TestAllChannelsAndLookupDeepCopyCategories(t *testing.T) {
	lt, _ := liveTVFixture(t, map[string]string{
		"a.m3u": "#EXTM3U\n#EXTINF:-1 group-title=\"Sports\",Alpha\nhttp://example.invalid/1.m3u8\n",
	})

	all := lt.AllChannels()
	if len(all) != 1 || len(all[0].Categories) == 0 {
		t.Fatalf("AllChannels = %v, want one channel with categories", all)
	}
	all[0].Categories[0] = "TAMPERED"

	found := lt.Lookup(ChannelKey{Name: "Alpha", Source: all[0].Source})
	if len(found) != 1 {
		t.Fatalf("Lookup = %v, want one match", found)
	}
	if found[0].Categories[0] == "TAMPERED" {
		t.Fatalf("mutating a Channel returned by AllChannels leaked into provider state: %v", found[0].Categories)
	}

	looked := lt.Lookup(ChannelKey{Name: "Alpha"})
	if len(looked) != 1 {
		t.Fatalf("Lookup = %v, want one match", looked)
	}
	looked[0].Categories[0] = "TAMPERED2"
	again := lt.AllChannels()
	if len(again) != 1 || again[0].Categories[0] == "TAMPERED2" {
		t.Fatalf("mutating a Channel returned by Lookup leaked into provider state: %v", again)
	}
}
