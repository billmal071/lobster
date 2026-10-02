package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"lobster/internal/media"
)

// Compile-time proof the type satisfies the streaming interface.
var _ StreamProvider = (*AnimeOnsen)(nil)

// animeOnsenFake is an httptest stand-in for api.animeonsen.xyz plus
// cdn.animeonsen.xyz. It records every request it was sent — method, path and
// the two headers the real CDN enforces — and how many were in flight at once,
// so a test can assert on what the provider actually put on the wire rather
// than on what its return value happens to be.
type animeOnsenFake struct {
	srv *httptest.Server

	// episodes is how many episodes each content id has. An id that is not
	// here answers 404 for every episode, which is what the live CDN does for
	// a bogus one.
	episodes map[string]int
	// search maps a query to the raw JSON body to answer with. A query that is
	// not here answers 404 with the API's own miss body.
	search map[string]string
	// forbidAbove, when non-zero, answers 403 for any episode above it — the
	// shape a rejected Referer or User-Agent takes on the live CDN.
	forbidAbove int

	mu        sync.Mutex
	reqs      []animeOnsenReq
	inFlight  int
	maxFlight int
}

type animeOnsenReq struct {
	method  string
	path    string
	referer string
	ua      string
}

var animeOnsenManifestPath = regexp.MustCompile(`^/video/mp4-dash/([^/]+)/(\d+)/manifest\.mpd$`)

