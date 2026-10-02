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

	// lie404 is how many times each episode number answers 404 before it
	// starts telling the truth. It models the one thing the rest of this fake
	// cannot express: a 404 that is not the truth. The live CDN sheds a burst
	// of concurrent HEADs that way — the manifest exists, and a request for it
	// inside the burst is refused anyway — and a 404 taken at face value then
	// ends the series early. Counted per episode and decremented as requests
	// arrive, so the lie is deterministic rather than timing-dependent.
	lie404 map[int]int

	// throttle is how many times each episode number answers 429 before it
	// answers truthfully, and throttleAbove makes every episode above it
	// answer 429 forever (a negative value throttles every episode, episode 1
	// included). Together they model the live CDN's bot mitigation:
	// a request that misses the edge cache goes to origin, and origin sheds
	// concurrent misses with 429. A 429 read as "no such episode" ends the
	// series early; read as a transport failure it loses the whole provider
	// to the fallback chain. Neither is what the status means.
	throttle      map[int]int
	throttleAbove int
	// throttleRetryAfter, when non-empty, is sent as the Retry-After header
	// with every 429. The live CDN sends none, so the default models that.
	throttleRetryAfter string

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
	// inflight is how many requests were being served when this one arrived,
	// this one included. It is what makes "the shed probe was re-asked on its
	// own" a measurable claim rather than a comment: 1 means solitary.
	inflight int
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
	f.reqs[len(f.reqs)-1].inflight = f.inFlight
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
		if f.throttleAbove != 0 && (f.throttleAbove < 0 || n > f.throttleAbove) {
			f.throttleResponse(w)
			return
		}
		if f.throttle != nil {
			f.mu.Lock()
			left := f.throttle[n]
			if left > 0 {
				f.throttle[n] = left - 1
			}
			f.mu.Unlock()
			if left > 0 {
				f.throttleResponse(w)
				return
			}
		}
		if f.lie404 != nil {
			f.mu.Lock()
			left := f.lie404[n]
			if left > 0 {
				f.lie404[n] = left - 1
			}
			f.mu.Unlock()
			if left > 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
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

func (f *animeOnsenFake) throttleResponse(w http.ResponseWriter) {
	if f.throttleRetryAfter != "" {
		w.Header().Set("Retry-After", f.throttleRetryAfter)
	}
	w.WriteHeader(http.StatusTooManyRequests)
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
// The matrix is the boundaries the algorithm's own structure creates, not a
// sample of plausible season lengths. The bracket probes powers of two, so
// every power of two and both of its neighbours is a distinct case: the
// boundary falling on a bracket probe, just below one, and just above one.
// Then an ordinary cour, a two-cour run, and counts needing several narrowing
// waves. 12 is the live-verified case — `episodes` reported 10 of KAMUI's 12 —
// and it is in here because of that, not because 12 is a common length.
//
// The expected count is a literal in the table, never computed: a test that
// derives what to expect with the same arithmetic the implementation uses
// proves only that the arithmetic is self-consistent, and both would move
// together under a mutation.
func TestAnimeOnsenEpisodesReportsExactlyTheEpisodesThatAnswered(t *testing.T) {
	for _, want := range []int{
		1, 2, 3, 7, 8, 9, 12, 13, 15, 16, 17, 24, 26, 31, 32, 33,
		63, 64, 65, 100, 127, 128, 129, 1097,
	} {
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
	// The ceiling is not a boundary anybody measured, so the count comes back
	// with the sentinel that says so. Reporting it as a finished enumeration
	// is the dishonesty ErrIncompleteEpisodeList exists to stop.
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount at the ceiling returned err = %v, want one wrapping ErrIncompleteEpisodeList", err)
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

// A 404 inside a wave of concurrent HEADs does not end the series.
//
// This is the live failure: `episodes` reported 10 of KAMUI's 12 episodes,
// three runs in a row, while a HEAD of episode 11 and of episode 12 sent on
// its own answered 200 and episode 13 answered 404. Nothing but a 404 makes
// this probe stop, so the wave that asked for 9..15 at once was told "no" about
// an episode that exists — and episodes 11 and 12 were then unreachable
// through lobster.
//
// The count is a measurement, so a boundary has to be confirmed by a probe
// sent on its own before it is reported, which is the condition under which a
// hand-run curl got the truth. Each case below is the same true count, 12,
// reached through a different lie.
func TestAnimeOnsenEpisodeProbeDoesNotTakeABurst404AsTheEndOfTheSeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		lie  map[int]int
	}{
		// The live shape: the narrowing wave is told the last two episodes are
		// absent, and both of its neighbours agree, so nothing looks wrong.
		{"the last two episodes are refused once", map[int]int{11: 1, 12: 1}},
		// A present episode appears above an absent one, which the contiguity
		// clamp resolves downwards — the safe direction, and still wrong.
		{"one episode inside the series is refused once", map[int]int{11: 1}},
		// The whole narrowing wave is shed, which puts the reported count back
		// at the bracket's own boundary.
		{"the whole narrowing wave is refused once", map[int]int{9: 1, 10: 1, 11: 1, 12: 1}},
		// The bracket's upper probe is shed, so the bracket closes below the
		// end of the series instead of above it.
		{"the bracket's own probe is refused once", map[int]int{8: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAnimeOnsenFake(t, &animeOnsenFake{
				episodes: map[string]int{"x": 12},
				lie404:   tc.lie,
			})
			n, err := f.provider().episodeCount("x")
			if err != nil {
				t.Fatalf("episodeCount: %v", err)
			}
			if n != 12 {
				t.Fatalf("episodeCount = %d, want 12", n)
			}
		})
	}
}

// An enumeration that stopped short comes back as a list *and* an error, so
// the caller can keep the measured prefix and still know it is a prefix.
//
// The two halves are asserted separately on purpose. Returning the error and
// dropping the list would cost `episodes` an answer it has; returning the list
// and dropping the error is the bug — a partial list presented as the whole
// series, which no caller can detect.
func TestAnimeOnsenGetEpisodesReturnsThePrefixItMeasuredAndSaysItIsOne(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": -1}})
	eps, err := f.provider().GetEpisodes("x", "x")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("GetEpisodes err = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	// 4096 written out rather than read from animeOnsenMaxEpisodes, for the
	// reason the ceiling test gives.
	if len(eps) != 4096 {
		t.Fatalf("GetEpisodes returned %d episodes alongside the error, want the 4096 it measured", len(eps))
	}
	// Not ErrNoResults: the catalogue does have this show, and a caller asking
	// that question must not be told otherwise.
	if errors.Is(err, ErrNoResults) {
		t.Fatalf("GetEpisodes err = %v, which errors.Is reads as ErrNoResults", err)
	}
}

// A confirmed boundary carries no incompleteness error. The sentinel has to
// mean something, and a complete list that reports itself as possibly short
// trains every caller to ignore the warning.
func TestAnimeOnsenGetEpisodesReportsAConfirmedBoundaryAsComplete(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	eps, err := f.provider().GetEpisodes("x", "x")
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(eps) != 12 {
		t.Fatalf("GetEpisodes returned %d episodes, want 12", len(eps))
	}
}

// When the boundary cannot be confirmed within the round bound, the count
// comes back with the sentinel rather than as a finished enumeration.
//
// This is the confirmation loop's own bound. Without it the search would walk
// forward a round at a time for as long as the budget allowed and then report
// a number with no confirmed boundary behind it at all — and report it as
// truth.
//
// The round bound is shrunk rather than the fixture made to lie four times in
// the right places: what is under test is that exhausting the bound is
// reported, not how many rounds a particular host takes to exhaust it.
func TestAnimeOnsenEpisodeProbeSaysSoWhenItCannotConfirmABoundary(t *testing.T) {
	old := animeOnsenConfirmRounds
	animeOnsenConfirmRounds = 1
	t.Cleanup(func() { animeOnsenConfirmRounds = old })

	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		lie404:   map[int]int{11: 1},
	})
	n, err := f.provider().episodeCount("x")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount = %d, err = %v; want an error wrapping ErrIncompleteEpisodeList", n, err)
	}
	// Still a measured episode: the prefix reported is one the CDN served.
	if n != 11 {
		t.Fatalf("episodeCount = %d, want the 11 the confirmation probe proved present", n)
	}
}

