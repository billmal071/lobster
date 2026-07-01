package provider

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
