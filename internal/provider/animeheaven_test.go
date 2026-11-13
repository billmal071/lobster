package provider

import (
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

const ahSearchPage = `<html><body><div class='linetitle c'>naruto</div>
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
var ahAnimePage = `<html><body><div class='boldtext'> <div class='linetitle2 c2'>` +
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
				_, _ = w.Write([]byte("<html><body>nothing</body></html>"))
				return
			}
			_, _ = w.Write([]byte(ahSearchPage))
		case "/anime.php":
			if r.URL.RawQuery == "none" {
				_, _ = w.Write([]byte("<html><body>no episodes</body></html>"))
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
	for _, k := range []string{"", "xyz", ahKey1 + "0", strings.ToUpper(ahKey1), ahKey1[:31] + ";"} {
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