// Episode 1 answering 404 inside the first wave is not "this source does not
// have the show" until a solitary probe agrees.
//
// ErrNoResults from here is load-bearing — cmd/multisearch and the fallback
// chain read it as "reached, and the catalogue does not have this" — so a shed
// burst would retire a series lobster can stream, under an error that tells
// the caller not to bother retrying.
func TestAnimeOnsenEpisodeProbeDoesNotReadABurst404OnEpisodeOneAsAnEmptyCatalogue(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		lie404:   map[int]int{1: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12", n)
	}
}

// A content id the CDN really has nothing for is still ErrNoResults, and the
// confirmation probe does not turn that into something retryable.
func TestAnimeOnsenEpisodeProbeStillReportsAGenuinelyEmptyCatalogueAsNoResults(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	_, err := f.provider().episodeCount("nope")
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("episodeCount for an unknown id = %v, want ErrNoResults", err)
	}
	if errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount for an unknown id = %v, which reads as an incomplete list", err)
	}
}

// A re-run after a disproved 404 resumes above what it has already proved,
// instead of measuring the series again from episode 1.
//
// The count is the same either way, which is why this needs its own
// assertion: the difference is entirely in cost, and the whole reason this
// probe brackets rather than walks is that an agent-facing command may not be
// left waiting on round trips. Episode 1 is the crisp witness — nothing above
// the floor ever needs to be asked about twice.
func TestAnimeOnsenEpisodeProbeResumesAboveTheFloorAfterADisproved404(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		lie404:   map[int]int{11: 1, 12: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12", n)
	}
	ep1 := 0
	for _, r := range f.requests() {
		if r.path == "/video/mp4-dash/x/1/manifest.mpd" {
			ep1++
		}
	}
	if ep1 != 1 {
		t.Fatalf("episode 1 was probed %d times; a re-run is bracketing from 1 again rather than from the episode it proved present", ep1)
	}
}

