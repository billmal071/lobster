package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
)

const (
	ahKey1 = "092e021097c57370b8aa764fd5920ecd"
	ahKey2 = "5dbc8b95077c0cd455143bdc18cea038"
	ahKey3 = "26157af769349fba76fe4f17d6068eac"
)

// ahChrome is the search form every real page of the site carries in its
// header; a block page or captive portal does not.
const ahChrome = `<form class='searchf bc2' action='search.php' method='get'></form>`

const ahSearchPage = "<html><body>" + ahChrome + `<div class='linetitle c'>naruto</div>
<div class='similarimg'><div class='p1'><a href='anime.php?t2py4'><img class='coverimg' src='image.php?rhoq3' alt='Boruto'></a><div class='similarname c'><a href='anime.php?t2py4' class='c'>Boruto: Naruto Next Generations</a></div></div></div>
<div class='similarimg'><div class='p1'><a href='anime.php?ukr6y'><img class='coverimg' src='image.php?ez7kl' alt='Naruto'></a><div class='similarname c'><a href='anime.php?ukr6y' class='c'>Naruto &amp; Friends</a></div></div></div>
<div class='similarimg'><div class='p1'><div class='similarname c'><a href='anime.php?ukr6y' class='c'>Naruto &amp; Friends</a></div></div></div>
</body></html>`

func ahEpisodeLink(key, num string) string {
	return `<a class='c' id ="` + key + `" onclick='gatea( "` + key + `")' onmouseover='gateh( "` + key + `")'    href= 'gate.php'
				><div class='trackep0 watch bc2'><div class='trackep watchb bc'><div class='watch1 bc c'>Episode</div><div  class= ' watch2 bc '  >` + num + `</div><div class='watch1 bc c'>1328 d ago</div></div></div></a>`
}

// ahAnimePage is built from a var so a test can swap in a different listing.
// The live page lists newest first.
var ahAnimePage = "<html><body>" + ahChrome + `<div class='boldtext'> <div class='linetitle2 c2'>` +
	ahEpisodeLink(ahKey1, "3") + ahEpisodeLink(ahKey2, "2") + ahEpisodeLink(ahKey3, "1") +
	`</div></div></body></html>`

const ahGatePage = `<html><body>
<!-- <source src='https://ax.example/video.mp4?commented&commented' type='video/mp4' onerror="xhr()">  -->
<video id='vid' controls>
<source src='https://rx.example/video.mp4?` + ahKey1 + `&tok' type='video/mp4' onerror="xhr()">
<source src='https://ct.example/video.mp4?` + ahKey1 + `&tok&error' type='video/mp4'>
</video></body></html>`

// ahStub serves the three page kinds and records the gate cookie it was sent.
func ahStub(t *testing.T, gateBody string) (*AnimeHeaven, *string) {
	t.Helper()
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search.php":
			if r.URL.Query().Get("s") == "empty" {
				_, _ = w.Write([]byte("<html><body>" + ahChrome + "No results</body></html>"))
				return
			}
			_, _ = w.Write([]byte(ahSearchPage))
		case "/anime.php":
			if r.URL.RawQuery == "none" {
				_, _ = w.Write([]byte("<html><body>" + ahChrome + "</body></html>"))
				return
			}
			_, _ = w.Write([]byte(ahAnimePage))
		case "/gate.php":
			if c, err := r.Cookie("key"); err == nil {
				gotCookie = c.Value
			}
			_, _ = w.Write([]byte(gateBody))
		default:
			http.NotFound(w, r)
		}
	}))
	old := animeHeavenBase
	animeHeavenBase = srv.URL
	t.Cleanup(func() { animeHeavenBase = old; srv.Close() })
	return NewAnimeHeaven(), &gotCookie
}

func TestAnimeHeavenSearchParsesDedupesAndUnescapes(t *testing.T) {
	p, _ := ahStub(t, ahGatePage)
	got, err := p.Search("naruto")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 results (duplicate dropped), got %+v", got)
	}
	if got[1].ID != "ukr6y" || got[1].Title != "Naruto & Friends" || got[1].Type != media.TV {
		t.Errorf("unexpected row %+v", got[1])
	}
}

func TestAnimeHeavenSearchEmptyAndBlank(t *testing.T) {
	p, _ := ahStub(t, ahGatePage)
	got, err := p.Search("empty")
	if err != nil || len(got) != 0 {
		t.Errorf("no matches: want empty, nil; got %v, %v", got, err)
	}
	if got, err := p.Search("   "); err != nil || got != nil {
		t.Errorf("blank query must not hit the site: %v, %v", got, err)
	}
}

