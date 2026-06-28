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