// --- HTTP 429: the CDN sheds concurrent cache misses -----------------------
//
// Measured live on 2026-10-02 against cdn.animeonsen.xyz, KAMUI
// (cvYyOlmbfFWvJYWG, 12 episodes). Eight concurrent HEADs of episodes
// 1,2,4,8,16,32,64,128 — exactly the probe's first bracket wave:
//
//	HTTP/2, one multiplexed connection   1,2,4,8 -> 200 (cf-cache-status HIT)
//	                                     16,32   -> 429 (cf-cache-status EXPIRED)
//	                                     64,128  -> 404
//	HTTP/1.1, --no-keepalive, 8 conns    1,2,4   -> 200
//	                                     8,32,64 -> 429
//	                                     16,128  -> 404
//	strictly sequential, same 8 URLs     8/8 correct, zero 429
//
// So it is neither the User-Agent (a Go-http-client/1.1 UA gets 403, not 429,
// and the probe sends a browser UA anyway) nor HTTP/2 multiplexing. It is
// concurrency against origin: only cache misses reach origin, and origin sheds
// them. Retrying the shed request on its own returned the truth every time.
//
// No 429 carried a Retry-After header, so the backoff cannot depend on one.

// animeOnsenNoSleep replaces the probe's backoff sleep with a recorder, so the
// retry tests assert on the waits the policy chose instead of spending them.
func animeOnsenNoSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var got []time.Duration
	var mu sync.Mutex
	prev := animeOnsenSleep
	animeOnsenSleep = func(d time.Duration) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	}
	t.Cleanup(func() { animeOnsenSleep = prev })
	return &got
}

// TestAnimeOnsenEpisodeProbeRetriesAThrottledProbe is the live bug: episode 16
// is the bracket's first cache miss for a 12-episode series, origin sheds it
// with 429, and the whole enumeration failed — so `episodes` fell through to a
// provider that reported 10 episodes and cannot stream any of them.
func TestAnimeOnsenEpisodeProbeRetriesAThrottledProbe(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		throttle: map[int]int{16: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v (a 429 is not a failure of the provider; it is a request to ask again)", err)
	}
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12", n)
	}
}

// TestAnimeOnsenThrottledProbeIsNotAnAbsentEpisode pins the other reading a
// 429 must never get: the status says "ask again", not "no such episode", and
// taking it for the end of the series is how a 12-episode show becomes a
// 15-episode one's worth of silence.
func TestAnimeOnsenThrottledProbeIsNotAnAbsentEpisode(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		throttle: map[int]int{11: 1, 12: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12 (a 429 on episodes 11 and 12 was read as the end of the series)", n)
	}
}

