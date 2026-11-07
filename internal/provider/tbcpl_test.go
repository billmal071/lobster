package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lobster/internal/media"
)

func TestNewTBCPL(t *testing.T) {
	p := NewTBCPL("tbcpl")
	if p == nil {
		t.Fatal("NewTBCPL returned nil")
	}
	if p.base != tbcplDefaultBase {
		t.Errorf("expected TBCPL alias to normalize to %q, got %q", tbcplDefaultBase, p.base)
	}

	p = NewTBCPL("1shows.org")
	if p.base != "https://1shows.org" {
		t.Errorf("expected explicit host to get https scheme, got %q", p.base)
	}
}

func TestTBCPLSearchAndDetails(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/multi" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("query"); got != "breaking bad" {
			t.Errorf("query = %q, want breaking bad", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"results": [
				"breaking bad",
				{
					"id": 1396,
					"name": "Breaking Bad",
					"media_type": "tv",
					"overview": "A chemistry teacher starts over.",
					"first_air_date": "2008-01-20",
					"vote_average": 8.9
				},
				{
					"id": 299536,
					"title": "Avengers: Infinity War",
					"media_type": "movie",
					"overview": "The Avengers face Thanos.",
					"release_date": "2018-04-25",
					"vote_average": 8.2
				}
			]
		}`)
	})
	defer cleanup()

	results, err := p.Search("breaking bad")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].ID != "tv/1396" || results[0].Title != "Breaking Bad" || results[0].Type != media.TV || results[0].Year != "2008" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[1].ID != "movie/299536" || results[1].Type != media.Movie || results[1].Year != "2018" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}

	details, err := p.GetDetails("tv/1396")
	if err != nil {
		t.Fatalf("GetDetails error: %v", err)
	}
	if details.Description != "A chemistry teacher starts over." {
		t.Errorf("description = %q", details.Description)
	}
	if details.Rating != "8.9" {
		t.Errorf("rating = %q, want 8.9", details.Rating)
	}
	if details.Released != "2008-01-20" {
		t.Errorf("released = %q, want 2008-01-20", details.Released)
	}
}

func TestTBCPLSeasonsAndEpisodes(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tv/1399/season/1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"name": "Season 1",
				"season_number": 1,
				"episodes": [
					{"episode_number": 1, "season_number": 1, "name": "Winter Is Coming"},
					{"episode_number": 2, "season_number": 1, "name": "The Kingsroad"}
				]
			}`)
		case "/api/tv/1399/season/2":
			http.Error(w, `{"error":"Forbidden: Not Allowed Gang"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	seasons, err := p.GetSeasons("tv/1399")
	if err != nil {
		t.Fatalf("GetSeasons error: %v", err)
	}
	if len(seasons) != 1 || seasons[0].Number != 1 || seasons[0].ID != "1399:1" {
		t.Fatalf("unexpected seasons: %+v", seasons)
	}

	episodes, err := p.GetEpisodes("tv/1399", seasons[0].ID)
	if err != nil {
		t.Fatalf("GetEpisodes error: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("len(episodes) = %d, want 2", len(episodes))
	}
	if episodes[1].Number != 2 || episodes[1].Title != "The Kingsroad" || episodes[1].ID != "1399:1:2" {
		t.Fatalf("unexpected episode: %+v", episodes[1])
	}
}

// Streams come from core.vidzee.wtf/streams/<movie|tv>/... in plain JSON, one
// server per call selected by s=; dubs are "v6:<Language>" servers.
func TestTBCPLWatchMovie(t *testing.T) {
	const streamURL = "https://cdn.example.test/movie/master.m3u8"
	var seen []string
	var p *TBCPL
	var cleanup func()
	p, _, cleanup = newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/streams/languages/movie/1122573":
			fmt.Fprint(w, `{"languages":["Hindi"]}`)
		case "/subs/movie/1122573":
			fmt.Fprint(w, `[{"label":"English","file":"https://subs.example.test/en.vtt"},{"label":"","file":""}]`)
		case "/streams/movie/1122573":
			seen = append(seen, r.URL.Query().Get("s"))
			if got := r.Header.Get("Referer"); got != p.vidzeeBaseURL+"/" {
				t.Errorf("Referer = %q, want %q", got, p.vidzeeBaseURL+"/")
			}
			fmt.Fprintf(w, `{"language":"Auto","url":%q,"headers":{}}`, streamURL)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	stream, err := p.Watch("movie/1122573", "", "DCloud", "1080")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if stream.URL != streamURL {
		t.Errorf("stream.URL = %q, want %q", stream.URL, streamURL)
	}
	if stream.Referer != p.vidzeeBaseURL+"/" {
		t.Errorf("referer = %q", stream.Referer)
	}
	if len(stream.Subtitles) != 1 || !strings.Contains(stream.Subtitles[0].URL, "en.vtt") || stream.Subtitles[0].Language != "English" {
		t.Errorf("unexpected subtitles: %+v", stream.Subtitles)
	}
	if len(seen) != 1 || seen[0] != "dcloud" {
		t.Errorf("servers tried = %v, want [dcloud]", seen)
	}
}

func TestTBCPLWatchTV(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/streams/tv/1399/1/2":
			if got := r.URL.Query().Get("s"); got != "v6:Hindi" {
				t.Errorf("s = %q, want v6:Hindi", got)
			}
			fmt.Fprint(w, `{"language":"Hindi","url":"https://cdn.example.test/tv/master.m3u8","headers":{"referer":"https://cdn-ref.example.test/","User-Agent":"VidzeeUA/1.0"}}`)
		case "/subs/tv/1399/1/2":
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	stream, err := p.Watch("tv/1399", "1399:1:2", "vidzee:v6:Hindi", "720")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if !strings.Contains(stream.URL, "/tv/") {
		t.Errorf("unexpected stream URL: %q", stream.URL)
	}
	// The API's "headers" are what the CDN wants; they override the player
	// referer when present.
	if stream.Referer != "https://cdn-ref.example.test/" || stream.UserAgent != "VidzeeUA/1.0" {
		t.Errorf("headers not passed through: referer=%q ua=%q", stream.Referer, stream.UserAgent)
	}
}

// The default rotation tries the preferred dub first (v6:English for the
// default preference, whether or not Vidzee advertises it), then the
// language-neutral servers, then the advertised dubs, falling through
// upstream failures. Upstream reports failures as non-2xx with an
// {"error":...} body; the reason must survive into the error.
func TestTBCPLWatchDefaultServerRotation(t *testing.T) {
	var seen []string
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/streams/languages/movie/603":
			fmt.Fprint(w, `{"languages":["Hindi","Tamil"]}`)
		case "/subs/movie/603":
			fmt.Fprint(w, `[]`)
		case "/streams/movie/603":
			s := r.URL.Query().Get("s")
			seen = append(seen, s)
			switch s {
			case "dcloud":
				http.Error(w, "error code: 502", http.StatusBadGateway)
			case "tik", "v6:English":
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error":"No stream"}`)
			default:
				fmt.Fprintf(w, `{"language":%q,"url":"https://cdn.example.test/%s.m3u8","headers":{}}`, strings.TrimPrefix(s, "v6:"), s)
			}
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	stream, err := p.Watch("movie/603", "", "Default", "1080")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if stream.URL != "https://cdn.example.test/v6:Hindi.m3u8" {
		t.Errorf("stream.URL = %q", stream.URL)
	}
	want := []string{"v6:English", "dcloud", "tik", "v6:Hindi"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("servers tried = %v, want %v", seen, want)
	}

	if _, err := p.Watch("movie/603", "", "tik", "1080"); err == nil || !strings.Contains(err.Error(), "No stream") {
		t.Errorf("upstream error reason lost: %v", err)
	}

	seen = nil
	p.SetAudioLanguage("tamil")
	if _, err := p.Watch("movie/603", "", "", "1080"); err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if len(seen) == 0 || seen[0] != "v6:Tamil" {
		t.Errorf("preferred dub not tried first: %v", seen)
	}
}