func TestAnimeHeavenEpisodesAscendingWithKeyAsID(t *testing.T) {
	p, _ := ahStub(t, ahGatePage)
	eps, err := p.GetEpisodes("ukr6y", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 || eps[0].Number != 1 || eps[0].ID != ahKey3 || eps[2].Number != 3 || eps[2].ID != ahKey1 {
		t.Errorf("want 1..3 ascending keyed by gate key, got %+v", eps)
	}
}

func TestAnimeHeavenEpisodesNoneAndBadID(t *testing.T) {
	p, _ := ahStub(t, ahGatePage)
	if eps, err := p.GetEpisodes("none", "1"); err != nil || len(eps) != 0 {
		t.Errorf("page with no episodes: want empty, nil; got %v, %v", eps, err)
	}
	for _, id := range []string{"", "../x", "a b", "a&b=c", strings.Repeat("a", 65)} {
		if _, err := p.GetEpisodes(id, "1"); err == nil {
			t.Errorf("id %q should be rejected", id)
		}
	}
}

func TestAnimeHeavenEpisodesDropDuplicateAndFractionalNumbers(t *testing.T) {
	dup := ahEpisodeLink("ffffffffffffffffffffffffffffffff", "3")
	frac := ahEpisodeLink("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "2.5")
	old := ahAnimePage
	ahAnimePage = strings.Replace(old, "</div></div></body>", dup+frac+"</div></div></body>", 1)
	t.Cleanup(func() { ahAnimePage = old })
	p, _ := ahStub(t, ahGatePage)
	eps, err := p.GetEpisodes("ukr6y", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 || eps[2].ID != ahKey1 {
		t.Errorf("first listing of a number wins, fractions skipped; got %+v", eps)
	}
}

func TestAnimeHeavenWatchSendsKeyCookieAndSkipsComments(t *testing.T) {
	p, cookie := ahStub(t, ahGatePage)
	st, err := p.Watch("ukr6y", ahKey1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if *cookie != ahKey1 {
		t.Errorf("gate cookie = %q, want the episode key", *cookie)
	}
	want := "https://rx.example/video.mp4?" + ahKey1 + "&tok"
	if st.URL != want {
		t.Errorf("URL = %q, want first live source %q", st.URL, want)
	}
}

func TestAnimeHeavenWatchRejectsMalformedKeyAndMissingSource(t *testing.T) {
	p, cookie := ahStub(t, "<html>gate closed</html>")
	for _, k := range []string{"xyz", ahKey1 + "0", strings.ToUpper(ahKey1), ahKey1[:31] + ";"} {
		if _, err := p.Watch("ukr6y", k, "", ""); err == nil {
			t.Errorf("key %q should be rejected", k)
		}
	}
	if *cookie != "" {
		t.Error("a malformed key must not reach the network")
	}
	if _, err := p.Watch("ukr6y", ahKey1, "", ""); err == nil {
		t.Error("a gate page with no source must be an error")
	}
}

// An attribute value may carry the separator as &amp;, so the error-mirror
// check has to run on the decoded URL.
func TestAnimeHeavenWatchSkipsEntityEncodedErrorMirror(t *testing.T) {
	gate := `<source src='https://ct.example/video.mp4?` + ahKey1 + `&amp;tok&amp;error' type='video/mp4'>
<source src='https://rx.example/video.mp4?` + ahKey1 + `&amp;tok' type='video/mp4'>`
	p, _ := ahStub(t, gate)
	st, err := p.Watch("ukr6y", ahKey1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://rx.example/video.mp4?" + ahKey1 + "&tok"; st.URL != want {
		t.Errorf("URL = %q, want %q", st.URL, want)
	}
}

func TestAnimeHeavenWatchSkipsError2Mirror(t *testing.T) {
	gate := `<source src='https://ck.example/video.mp4?` + ahKey1 + `&tok&error2' type='video/mp4'>
<source src='https://rx.example/video.mp4?` + ahKey1 + `&tok' type='video/mp4'>`
	p, _ := ahStub(t, gate)
	st, err := p.Watch("ukr6y", ahKey1, "", "")
	if err != nil || strings.Contains(st.URL, "error2") {
		t.Errorf("got %+v, %v; want the non-error mirror", st, err)
	}
}

func TestAnimeHeavenSearchSkipsBlankTitles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ahChrome + `<div class='similarname c'><a href='anime.php?abc12' class='c'>   </a></div>`))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	got, err := NewAnimeHeaven().Search("x")
	if err != nil || len(got) != 0 {
		t.Errorf("blank title must be dropped; got %v, %v", got, err)
	}
}

func TestAnimeHeavenHTTPErrorStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	if _, err := NewAnimeHeaven().Search("x"); err == nil {
		t.Error("a 500 must surface as an error, not an empty result")
	} else if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %v, want the HTTP status named", err)
	}
}

func TestAnimeHeavenSeasonsRejectsBadID(t *testing.T) {
	if _, err := NewAnimeHeaven().GetSeasons("../x"); err == nil {
		t.Error("bad show id should be rejected")
	}
}