// TestAnimeOnsenThrottledProbeIsNotAMissingShow is the worst reading, and the
// one that poisons the chain: ErrNoResults means "animeonsen does not have
// this", and a caller cannot tell that from "the CDN would not answer".
func TestAnimeOnsenThrottledProbeIsNotAMissingShow(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 12},
		throttleAbove: -1, // every episode, episode 1 included
	})
	n, err := f.provider().episodeCount("x")
	if err == nil {
		t.Fatalf("episodeCount = %d with no error; nothing was measured and saying so is the only honest answer", n)
	}
	if errors.Is(err, ErrNoResults) {
		t.Fatalf("episodeCount error wraps ErrNoResults (%v); a throttled probe is not an empty catalogue", err)
	}
}

// TestAnimeOnsenPersistentThrottleYieldsThePrefixAndSaysSo covers property 3:
// enumeration that cannot be completed reports what it measured and flags it,
// rather than erroring and handing the question to a provider whose answer is
// shorter and unplayable.
func TestAnimeOnsenPersistentThrottleYieldsThePrefixAndSaysSo(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 12},
		throttleAbove: 12, // every miss is shed, forever
	})
	p := f.provider()
	n, err := p.episodeCount("x")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount error = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	// 8 is the measured prefix: the bracket wave proves 1,2,4,8 present and
	// every probe above 12 is shed, so 8 is the highest episode confirmed.
	if n != 8 {
		t.Fatalf("episodeCount = %d, want the measured prefix 8", n)
	}
	eps, err := p.GetEpisodes("x", "1")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("GetEpisodes error = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	if len(eps) != 8 {
		t.Fatalf("GetEpisodes returned %d episodes alongside the sentinel, want 8", len(eps))
	}
}

// TestAnimeOnsenRetryHonoursRetryAfter checks the one input the policy must
// defer to when it is present. Live 429s carried none, so this is the
// contract rather than the observed case.
func TestAnimeOnsenRetryHonoursRetryAfter(t *testing.T) {
	waits := animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:           map[string]int{"x": 3},
		throttle:           map[int]int{1: 1},
		throttleRetryAfter: "1",
	})
	if _, err := f.provider().episodeAvailable("x", 1); err != nil {
		t.Fatalf("episodeAvailable: %v", err)
	}
	if len(*waits) != 1 {
		t.Fatalf("the probe slept %d times for one 429, want 1: %v", len(*waits), *waits)
	}
	// Retry-After: 1 is one second. Written out rather than read back from the
	// policy's own constants, which would move with a mutant.
	if (*waits)[0] != time.Second {
		t.Fatalf("waited %v after Retry-After: 1, want 1s", (*waits)[0])
	}
}

// TestAnimeOnsenRetryBackoffIsBoundedWithoutRetryAfter pins the default the
// live CDN actually makes it use, and that it is bounded: the whole retry
// policy has to fit inside cmd.episodesFallbackTimeout (5s) or the fix just
// moves the silent fallback from a 429 to a deadline.
func TestAnimeOnsenRetryBackoffIsBoundedWithoutRetryAfter(t *testing.T) {
	waits := animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 3},
		throttleAbove: -1,
	})
	if _, err := f.provider().episodeAvailable("x", 1); err == nil {
		t.Fatal("episodeAvailable succeeded against a host that answers 429 to everything")
	}
	// Measured literals, not the policy's constants: three attempts means two
	// waits, 150ms then 300ms, 450ms spent in total on one probe.
	want := []time.Duration{150 * time.Millisecond, 300 * time.Millisecond}
	if len(*waits) != len(want) {
		t.Fatalf("probe slept %v, want %v", *waits, want)
	}
	total := time.Duration(0)
	for i, w := range *waits {
		if w != want[i] {
			t.Fatalf("probe slept %v, want %v", *waits, want)
		}
		total += w
	}
	if total != 450*time.Millisecond {
		t.Fatalf("one probe spent %v retrying, want 450ms", total)
	}
}

// TestAnimeOnsenRetryStopsAtTheBudget stops the retry policy from spending
// time the caller does not have. cmd/episodes abandons the call at 5s; a
// backoff that keeps sleeping past the probe's own deadline turns a recoverable
// 429 into the same silent fallback by a different route.
func TestAnimeOnsenRetryStopsAtTheBudget(t *testing.T) {
	waits := animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 12},
		throttleAbove: -1,
	})
	p := f.provider()
	// A clock that is already past the budget the moment the probe consults
	// it: every retry must be refused rather than slept through.
	calls := 0
	base := time.Now()
	p.now = func() time.Time {
		calls++
		if calls <= 1 {
			return base
		}
		return base.Add(animeOnsenProbeBudget + time.Second)
	}
	if _, err := p.episodeCount("x"); err == nil {
		t.Fatal("episodeCount succeeded past its budget")
	}
	if n := len(*waits); n != 0 {
		t.Fatalf("the probe slept %d times with no budget left: %v", n, *waits)
	}
}

