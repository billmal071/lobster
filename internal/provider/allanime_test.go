package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"lobster/internal/media"
)

func TestNewAllAnimeTranslation(t *testing.T) {
	if got := NewAllAnime(false).Translation(); got != "sub" {
		t.Fatalf("default translation = %q, want sub", got)
	}
	if got := NewAllAnime(true).Translation(); got != "dub" {
		t.Fatalf("dub translation = %q, want dub", got)
	}
	a := NewAllAnime(false)
	a.SetTranslation("dub")
	if got := a.Translation(); got != "dub" {
		t.Fatalf("after SetTranslation = %q, want dub", got)
	}
}

// Compile-time proof the type satisfies the streaming interface.
var _ StreamProvider = (*AllAnime)(nil)

// fakeDoer routes by request body/url substring to canned JSON.
type fakeDoer struct{ routes map[string]string }

func (f fakeDoer) Do(r *http.Request) (*http.Response, error) {
	body := ""
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	key := r.URL.String() + " " + body
	for sub, resp := range f.routes {
		if strings.Contains(key, sub) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(resp))}, nil
		}
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

func newTestAllAnime(routes map[string]string) *AllAnime {
	a := NewAllAnime(false)
	a.client = fakeDoer{routes: routes}
	return a
}

func TestAllAnimeSearch(t *testing.T) {
	a := newTestAllAnime(map[string]string{
		`"query":"Frieren"`: `{"data":{"shows":{"edges":[
			{"_id":"ReH","name":"Sousou no Frieren","englishName":"Frieren","thumbnail":"http://img/f.jpg","availableEpisodes":{"sub":28,"dub":28,"raw":0}}
		]}}}`,
	})
	res, err := a.Search("Frieren")
	if err != nil || len(res) != 1 {
		t.Fatalf("search: %v / %+v", err, res)
	}
	if res[0].ID != "ReH" || res[0].Title != "Frieren" || res[0].Type != media.TV || res[0].Poster != "http://img/f.jpg" || res[0].Episodes != 28 {
		t.Fatalf("mapped result wrong: %+v", res[0])
	}
}

func TestAllAnimeGetEpisodes(t *testing.T) {
	a := newTestAllAnime(map[string]string{
		`"showId":"ReH"`: `{"data":{"show":{"_id":"ReH","availableEpisodesDetail":{"sub":["3","2","1"],"dub":["2","1"],"raw":[]}}}}`,
	})
	eps, err := a.GetEpisodes("ReH", "ReH")
	if err != nil || len(eps) != 3 {
		t.Fatalf("episodes: %v / %d", err, len(eps))
	}
	// Ascending by number, native ID encodes show|episodeString|sub.
	if eps[0].Number != 1 || eps[0].ID != "ReH|1|sub" {
		t.Fatalf("ep[0] wrong: %+v", eps[0])
	}
	// Switching to dub yields the dub list.
	a.SetTranslation("dub")
	dub, _ := a.GetEpisodes("ReH", "ReH")
	if len(dub) != 2 || dub[1].ID != "ReH|2|dub" {
		t.Fatalf("dub episodes wrong: %+v", dub)
	}
}

func TestAllAnimeWatchClockPath(t *testing.T) {
	// sources query (persisted GET) returns a tobeparsed blob whose plaintext lists one "--" source.
	plain := `{"episode":{"sourceUrls":[{"sourceUrl":"--175b54575b53","sourceName":"S-mp4","priority":9}]}}`
	blob := makeBlob(plain, "Xot36i3lK3:v1")
	a := newTestAllAnime(map[string]string{
		`episodeString`: `{"data":{"tobeparsed":"` + blob + `"}}`, // sources query
		`/clock.json`:   `{"links":[{"link":"https://cdn/master.m3u8"}],"subtitles":[{"lang":"en","label":"English","src":"https://cdn/en.vtt"}]}`,
	})
	st, err := a.Watch("ReH", "ReH|1|sub", "Default", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn/master.m3u8" {
		t.Fatalf("stream URL = %q", st.URL)
	}
	if len(st.Subtitles) != 1 || st.Subtitles[0].URL != "https://cdn/en.vtt" {
		t.Fatalf("subtitles wrong: %+v", st.Subtitles)
	}
}

func TestAllAnimeTrending(t *testing.T) {
	a := newTestAllAnime(map[string]string{
		`"sortBy":"Recent"`: `{"data":{"shows":{"edges":[
			{"_id":"X1","name":"Show One","thumbnail":"http://img/1.jpg","availableEpisodes":{"sub":12,"dub":0,"raw":0}}
		]}}}`,
	})
	res, err := a.Trending(media.TV)
	if err != nil || len(res) != 1 {
		t.Fatalf("trending: %v / %+v", err, res)
	}
	if res[0].ID != "X1" || res[0].Title != "Show One" || res[0].Type != media.TV || res[0].Episodes != 12 {
		t.Fatalf("mapped trending wrong: %+v", res[0])
	}
}

func TestAllAnimeSearchRanksMainSeriesFirst(t *testing.T) {
	// AllAnime returns specials first; the relevance sort must surface the main
	// series (exact title, most episodes), not a 1-episode special.
	a := newTestAllAnime(map[string]string{
		`"query":"Death Note"`: `{"data":{"shows":{"edges":[
			{"_id":"A","name":"Death Note Rewrite","availableEpisodes":{"sub":1}},
			{"_id":"B","name":"Death Note","availableEpisodes":{"sub":37}},
			{"_id":"C","name":"Death Note: Relight","availableEpisodes":{"sub":1}}
		]}}}`,
	})
	res, err := a.Search("Death Note")
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Title != "Death Note" || res[0].Episodes != 37 {
		t.Fatalf("expected main series first, got %q (%d eps)", res[0].Title, res[0].Episodes)
	}
}