func TestAnimeHeavenSeasonsAndEmbed(t *testing.T) {
	p, _ := ahStub(t, ahGatePage)
	s, err := p.GetSeasons("ukr6y")
	if err != nil || len(s) != 1 || s[0].Number != 1 {
		t.Errorf("one season expected, got %v, %v", s, err)
	}
	if _, err := p.GetEmbedURL("x"); err == nil {
		t.Error("GetEmbedURL must direct callers to Watch")
	}
}

// Downloads and the fallback resolver call Watch with "" or
// "<show>:<season>:<episode>", never with a gate key.
func TestAnimeHeavenWatchAcceptsResolverEpisodeIDs(t *testing.T) {
	p, cookie := ahStub(t, ahGatePage)
	for _, id := range []string{"ukr6y:1:3", "ukr6y:1:03"} {
		*cookie = ""
		if _, err := p.Watch("ukr6y", id, "", ""); err != nil {
			t.Fatalf("Watch(%q): %v", id, err)
		}
		if *cookie != ahKey1 {
			t.Errorf("Watch(%q) sent key %q, want episode 3's key", id, *cookie)
		}
	}
	*cookie = ""
	if _, err := p.Watch("ukr6y", "", "", ""); err != nil {
		t.Fatalf("Watch with empty episode id: %v", err)
	}
	if *cookie != ahKey3 {
		t.Errorf("empty episode id sent key %q, want episode 1's key", *cookie)
	}
	for _, bad := range []string{"ukr6y:2:1", "ukr6y:1:99", "ukr6y:x:1", "nonsense"} {
		if _, err := p.Watch("ukr6y", bad, "", ""); err == nil {
			t.Errorf("Watch(%q) should fail", bad)
		}
	}
}

func TestAnimeHeavenBlockPageIsUnrecognisedNotEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><title>Access Denied</title><body>blocked</body></html>`))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	p := NewAnimeHeaven()
	if _, err := p.Search("naruto"); !errors.Is(err, ErrUnrecognisedResponse) || errors.Is(err, ErrNoResults) {
		t.Errorf("Search on a block page = %v, want ErrUnrecognisedResponse", err)
	}
	if _, err := p.GetEpisodes("ukr6y", "1"); !errors.Is(err, ErrUnrecognisedResponse) {
		t.Errorf("GetEpisodes on a block page = %v, want ErrUnrecognisedResponse", err)
	}
}

func TestAnimeHeavenWatchOnlyAcceptsHTTPSSources(t *testing.T) {
	for _, src := range []string{
		"file:///etc/passwd", "edl://%5Bx%5D", "ytdl://x", "lavf://x", "javascript:alert(1)",
		"data:video/mp4;base64,AAAA", "https:///no-host.mp4", "http://cleartext.example/video.mp4?k", "ftp://x/y", "//x/y",
	} {
		p, _ := ahStub(t, `<source src='`+src+`' type='video/mp4'>`)
		if st, err := p.Watch("ukr6y", ahKey1, "", ""); err == nil {
			t.Errorf("source %q accepted as %q", src, st.URL)
		}
	}
}

func TestAnimeHeavenWatchUnterminatedCommentHidesRest(t *testing.T) {
	gate := `<!-- <source src='https://dead.example/video.mp4?k' type='video/mp4'>
<source src='https://rx.example/video.mp4?k' type='video/mp4'>`
	p, _ := ahStub(t, gate)
	if st, err := p.Watch("ukr6y", ahKey1, "", ""); err == nil {
		t.Errorf("everything after an unterminated <!-- is a comment; got %q", st.URL)
	}
}

func TestAnimeHeavenWatchFirstLiveMirrorWins(t *testing.T) {
	gate := `<source src='https://a.example/video.mp4?k' type='video/mp4'>