// TestAnimeOnsenProbeReusesConfirmedEpisodesWithinARun is property 4: fewer
// requests, without trading any correctness for them. Only 200s are reused —
// a 404 has to stay re-askable, because re-asking it on its own is the whole
// mechanism that catches a shed burst.
func TestAnimeOnsenProbeReusesConfirmedEpisodesWithinARun(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	p := f.provider()
	if _, err := p.episodeCount("x"); err != nil {
		t.Fatalf("first episodeCount: %v", err)
	}
	first := len(f.requests())
	if _, err := p.episodeCount("x"); err != nil {
		t.Fatalf("second episodeCount: %v", err)
	}
	second := len(f.requests()) - first
	// Measured literals. The first enumeration of a 12-episode series costs
	// 18 requests; the second costs 10, because the eleven episodes the first
	// one watched answer 200 are not asked again while every 404 still is.
	// Measured literals, written out rather than compared against each other
	// or against the probe's own constants. A 12-episode series costs 16
	// requests to enumerate cold and 8 to enumerate again, because the eight
	// episodes the first pass watched answer 200 are not asked a second time
	// while every 404 still is.
	if first != 16 {
		t.Fatalf("first enumeration cost %d requests, want 16", first)
	}
	if second != 8 {
		t.Fatalf("second enumeration cost %d requests, want 8", second)
	}
	// And a known-present episode costs nothing at all to re-check, which is
	// the path resolver takes: GetEpisodes, then Watch on one of them.
	before := len(f.requests())
	if _, err := p.Watch("x", "1", "", ""); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if n := len(f.requests()) - before; n != 0 {
		t.Fatalf("Watch re-probed episode 1 with %d requests after enumeration already proved it present", n)
	}
}

// TestAnimeOnsenWatchRecoversFromAThrottledProbe: a 429 on the availability
// check must not become "no episode N", which is what `play` would have said.
func TestAnimeOnsenWatchRecoversFromAThrottledProbe(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		throttle: map[int]int{11: 2},
	})
	st, err := f.provider().Watch("x", "11", "", "")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !strings.HasSuffix(st.URL, "/x/11/manifest.mpd") {
		t.Fatalf("Watch URL = %q", st.URL)
	}
}

// TestAnimeOnsenProbeCapsItsInFlightRequests addresses the cause rather than
// the symptom: the 429s came from concurrent cache misses arriving at origin
// together, and the shedding scaled with how many were in flight. The probe
// stays parallel — it has a 5s budget to meet — but not the whole wave at once.
func TestAnimeOnsenProbeCapsItsInFlightRequests(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	p := f.provider()
	if _, err := p.episodeCount("x"); err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	// Still parallel: a serialised walk would peak at one.
	if got := f.peakConcurrency(); got < 2 {
		t.Fatalf("peak concurrency %d; the wave is serialised and cannot meet its budget", got)
	}
	// But not all eight at once. 8 is written out rather than read from
	// animeOnsenProbeWidth, so shrinking the constant cannot satisfy this.
	if got := f.peakConcurrency(); got >= 8 {
		t.Fatalf("peak concurrency %d; the whole wave is in flight at once, which is what origin sheds", got)
	}
}

// TestAnimeOnsenProbeBudgetFitsInsideItsCallersDeadline pins the relationship
// the budget exists to hold, which is the half a value on its own cannot show.
//
// cmd.episodesFallbackTimeout is 5s and abandons the episode call at it. A
// probe budget above that can never produce this provider's own answer on that
// path — the caller gives up first and asks the fallback chain, which is the
// silent downgrade the rest of this file is about. The 5s is written out
// rather than imported, because internal/provider cannot import cmd and
// because a literal is what makes this a claim about the caller rather than a
// restatement of the constant under test.
func TestAnimeOnsenProbeBudgetFitsInsideItsCallersDeadline(t *testing.T) {
	const episodesFallbackTimeout = 5 * time.Second
	if animeOnsenProbeBudget >= episodesFallbackTimeout {
		t.Fatalf("animeOnsenProbeBudget is %s, which is not below the %s cmd/episodes.go abandons the call at; the probe can never answer on that path",
			animeOnsenProbeBudget, episodesFallbackTimeout)
	}
	// And it has to leave the probe room to notice and return, not just to be
	// nominally smaller.
	if margin := episodesFallbackTimeout - animeOnsenProbeBudget; margin < 250*time.Millisecond {
		t.Fatalf("animeOnsenProbeBudget leaves only %s to return in; want at least 250ms", margin)
	}
}