func TestTBCPLGetServersListsAdvertisedDubs(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/streams/languages/tv/1399/1/2" {
			fmt.Fprint(w, `{"languages":["Hindi"]}`)
			return
		}
		http.NotFound(w, r)
	})
	defer cleanup()

	servers, err := p.GetServers("tv/1399", "1399:1:2")
	if err != nil {
		t.Fatalf("GetServers error: %v", err)
	}
	var ids []string
	for _, s := range servers {
		ids = append(ids, s.ID)
	}
	want := "vidzee:v6:English,vidzee:dcloud,vidzee:tik,vidzee:v6:Hindi"
	if strings.Join(ids, ",") != want {
		t.Errorf("server IDs = %v, want %s", ids, want)
	}
}

// /streams/languages is incomplete: Fight Club (movie/550) lists only Hindi
// while v6:English streams fine. The preferred dub and v6:English are always
// tried, so a default-preference user is not handed a Hindi dub just because
// the listing omitted English.
func TestTBCPLWatchTriesPreferredDubWhenNotAdvertised(t *testing.T) {
	var seen []string
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/streams/languages/movie/550":
			fmt.Fprint(w, `{"languages":["Hindi"]}`)
		case "/subs/movie/550":
			fmt.Fprint(w, `[]`)
		case "/streams/movie/550":
			s := r.URL.Query().Get("s")
			seen = append(seen, s)
			fmt.Fprintf(w, `{"language":%q,"url":"https://cdn.example.test/%s.m3u8","headers":{}}`, strings.TrimPrefix(s, "v6:"), s)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	stream, err := p.Watch("movie/550", "", "", "1080")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if stream.URL != "https://cdn.example.test/v6:English.m3u8" {
		t.Errorf("stream.URL = %q, want the English dub", stream.URL)
	}
	if len(seen) != 1 || seen[0] != "v6:English" {
		t.Errorf("servers tried = %v, want [v6:English]", seen)
	}

	// A non-English preference still keeps v6:English in the rotation, once,
	// after the neutral servers.
	p.SetAudioLanguage("hindi")
	srcs := p.serverCandidates(context.Background(), "movie/550")
	var links []string
	for _, s := range srcs {
		links = append(links, s.Link)
	}
	want := "v6:Hindi,dcloud,tik,v6:English"
	if strings.Join(links, ",") != want {
		t.Errorf("candidates = %v, want %s", links, want)
	}

	// An ISO preference names the same dub server as the language word:
	// "ta" is v6:Tamil, not v6:Ta, and still goes first, with Hindi — the
	// one advertised dub — keeping its place after the neutral servers.
	p.SetAudioLanguage("ta")
	links = links[:0]
	for _, s := range p.serverCandidates(context.Background(), "movie/550") {
		links = append(links, s.Link)
	}
	want = "v6:Tamil,dcloud,tik,v6:Hindi,v6:English"
	if strings.Join(links, ",") != want {
		t.Errorf("candidates for ta = %v, want %s", links, want)
	}
}