func newAnimeOnsenFake(t *testing.T, f *animeOnsenFake) *animeOnsenFake {
	t.Helper()
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *animeOnsenFake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, animeOnsenReq{
		method:  r.Method,
		path:    r.URL.Path,
		referer: r.Header.Get("Referer"),
		ua:      r.Header.Get("User-Agent"),
	})
	f.inFlight++
	if f.inFlight > f.maxFlight {
		f.maxFlight = f.inFlight
	}
	f.mu.Unlock()
	// Hold the request open briefly so concurrent waves genuinely overlap;
	// without this a fast handler can serialise by accident and the
	// parallelism assertion would pass for the wrong reason.
	time.Sleep(2 * time.Millisecond)
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if m := animeOnsenManifestPath.FindStringSubmatch(r.URL.Path); m != nil {
		n, _ := strconv.Atoi(m[2])
		if f.forbidAbove > 0 && n > f.forbidAbove {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if have, ok := f.episodes[m[1]]; ok && (have < 0 || n <= have) {
			w.Header().Set("Content-Type", "application/dash+xml")
			fmt.Fprintf(w, `<MPD><Period id="%d"/></MPD>`, n)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if q, ok := strings.CutPrefix(r.URL.Path, "/v4/search/"); ok {
		if body, ok := f.search[q]; ok {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"status":404,"message":"Cannot find matching content title"}`)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}

func (f *animeOnsenFake) provider() *AnimeOnsen {
	return NewAnimeOnsenAt(f.srv.URL+"/v4", f.srv.URL+"/video/mp4-dash")
}

func (f *animeOnsenFake) requests() []animeOnsenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]animeOnsenReq(nil), f.reqs...)
}

func (f *animeOnsenFake) peakConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxFlight
}

// kamuiSearchBody is the live response for "kamui", trimmed to the row the
// title-selection rules are about. Both titles are present and they disagree,
// which is the whole point of the fixture.
const kamuiSearchBody = `{"status":200,"result":[
  {"content_id":"cvYyOlmbfFWvJYWG","content_title":"Ushiro no Shoumen Kamui-san","content_title_en":"KAMUI: He's Behind You"}
]}`

// Every request this provider makes carries the exact Referer the CDN matches
// on and a User-Agent that is not ffmpeg's.
//
// Both are measured on the wire, not read off the stream: the CDN answers 403
// to the bare apex Referer, to no Referer at all, and to a "Lavf/..."
// User-Agent, so a provider that only put them in media.Stream and not in its
// own probes would report every episode as missing.
func TestAnimeOnsenSendsTheExactRefererAndANonFFmpegUserAgentOnEveryRequest(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"cvYyOlmbfFWvJYWG": 12},
		search:   map[string]string{"kamui": kamuiSearchBody},
	})
	p := f.provider()

	if _, err := p.Search("kamui"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := p.GetEpisodes("cvYyOlmbfFWvJYWG", "cvYyOlmbfFWvJYWG"); err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if _, err := p.Watch("cvYyOlmbfFWvJYWG", "3", "", "1080"); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// The expected values are written out rather than read from the package
	// constants. Comparing a request against the same constant that produced
	// it is a tautology: it holds for whatever the constant says, including
	// the apex host the CDN answers 403 to. These two literals are the
	// measured facts (2026-10-02), so they are what the test states.
	const (
		wantReferer = "https://www.animeonsen.xyz/"
		wantUAPart  = "Mozilla/5.0"
	)
	reqs := f.requests()
	if len(reqs) < 3 {
		t.Fatalf("only %d requests recorded; the fixture is not exercising the real paths", len(reqs))
	}
	for _, r := range reqs {
		if r.referer != wantReferer {
			t.Errorf("%s %s sent Referer %q, want exactly %q; the CDN 403s the apex host, a missing trailing slash and an absent header alike", r.method, r.path, r.referer, wantReferer)
		}
		// An absent header is the case worth naming: net/http substitutes
		// "Go-http-client/1.1", which is neither empty nor Lavf, so a test
		// that only rejected those two would pass with the header gone.
		if !strings.Contains(r.ua, wantUAPart) {
			t.Errorf("%s %s sent User-Agent %q, want a browser UA containing %q; the CDN 403s ffmpeg's Lavf default, and leaving the header off sends Go's own", r.method, r.path, r.ua, wantUAPart)
		}
	}
}

// The stream handed to the player and to ffmpeg carries both headers.
//
// Stream.UserAgent is load-bearing here and not cosmetic: internal/player
// folds it into --user-agent and --demuxer-lavf-o=headers=, and
// internal/download folds it into ffmpeg's -headers. Left empty, every one of
// those hops sends "Lavf/<version>", which this CDN denies with 403 — so the
// failure would look like a dead host rather than a missing header.
func TestAnimeOnsenStreamCarriesTheHeadersTheCDNDemands(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"abc": 5}})
	s, err := f.provider().Watch("abc", "2", "", "1080")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if s.Referer != animeOnsenReferer {
		t.Errorf("Stream.Referer = %q, want %q", s.Referer, animeOnsenReferer)
	}
	if s.UserAgent == "" || strings.Contains(s.UserAgent, "Lavf") {
		t.Errorf("Stream.UserAgent = %q; without a non-Lavf UA every player and ffmpeg hop 403s on the manifest", s.UserAgent)
	}
	if !strings.HasSuffix(s.URL, "/abc/2/manifest.mpd") {
		t.Errorf("Stream.URL = %q, want the episode's manifest", s.URL)
	}
	if s.Deobfuscate {
		t.Error("Stream.Deobfuscate = true; the fake-PNG HLS proxy rewrites m3u8 playlists and would corrupt a DASH manifest")
	}
}

// A requested quality the source does not have is not echoed back.
//
// There is exactly one Representation (avc1 1280x720 + mp4a), so --quality has
// nothing to select. Reporting the asked-for 1080 would put a number in
// history, in the JSON envelope and on screen that no byte of the stream
// supports.
func TestAnimeOnsenWatchReportsTheQualityItHasRatherThanTheOneAsked(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"abc": 5}})
	for _, asked := range []string{"1080", "best", "360", ""} {
		s, err := f.provider().Watch("abc", "1", "", asked)
		if err != nil {
			t.Fatalf("Watch(quality=%q): %v", asked, err)
		}
		if s.Quality != "720" {
			t.Errorf("Watch(quality=%q).Quality = %q, want 720: the manifest has one 720p rendition and no ladder", asked, s.Quality)
		}
	}
}

// A catalogue miss is ErrNoResults, not a transport failure.
//
// cmd/find.go turns the difference into an exit code — 2 for "nothing matched",
// 3 for "every source is down" — so a 404 reported as a bare error sends an
// agent to diagnose provider health over a misspelling.
func TestAnimeOnsenSearchReportsACatalogueMissAsNoResults(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{search: map[string]string{"kamui": kamuiSearchBody}})
	_, err := f.provider().Search("zzzqqqxxnonexistent")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("Search(miss) err = %v, want ErrNoResults", err)
	}
	if errors.Is(err, ErrUnrecognisedResponse) {
		t.Fatalf("Search(miss) err = %v, also matches ErrUnrecognisedResponse; the host answered its own 404, so it was reached", err)
	}
}

// A bogus content id — which the CDN answers 404 for at every episode — is a
// catalogue miss too, and must not poison the fallback chain with an error
// that reads as "this provider is down".
func TestAnimeOnsenEpisodesReportsABogusContentIDAsNoResults(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"real": 3}})
	_, err := f.provider().GetEpisodes("ZZZZZZZZZZZZZZZZ", "ZZZZZZZZZZZZZZZZ")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("GetEpisodes(bogus id) err = %v, want ErrNoResults", err)
	}
}

// Every episode reported answered 200, and the one after the last did not.
//
// The counts span the shapes the probe's two phases take: fewer than one wave,
// exactly a power of two (where the bracket's own probe is the boundary), an
// ordinary cour, and a count that needs several narrowing waves.
func TestAnimeOnsenEpisodesReportsExactlyTheEpisodesThatAnswered(t *testing.T) {
	for _, want := range []int{1, 8, 12, 13, 26, 100, 1097} {
		t.Run(strconv.Itoa(want), func(t *testing.T) {
			f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": want}})
			eps, err := f.provider().GetEpisodes("x", "x")
			if err != nil {
				t.Fatalf("GetEpisodes: %v", err)
			}
			if len(eps) != want {
				t.Fatalf("GetEpisodes returned %d episodes, want %d", len(eps), want)
			}
			for i, e := range eps {
				if e.Number != i+1 || e.ID != strconv.Itoa(i+1) {
					t.Fatalf("episode %d = %+v, want Number/ID %d", i, e, i+1)
				}
			}
		})
	}
}

// The probe terminates, and cheaply, against a host that answers 200 to
// everything.
//
// This is the input the ceiling exists for: without one the walk would widen
// forever, and a serial version of it would be a round trip per episode. The
// request bound is what makes "bounded" testable — an unbounded probe of a
// 4096-episode ceiling would show up here as thousands of requests, not as a
// hang.
func TestAnimeOnsenEpisodeProbeIsBoundedAgainstAHostThatAnswersEverything(t *testing.T) {
	// -1 means "every episode exists" in the fake.
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": -1}})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	// Written out, not read from animeOnsenMaxEpisodes: a test comparing
	// against the same constant the code uses passes for any ceiling,
	// including none at all.
	const wantCeiling = 4096
	if n != wantCeiling {
		t.Fatalf("episodeCount = %d, want the ceiling %d: nothing above it was probed, so nothing above it may be claimed", n, wantCeiling)
	}
	if got := len(f.requests()); got > 24 {
		t.Fatalf("the probe made %d requests to reach the ceiling of %d; it is walking, not searching", got, wantCeiling)
	}
}

// The probe fans out. A serial walk is a round trip per probe against a 30 s
// HTTP timeout, which is what this repo's agent-facing commands may not be
// left waiting on (cmd/multisearch.go races the search chain for the same
// reason).
func TestAnimeOnsenEpisodeProbeRunsItsHEADsInParallel(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 26}})
	if _, err := f.provider().episodeCount("x"); err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	if peak := f.peakConcurrency(); peak < 2 {
		t.Fatalf("peak concurrent HEADs = %d; the probe is serial, so its cost is one round trip per episode probed", peak)
	}
	// Two waves for a 26-episode series is the shape the width is chosen for.
	// Counting round trips directly is not possible from here; the request
	// count is the proxy, and a serial walk would be ~10 and a per-episode one
	// ~27.
	if got := len(f.requests()); got > 3*animeOnsenProbeWidth {
		t.Fatalf("the probe made %d requests for a 26-episode series; that is more waves than the search needs", got)
	}
}

// A 403 is not "no such episode".
//
// This is the input that would violate the guarantee: a host answering 403 for
// every episode above 3 — which is exactly what a rejected Referer or
// User-Agent looks like on this CDN — must not be read as a 3-episode series.
// Reporting the truncated list would be the fabricated-episode-list failure of
// #61 in the other direction, and nothing downstream could detect it.
func TestAnimeOnsenEpisodeProbeDoesNotReadA403AsTheEndOfTheSeries(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:    map[string]int{"x": -1},
		forbidAbove: 3,
	})
	n, err := f.provider().episodeCount("x")
	if err == nil {
		t.Fatalf("episodeCount = %d with nil error; a 403 above episode 3 was read as a 3-episode series", n)
	}
	if errors.Is(err, ErrNoResults) {
		t.Fatalf("episodeCount err = %v, which says the catalogue has nothing; a 403 is an access failure and must not be reported as an empty catalogue", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("episodeCount err = %v; it should name the status so a header regression is diagnosable", err)
	}
}

// The probe gives up on its own budget rather than running against the HTTP
// client's 30 s timeout once per wave.
func TestAnimeOnsenEpisodeProbeGivesUpWhenItsBudgetExpires(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": -1}})
	p := f.provider()
	// A clock that jumps a minute per reading, so the first budget check after
	// the first wave is already past the deadline.
	var n int
	p.now = func() time.Time {
		n++
		return time.Unix(0, 0).Add(time.Duration(n) * time.Minute)
	}
	_, err := p.episodeCount("x")
	if err == nil {
		t.Fatal("episodeCount succeeded past its budget; the probe has no deadline of its own")
	}
	if !strings.Contains(err.Error(), "probe exceeded") {
		t.Fatalf("episodeCount err = %v, want one naming the exceeded probe budget", err)
	}
}

// Seasons are separate catalogue entries here, so season 2 of a content id is
// not a thing that exists and must be refused rather than silently played as
// season 1.
func TestAnimeOnsenWatchRefusesASecondSeason(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	if _, err := f.provider().Watch("x", "x:2:1", "", "1080"); err == nil {
		t.Fatal("Watch(season 2) succeeded; on this source a second season is a different content id")
	}
}

// An episode past the end is absent, and absent is an error for Watch — but
// not the kind that claims the provider is broken.
func TestAnimeOnsenWatchRefusesAnEpisodePastTheEnd(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	s, err := f.provider().Watch("x", "13", "", "1080")
	if err == nil {
		t.Fatalf("Watch(episode 13 of 12) returned %+v, want an error", s)
	}
}

// Which of a row's two titles is reported, and why it depends on the query.
//
// resolver.Matches admits a candidate only when its title reduces to the same
// normalized key as the ref's, and cmd.probeSeasons searches the provider with
// req.Title itself — so the title a row reports decides whether `episodes`
// accepts this provider's answer at all. The English title is the one foreign
// catalogues use, so it is the default; the romaji one is reported when it is
// the spelling the caller actually asked about.
//
// The last case is the one that keeps this honest: the provider must not just
// echo the query back. A caller asking about a different show has to be told
// about this one under a name that does not match, so the gate refuses it.
func TestAnimeOnsenTitlePrefersEnglishButAnswersUnderTheSpellingAsked(t *testing.T) {
	row := animeOnsenHit{
		ContentID:    "cvYyOlmbfFWvJYWG",
		ContentTitle: "Ushiro no Shoumen Kamui-san",
		TitleEN:      "KAMUI: He's Behind You",
	}
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"KAMUI: He's Behind You", "KAMUI: He's Behind You"},
		{"kamui: he's behind you", "KAMUI: He's Behind You"},
		{"Ushiro no Shoumen Kamui-san", "Ushiro no Shoumen Kamui-san"},
		{"ushiro no shoumen kamui san", "Ushiro no Shoumen Kamui-san"},
		{"kamui", "KAMUI: He's Behind You"},
		{"Ninja Kamui", "KAMUI: He's Behind You"},
	} {
		if got := animeOnsenTitle(tc.query, row); got != tc.want {
			t.Errorf("animeOnsenTitle(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}

	// A row with only one title reports that one whatever was asked.
	only := animeOnsenHit{ContentID: "a", ContentTitle: "Sousou no Frieren"}
	if got := animeOnsenTitle("Frieren", only); got != "Sousou no Frieren" {
		t.Errorf("animeOnsenTitle with no english title = %q, want the romaji one", got)
	}
}

// Search reports TV with no fabricated year or duration. The response carries
// none, and a Year nobody sent feeds resolver.candidateScore a signal that
// looks like evidence.
func TestAnimeOnsenSearchClaimsNothingTheResponseDidNotCarry(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{search: map[string]string{"kamui": kamuiSearchBody}})
	res, err := f.provider().Search("kamui")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("Search returned %d rows, want 1", len(res))
	}
	r := res[0]
	if r.ID != "cvYyOlmbfFWvJYWG" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Type != media.TV {
		t.Errorf("Type = %v, want TV", r.Type)
	}
	if r.Year != "" || r.Duration != "" || r.Episodes != 0 || r.Seasons != 0 {
		t.Errorf("row = %+v; the search response carries no year, duration or counts, so none may be claimed", r)
	}
}