// TestAnimeOnsenReAsksAShedProbeOnItsOwn is the measured recovery condition,
// asserted as a condition and not as a comment.
//
// Every probe the live CDN shed answered truthfully when it was re-sent by
// itself, and re-sending it while the rest of its wave is still in flight is
// asking again under the circumstances that caused the refusal. So the wave
// does not retry; it collects what it lost and re-asks those one at a time.
//
// The fixture sheds every probe of the first wave exactly once, which is the
// case the narrowing cannot paper over: without the re-ask the wave teaches
// nothing, the search gives up with nothing measured, and `episodes` is back
// to asking the chain.
func TestAnimeOnsenReAsksAShedProbeOnItsOwn(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		throttle: map[int]int{1: 1, 2: 1, 4: 1, 8: 1, 16: 1, 32: 1, 64: 1, 128: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil {
		t.Fatalf("episodeCount: %v", err)
	}
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12", n)
	}
	// Every repeat of a path is a re-ask, and a re-ask must have been alone on
	// the wire. 1 is written out: it is the claim, not a constant the code
	// also reads.
	seen := map[string]int{}
	repeats := 0
	for _, r := range f.requests() {
		seen[r.path]++
		if seen[r.path] > 1 {
			repeats++
			if r.inflight != 1 {
				t.Fatalf("a re-ask of %s arrived with %d requests in flight, want 1", r.path, r.inflight)
			}
		}
	}
	if repeats < 8 {
		t.Fatalf("only %d probes were re-asked; the fixture shed 8 and the re-ask pass is not running", repeats)
	}
}

// TestAnimeOnsenRetryRefusesToSleepPastItsDeadline aims straight at the
// deadline check inside the retry, which the budget test above cannot reach:
// there, the budget is already spent when searchBoundary first looks, so no
// probe is ever sent and no backoff is ever considered.
func TestAnimeOnsenRetryRefusesToSleepPastItsDeadline(t *testing.T) {
	waits := animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 12},
		throttleAbove: -1,
	})
	p := f.provider()
	// A deadline already in the past. The first request still goes out — the
	// probe does not pre-check — and the 429 it gets back must not be slept on.
	if _, err := p.probeEpisodeWithRetries("x", 1, p.now().Add(-time.Second), animeOnsenProbeRetries); err == nil {
		t.Fatal("probeEpisodeWithRetries succeeded against a host that answers 429 to everything")
	}
	if n := len(*waits); n != 0 {
		t.Fatalf("the probe slept %d times with a deadline already past: %v", n, *waits)
	}
	// Exactly one request: the first attempt, and no retry it had no time for.
	if got := len(f.requests()); got != 1 {
		t.Fatalf("the probe made %d requests with no budget to retry, want 1", got)
	}
}

// TestAnimeOnsenShedEpisodeOneProbeIsNotAnEmptyCatalogue reaches the one arm
// that can still answer ErrNoResults.
//
// It needs a wave that learns something while episode 1 itself stays unknown,
// which is a one-episode series whose only episode is shed: everything above
// it genuinely 404s, so the bracket does not give up, and the search arrives
// at the "episode 1 answered absent" arm with a 429 rather than a 404 behind
// it. ErrNoResults there means "animeonsen does not have this show", which the
// fallback chain treats as final — it would retire a series lobster can stream
// over one refused request.
func TestAnimeOnsenShedEpisodeOneProbeIsNotAnEmptyCatalogue(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 1},
		throttle: map[int]int{1: 99},
	})
	n, err := f.provider().episodeCount("x")
	if err == nil {
		t.Fatalf("episodeCount = %d with no error; episode 1 never answered", n)
	}
	if errors.Is(err, ErrNoResults) {
		t.Fatalf("episodeCount err = %v; it wraps ErrNoResults, which tells the chain animeonsen does not have this show", err)
	}
}