// Upstream is case-sensitive after "v6:" ("v6:english" is 400 Unknown
// server), so a user-supplied dub server is normalised to the capitalised
// form it expects.
func TestTBCPLMatchDirectServerCapitalisesDub(t *testing.T) {
	p := NewTBCPL("")
	for in, want := range map[string]string{
		"v6:english":        "v6:English",
		"V6:HINDI":          "v6:Hindi",
		"vidzee:v6:tamil":   "v6:Tamil",
		"vidzee:v6:Telugu":  "v6:Telugu",
		"vidzee:V6: french": "v6:French",
	} {
		src, ok := p.matchDirectServer(in)
		if !ok || src.Link != want {
			t.Errorf("matchDirectServer(%q) = %q, %v; want %q", in, src.Link, ok, want)
		}
	}
}

// Watch must finish inside its 20s budget even when every request stalls: the
// deadline is set before the languages lookup and bounds every request,
// including the subtitle fetch.
func TestTBCPLWatchDeadlineCoversEveryRequest(t *testing.T) {
	release := make(chan struct{})
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	defer cleanup()
	p.client.Timeout = 0 // only the Watch deadline bounds the requests

	tbcplWatchBudget = 300 * time.Millisecond
	defer func() { tbcplWatchBudget = 20 * time.Second }()

	start := time.Now()
	_, err := p.Watch("movie/603", "", "", "1080")
	if err == nil {
		t.Fatal("Watch succeeded against a stalled server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Watch took %v, want under the budget", elapsed)
	}
}

func newTestTBCPL(t *testing.T, handler http.HandlerFunc) (*TBCPL, *httptest.Server, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	p := NewTBCPL(ts.URL)
	p.client = ts.Client()
	p.tmdbBaseURL = ts.URL
	p.vidzeeBaseURL = ts.URL
	p.vidzeeAPIURL = ts.URL
	return p, ts, ts.Close
}

func TestTBCPLDubLanguageCapitalisesTheFirstRune(t *testing.T) {
	for in, want := range map[string]string{"english": "English", " ÉSPAÑOL ": "Éspañol", "élan": "Élan", "": ""} {
		if got := tbcplDubLanguage(in); got != want {
			t.Errorf("tbcplDubLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