<source src='https://b.example/video.mp4?k' type='video/mp4'>`
	p, _ := ahStub(t, gate)
	st, err := p.Watch("ukr6y", ahKey1, "", "")
	if err != nil || st.URL != "https://a.example/video.mp4?k" {
		t.Errorf("got %+v, %v; want the first mirror", st, err)
	}
}

func TestAnimeHeavenWatchStripsControlCharactersFromSource(t *testing.T) {
	gate := `<source src='https://a.example/video.mp4?k&#10;&#13;&#0;&#127;x' type='video/mp4'>`
	p, _ := ahStub(t, gate)
	st, err := p.Watch("ukr6y", ahKey1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://a.example/video.mp4?kx" {
		t.Errorf("URL = %q, want control characters removed", st.URL)
	}
}

func TestAnimeHeavenEpisodeZeroIsListed(t *testing.T) {
	old := ahAnimePage
	ahAnimePage = strings.Replace(old, "</div></div></body>", ahEpisodeLink("00000000000000000000000000000000", "0")+"</div></div></body>", 1)
	t.Cleanup(func() { ahAnimePage = old })
	p, _ := ahStub(t, ahGatePage)
	eps, err := p.GetEpisodes("ukr6y", "1")
	if err != nil || len(eps) != 4 || eps[0].Number != 0 {
		t.Errorf("episode 0 (a prologue or special) must be listed first; got %+v, %v", eps, err)
	}
}

// An anchor with a gate-looking id but no episode cell must not borrow the
// number of the next episode's cell.
func TestAnimeHeavenStrayAnchorDoesNotBorrowNextEpisodeNumber(t *testing.T) {
	// Left unclosed, so the next episode's anchor follows it directly.
	stray := `<a class='c' id ="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" href='x'>no cell `
	old := ahAnimePage
	ahAnimePage = strings.Replace(old, "<div class='linetitle2 c2'>", "<div class='linetitle2 c2'>"+stray, 1)
	t.Cleanup(func() { ahAnimePage = old })
	p, _ := ahStub(t, ahGatePage)
	eps, err := p.GetEpisodes("ukr6y", "1")
	if err != nil || len(eps) != 3 {
		t.Fatalf("got %+v, %v", eps, err)
	}
	for _, e := range eps {
		if e.ID == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Errorf("stray anchor listed as episode %d", e.Number)
		}
	}
}

func TestAnimeHeavenOversizedBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ahChrome))
		_, _ = w.Write([]byte(strings.Repeat("x", animeHeavenMaxBody)))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	if _, err := NewAnimeHeaven().Search("x"); err == nil {
		t.Error("a body past the cap must be an error, not a silently truncated page")
	}
}

func TestAnimeHeavenSendsBrowserHeaders(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		_, _ = w.Write([]byte(ahChrome))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	_, _ = NewAnimeHeaven().Search("x")
	if !strings.HasPrefix(ua, "Mozilla/") {
		t.Errorf("User-Agent = %q, want a browser one", ua)
	}
}

func TestAnimeHeavenOverlongShowIDNotListedBySearch(t *testing.T) {
	long := strings.Repeat("a", 33)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ahChrome + `<div class='similarname c'><a href='anime.php?` + long + `' class='c'>Long</a></div>`))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	got, err := NewAnimeHeaven().Search("x")
	if err != nil || len(got) != 0 {
		t.Errorf("a result GetEpisodes would refuse must not be listed; got %v, %v", got, err)
	}
}

// TMDB titles carry diacritics ("Shippūden") that the site's search does not
// fold, so it finds nothing for them.
func TestAnimeHeavenSearchFoldsDiacriticsInQuery(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("s")
		_, _ = w.Write([]byte(ahSearchPage))
	}))
	defer srv.Close()
	prev := animeHeavenBase
	animeHeavenBase = srv.URL
	defer func() { animeHeavenBase = prev }()
	got, err := NewAnimeHeaven().Search("Naruto Shippūden")
	if err != nil || len(got) == 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if gotQ != "Naruto Shippuden" {
		t.Errorf("query sent = %q, want the ASCII form", gotQ)
	}
}

// A closed anchor with no number must not pick up a watch2 cell that follows
// it elsewhere on the page.
func TestAnimeHeavenClosedAnchorDoesNotReadPastItsClose(t *testing.T) {
	old := ahAnimePage
	tail := `<a class='c' id ="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" href='x'>no cell</a><div class=' watch2 bc '>9</div>`
	ahAnimePage = strings.Replace(old, "</div></div></body>", tail+"</div></div></body>", 1)
	t.Cleanup(func() { ahAnimePage = old })
	p, _ := ahStub(t, ahGatePage)
	eps, err := p.GetEpisodes("ukr6y", "1")
	if err != nil || len(eps) != 3 {
		t.Errorf("got %+v, %v; want only the 3 real episodes", eps, err)
	}
}

// The next anchor may open with any whitespace after "<a", as ahEpisodeRe
// accepts; an unclosed stray before it must still stop there.
func TestAnimeHeavenStrayAnchorBoundedByAnyWhitespaceAnchor(t *testing.T) {
	for _, ws := range []string{"\n", "\t"} {
		stray := `<a class='c' id ="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" href='x'>no cell `
		old := ahAnimePage
		page := strings.Replace(old, "<a class='c' id =\""+ahKey1, stray+"<a"+ws+"class='c' id =\""+ahKey1, 1)
		ahAnimePage = page
		p, _ := ahStub(t, ahGatePage)
		eps, err := p.GetEpisodes("ukr6y", "1")
		ahAnimePage = old
		if err != nil || len(eps) != 3 || eps[2].ID != ahKey1 {
			t.Errorf("ws %q: got %+v, %v; want the 3 real episodes with their own keys", ws, eps, err)
		}
	}
}
