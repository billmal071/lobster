package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
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
	// throttleAfter names episodes that answer truthfully for their first k
	// requests and are shed with 429 from then on. It is throttle read the
	// other way round, and it exists because the one state neither of the
	// others can express is the one the confirmation probe lives in: an
	// episode has to answer the search's wave with a real 404 and then shed
	// the solitary HEAD that re-checks that 404. A fixture that sheds from the
	// first request never lets the search locate a boundary at all, so the
	// confirmation arm is never reached and a test aimed at it silently scores
	// against the wave instead.
	throttleAfter map[int]int

	// stallAfter names the episodes that hold their request open for stallFor
	// before answering, unless the request's own context is cancelled first —
	// in which case nothing is written at all. The value is how many requests
	// answer normally first (0: stall from the very first). stallAll stalls
	// every episode and every request.
	//
	// It is the only knob here that can tell a request bounded by the probe
	// budget from one bounded by the HTTP client's 30 s timeout. Every other
	// fixture answers immediately, so the probe's deadline could only ever be
	// consulted *between* requests and a stalled round trip was
	// indistinguishable from a fast one.
	//
	// Keyed per episode rather than "every episode above N", because a wave
	// probes 1,2,4,8,... — so a threshold low enough to stall the episode
	// under test also stalls the bracket's own upper probes, and the
	// measurement then never reaches the arm the test is aimed at. Counted per
	// request for the same reason: the confirmation probe asks about an
	// episode the search has usually already asked about once, and a fixture
	// that stalls both never gets past the search.
	stallAfter map[int]int
	stallAll   bool
	stallFor   time.Duration

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
		if f.throttleAfter != nil {
			f.mu.Lock()
			left, ok := f.throttleAfter[n]
			if ok && left > 0 {
				f.throttleAfter[n] = left - 1
			}
			f.mu.Unlock()
			if ok && left == 0 {
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
		stall := f.stallAll
		if !stall && f.stallAfter != nil {
			f.mu.Lock()
			left, ok := f.stallAfter[n]
			if ok && left > 0 {
				f.stallAfter[n] = left - 1
			}
			f.mu.Unlock()
			stall = ok && left == 0
		}
		if stall {
			select {
			case <-time.After(f.stallFor):
			case <-r.Context().Done():
				// The client gave up on this request. Answering now would
				// write into a closed response, and a fixture that answered
				// anyway could not distinguish a bounded request from an
				// unbounded one.
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
// folds it into mpv's --user-agent and internal/download folds it into ffmpeg's
// -headers. Left empty, every one of those hops sends "Lavf/<version>", which
// this CDN denies with 403 — so the failure would look like a dead host rather
// than a missing header.
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
	// The powers of two and their neighbours are the bracket's boundaries; the
	// rest are the narrowing's, including the counts where its evenly spaced
	// points land on the answer and the ones where they straddle it. Every
	// count from 1 to 300 was run against this fixture when the narrowing's
	// width changed (0 mismatches); the table is the subset worth paying for
	// on every run.
	for _, want := range []int{
		1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 20,
		24, 25, 26, 31, 32, 33, 48, 50, 63, 64, 65, 100, 127, 128, 129, 1097,
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
	// And it is the strong flag. The round bound is only spent by a
	// confirmation probe *disproving* the boundary the search found, so what
	// this reports is a count with no located end behind it at all — the
	// opposite of a boundary that was found and merely not re-checked.
	if errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v; every boundary this measurement found was disproved, so there is no unconfirmed end here, only a short list", err)
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
	// Measured literals, written out rather than compared against each other
	// or against the probe's own constants. A 12-episode series costs 13
	// requests to enumerate cold and 6 to enumerate again, because the
	// episodes the first pass watched answer 200 are not asked a second time
	// while every 404 still is.
	if first != 13 {
		t.Fatalf("first enumeration cost %d requests, want 13", first)
	}
	if second != 6 {
		t.Fatalf("second enumeration cost %d requests, want 6", second)
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

// TestAnimeOnsenNarrowingProbesNoWiderThanItCanHaveInFlight pins the
// narrowing's wave width against the thing that makes a wide wave expensive.
//
// Round trips are the cost here, not requests: a wave of W points goes out
// ceil(W/animeOnsenProbeInFlight) tranches at a time, and it only shrinks the
// unknown gap by a factor of W+1. Widening past the in-flight cap therefore
// buys less than the extra tranche costs — and every point the wave spends
// above the real boundary is spent for nothing, because the boundary is fixed
// by the *lowest* absent probe.
//
// Asserted as the exact multiset of episodes a 12-episode enumeration asks
// about, measured and written out. A set is what pins the schedule; a count
// alone would pass for a wave of four taken twice, and comparing against
// animeOnsenNarrowWidth would pass for any width including the old one.
func TestAnimeOnsenNarrowingProbesNoWiderThanItCanHaveInFlight(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	n, err := f.provider().episodeCount("x")
	if err != nil || n != 12 {
		t.Fatalf("episodeCount = %d, %v; want 12 and no error", n, err)
	}
	var asked []int
	for _, r := range f.requests() {
		if m := animeOnsenManifestPath.FindStringSubmatch(r.path); m != nil {
			k, _ := strconv.Atoi(m[2])
			asked = append(asked, k)
		}
	}
	sort.Ints(asked)
	// The bracket (1,2,4,8,16,32,64,128), one narrowing wave of four points
	// evenly spaced in the gap 9..15 (9,11,12,14), and the single point that
	// is left (13) — which, being alone on the wire, is itself the boundary
	// measurement and needs no re-check after it.
	want := []int{1, 2, 4, 8, 9, 11, 12, 13, 14, 16, 32, 64, 128}
	if fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Fatalf("the probe asked about %v, want %v", asked, want)
	}
	// Spelled out because it is the saving, not a restatement: a wave that
	// probed the whole gap would have asked about these two, and the answers
	// could not have changed the boundary.
	for _, never := range []int{10, 15} {
		if slices.Contains(asked, never) {
			t.Fatalf("episode %d was probed; nothing below the lowest absent episode can move the boundary, so the gap is not there to be enumerated", never)
		}
	}
}

// animeOnsenProbesOf counts how many times the probe asked about one episode.
func animeOnsenProbesOf(f *animeOnsenFake, n int) int {
	asked := 0
	for _, r := range f.requests() {
		if strings.HasSuffix(r.path, fmt.Sprintf("/%d/manifest.mpd", n)) {
			asked++
		}
	}
	return asked
}

// The boundary re-check is the last request of the enumeration and always an
// origin cache miss — nobody has ever fetched the manifest after the end of a
// series, so nothing has put it at the edge. On the live CDN that is the ~1s
// round trip, against a 4500ms budget, and spending it is what left healthy
// runs reporting an unconfirmed end on a correct list.
//
// It is only worth spending when the absence it re-checks was observed inside a
// concurrent wave, because that is the observation a shedding origin falsifies.
// When the search closed its last gap with a single request, the absence was
// already measured alone — the same oracle, under the same conditions — and
// asking again learns nothing.
func TestAnimeOnsenABoundaryMeasuredAloneIsNotAskedASecondTime(t *testing.T) {
	// 12 episodes: the bracket leaves the gap 9..15, one narrowing wave of four
	// points settles lo=12/hi=14, and the single remaining point (13) goes out
	// on its own.
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	n, err := f.provider().episodeCount("x")
	if err != nil || n != 12 {
		t.Fatalf("episodeCount = %d, %v; want 12 and no error", n, err)
	}
	if got := animeOnsenProbesOf(f, 13); got != 1 {
		t.Fatalf("episode 13 was probed %d times, want 1: its absence was already measured by a request that was alone on the wire, which is exactly what a second one would establish", got)
	}
}

// And the negative control, which is the half that must not regress: an absence
// measured inside a wave is still re-checked. This is the observation that cost
// two episodes of a twelve-episode series — a burst of eight concurrent HEADs
// answered 404 for a manifest the CDN then served.
func TestAnimeOnsenABoundaryMeasuredInAWaveIsStillAskedAgain(t *testing.T) {
	// One episode, so the bracket itself lands on the boundary: episode 2
	// answers 404 as one of eight requests in flight, and the narrowing never
	// runs.
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 1}})
	n, err := f.provider().episodeCount("x")
	if err != nil || n != 1 {
		t.Fatalf("episodeCount = %d, %v; want 1 and no error", n, err)
	}
	if got := animeOnsenProbesOf(f, 2); got != 2 {
		t.Fatalf("episode 2 was probed %d times, want 2 (the bracket's wave, then the solitary re-check); a 404 seen inside a burst is the observation this provider cannot trust", got)
	}
}

// A probe the wave shed and the serial pass re-asked was also sent on its own,
// so it counts as a measurement made alone. The re-ask pass is the one place
// solitariness comes from something other than the wave's size, and without it
// a shed boundary would be re-checked by a third request that asks what the
// second already answered.
func TestAnimeOnsenABoundaryTheReAskPassMeasuredCountsAsMeasuredAlone(t *testing.T) {
	animeOnsenNoSleep(t)
	// The narrowing's final single-point wave (13) is shed once, so the wave
	// learns nothing and the serial re-ask pass asks again — alone, which is
	// the condition that made that pass the fix in the first place.
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		throttle: map[int]int{13: 1},
	})
	n, err := f.provider().episodeCount("x")
	if err != nil || n != 12 {
		t.Fatalf("episodeCount = %d, %v; want 12 and no error", n, err)
	}
	if got := animeOnsenProbesOf(f, 13); got != 2 {
		t.Fatalf("episode 13 was probed %d times, want 2 (the shed wave, then the solitary re-ask); a third is the re-check of an absence the re-ask already measured alone", got)
	}
}

// The budget case, and the one the split in episode_list_unconfirmed was
// reported on: the search hands back a boundary with nothing left to re-check
// it in. When that boundary was measured alone there is nothing to re-check,
// so the answer is complete and silent rather than flagged.
//
// It is the mirror of TestAnimeOnsenABoundaryHandedBackWithNoBudgetLeftIs
// Unconfirmed, which keeps the flag because its boundary came out of a wave.
func TestAnimeOnsenASolitaryBoundaryIsCompleteEvenWithNoBudgetLeft(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
	p := f.provider()
	// A clock that stays put until the search has made all thirteen of its
	// requests — the bracket's eight, the narrowing's four, and the single
	// point left over — and then jumps past the deadline. It cannot jump
	// earlier: headManifest sizes each request's context from the remaining
	// budget, so a spent clock would cancel the requests the search needs.
	base := time.Now()
	p.now = func() time.Time {
		if len(f.requests()) >= 13 {
			return base.Add(2 * animeOnsenProbeBudget)
		}
		return base
	}
	n, err := p.episodeCount("x")
	if n != 12 {
		t.Fatalf("episodeCount = %d, want 12", n)
	}
	if err != nil {
		t.Fatalf("episodeCount err = %v; episode 13 was already measured absent on its own, so a spent budget costs this answer nothing and there is nothing to flag", err)
	}
	if got := len(f.requests()); got != 13 {
		t.Fatalf("the probe made %d requests, want 13 and no re-check; this test is not reaching the arm between the search and the probe", got)
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
	// A clock that leaves budget for the first request and none for the
	// backoff after it. The request has to go out — the deadline now bounds
	// the request itself, so a deadline already in the past would be refused
	// before the wire and never reach the retry at all — and the 429 it gets
	// back must not be slept on.
	base := time.Now()
	deadline := base.Add(time.Second)
	calls := 0
	p.now = func() time.Time {
		calls++
		if calls <= 1 {
			return base
		}
		return deadline.Add(time.Second)
	}
	if _, err := p.probeEpisodeWithRetries("x", 1, deadline, animeOnsenProbeRetries); err == nil {
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

// TestAnimeOnsenWatchDoesNotNeedACompleteEpisodeListToPlay is the collision
// between this file's two newest features.
//
// The resolver's fallback hands Watch a "showID:season:episode" id
// (resolver.tryStreamProviderFallback), and a movie or a season-less request
// hands it "". Both used to be answered by enumerating the whole series and
// looking the number up — and enumeration now returns its measured prefix
// *together with* ErrIncompleteEpisodeList whenever it could not reach a
// confirmed boundary, which resolveNumericEpisodeID reports as a failure
// because any error there is one. So playback broke precisely when the
// shedding warning fired, and an episode above the prefix was reported "not
// found" while a solitary HEAD of its manifest answers 200.
//
// The enumeration was never buying anything here: on this source the episode
// id *is* the episode number, so the lookup only ever reproduced the number it
// was given, and the existence check it also performed is made again — better,
// because it is not bounded by the prefix — by the solitary probe Watch runs
// before it returns a stream. The request count is asserted for that reason:
// it is what tells a re-introduced enumeration from an absent one.
func TestAnimeOnsenWatchDoesNotNeedACompleteEpisodeListToPlay(t *testing.T) {
	// A host that answers 200 for every episode, so enumeration runs into the
	// animeOnsenMaxEpisodes ceiling and reports its prefix with the flag.
	// GetEpisodes under this fixture returns a list and an error at once.
	newFake := func(t *testing.T) *animeOnsenFake {
		return newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": -1}})
	}
	// Precondition, written out rather than assumed: this fixture really does
	// make GetEpisodes answer with both.
	t.Run("the fixture does return a flagged list", func(t *testing.T) {
		eps, err := newFake(t).provider().GetEpisodes("x", "x")
		if len(eps) == 0 || !errors.Is(err, ErrIncompleteEpisodeList) {
			t.Fatalf("GetEpisodes = %d episodes, err %v; want a prefix wrapping ErrIncompleteEpisodeList", len(eps), err)
		}
	})

	for _, tc := range []struct {
		name      string
		episodeID string
		want      int
	}{
		{"an episode inside the measured prefix", "x:1:3", 3},
		// Past the prefix the old code reported "episode N not found" while
		// the CDN serves it.
		{"an episode past the measured prefix", "x:1:5000", 5000},
		// What every movie request in the fallback chain sends.
		{"the empty id the chain sends for a film", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			s, err := f.provider().Watch("x", tc.episodeID, "", "1080")
			if err != nil {
				t.Fatalf("Watch(%q) = %v; an incomplete episode list must not stop playback", tc.episodeID, err)
			}
			if want := fmt.Sprintf("/%d/manifest.mpd", tc.want); !strings.HasSuffix(s.URL, want) {
				t.Fatalf("Watch(%q) URL = %q, want one ending %q", tc.episodeID, s.URL, want)
			}
			if got := len(f.requests()); got != 1 {
				t.Fatalf("Watch made %d requests, want 1 (the availability probe); the episode list is being enumerated to resolve a number it was handed", got)
			}
		})
	}
}

// TestAnimeOnsenWatchStillRefusesWhatTheLookupUsedTo is the other side of the
// test above: dropping the catalogue lookup must not drop the checks it was
// making. "Episode N exists" is a real check, and trading a wrong failure for
// a wrong success would be no improvement at all.
func TestAnimeOnsenWatchStillRefusesWhatTheLookupUsedTo(t *testing.T) {
	for _, tc := range []struct{ name, episodeID string }{
		// The check the probe below Watch makes, reached through the fallback
		// ref rather than through a bare number.
		{"an episode past the end of the series", "x:1:13"},
		// A season that is a different catalogue entry here.
		{"a second season", "x:2:1"},
		{"a season that is not a number", "x:one:1"},
		{"an episode that is not a number", "x:1:one"},
		{"an episode numbered zero", "x:1:0"},
		{"too few fields", "x:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 12}})
			if s, err := f.provider().Watch("x", tc.episodeID, "", "1080"); err == nil {
				t.Fatalf("Watch(%q) = %q, want a refusal", tc.episodeID, s.URL)
			}
		})
	}
}

// withAnimeOnsenProbeBudget shrinks the probe's budget for one test, so a
// deadline can be reached in milliseconds instead of seconds.
func withAnimeOnsenProbeBudget(t *testing.T, d time.Duration) {
	t.Helper()
	prev := animeOnsenProbeBudget
	animeOnsenProbeBudget = d
	t.Cleanup(func() { animeOnsenProbeBudget = prev })
}

// TestAnimeOnsenProbeBoundsEachRequestInFlightByItsBudget is the gap the
// budget's own comment claimed could not exist ("under 4.5 s the probe always
// gets to answer").
//
// The budget was only ever consulted between requests and between waves, so a
// single stalled round trip was bounded by the HTTP client's 30 s timeout
// instead — six times the 5 s at which cmd/episodes.go abandons the call and
// asks the fallback chain. That is the silent downgrade the whole 429 fix
// exists to prevent, reached by a different route, and the abandoned goroutine
// goes on probing behind it.
//
// The assertion is on elapsed time, because that is the thing that changes: an
// unbounded probe still returns a flagged answer eventually, just long after
// anyone is listening.
func TestAnimeOnsenProbeBoundsEachRequestInFlightByItsBudget(t *testing.T) {
	animeOnsenNoSleep(t)
	withAnimeOnsenProbeBudget(t, 300*time.Millisecond)
	// The bracket's first wave is 1,2,4,...,128, so everything up to 32
	// answers at once and the top two probes hold their requests open far
	// longer than the budget. The stalled ones are the top of the wave rather
	// than the bottom on purpose: animeOnsenProbeInFlight slots are handed out
	// in no particular order, so a wave whose *low* episodes stall can starve
	// the fast ones out of a slot entirely and measure nothing at all.
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:   map[string]int{"x": -1},
		stallAfter: map[int]int{64: 0, 128: 0},
		stallFor:   3 * time.Second,
	})
	p := f.provider()

	start := time.Now()
	n, err := p.episodeCount("x")
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("episodeCount took %s against a %s budget; a request already in flight is not bounded by the probe deadline, only the gaps between them are",
			elapsed, animeOnsenProbeBudget)
	}
	// A stalled probe teaches nothing about its episode, which is the same
	// thing a shed one teaches: report the measured prefix and say it is one,
	// rather than failing the provider or claiming the series ends here.
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	if n != 32 {
		t.Fatalf("episodeCount = %d, want the 32 episodes that answered before the stall", n)
	}
}

// TestAnimeOnsenAnUnreachableCDNIsNotAShortAnswer is the other side of the
// test above.
//
// The probe deadline and the HTTP client's own 30 s timeout surface the same
// context.DeadlineExceeded, and only one of them means "this is as far as the
// measurement got". A client timeout means the CDN could not be reached at
// all, which is a failure to report, not a prefix to flag — reading it as
// budget expiry would have the provider answer with a confident short list
// every time the network is down.
func TestAnimeOnsenAnUnreachableCDNIsNotAShortAnswer(t *testing.T) {
	animeOnsenNoSleep(t)
	// Stall every episode, and let the client give up long before the probe's
	// own budget does.
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": 12},
		stallAll: true,
		stallFor: 3 * time.Second,
	})
	p := f.provider()
	p.client = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := p.probeEpisodeWithRetries("x", 1, p.now().Add(time.Hour), animeOnsenProbeRetries)
	if err == nil {
		t.Fatal("probeEpisodeWithRetries succeeded against a CDN that never answered")
	}
	if errors.Is(err, errAnimeOnsenProbeExpired) {
		t.Fatalf("probe err = %v; a client timeout with an hour of budget left was reported as the budget running out, which turns an unreachable CDN into an honest-looking short list", err)
	}
}

// TestAnimeOnsenASpentBudgetIsNotReportedAsThrottling pins which of the two
// "the wave taught nothing" reasons is reported.
//
// They point at different levers — throttling is answered by lowering
// animeOnsenProbeInFlight, a spent budget by raising animeOnsenProbeBudget —
// so reporting the wrong one sends whoever reads the warning to the wrong
// place. The wave here is starved rather than shed: the stalled probes hold
// every in-flight slot until the deadline, so the fast episodes below them
// never get one and nothing at all is measured.
func TestAnimeOnsenASpentBudgetIsNotReportedAsThrottling(t *testing.T) {
	animeOnsenNoSleep(t)
	withAnimeOnsenProbeBudget(t, 200*time.Millisecond)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes: map[string]int{"x": -1},
		stallAll: true,
		stallFor: 3 * time.Second,
	})
	_, err := f.provider().episodeCount("x")
	if err == nil {
		t.Fatal("episodeCount succeeded against a CDN that answered nothing")
	}
	if !strings.Contains(err.Error(), "probe exceeded") {
		t.Fatalf("episodeCount err = %v, want one naming the spent probe budget", err)
	}
	if strings.Contains(err.Error(), "throttled") {
		t.Fatalf("episodeCount err = %v; the budget ran out, and calling that throttling points at the wrong lever", err)
	}
}

// TestAnimeOnsenAStalledConfirmationProbeStillYieldsThePrefix reaches the one
// place the budget can run out that is not a wave: the solitary HEAD of n+1
// that episodeCount sends before it reports a boundary.
//
// It is the last probe of a successful measurement, so a budget spent there
// has a real prefix behind it — twelve episodes the CDN served — and losing
// that to a transport error would send `episodes` to the fallback chain with
// nothing, which is the downgrade this whole file is about. The boundary is
// simply unconfirmed, which is what ErrIncompleteEpisodeList says.
//
// The fixture has to leave budget for the bracket and the narrowing and run
// out only inside the confirmation probe; a budget already spent is caught one
// line earlier and never reaches this arm.
func TestAnimeOnsenAStalledConfirmationProbeStillYieldsThePrefix(t *testing.T) {
	animeOnsenNoSleep(t)
	withAnimeOnsenProbeBudget(t, 400*time.Millisecond)
	// A one-episode series, so the bracket lands on the boundary directly
	// (1 present, 2 absent) and the narrowing never runs. Episode 2 answers
	// the bracket's 404 and then stalls, which makes the stalled request the
	// confirmation probe and nothing else — 404s are deliberately not cached,
	// so the probe really does ask again.
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:   map[string]int{"x": 1},
		stallAfter: map[int]int{2: 1},
		stallFor:   3 * time.Second,
	})
	p := f.provider()

	n, err := p.episodeCount("x")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount = %d, err = %v; want the measured prefix wrapping ErrIncompleteEpisodeList", n, err)
	}
	if n != 1 {
		t.Fatalf("episodeCount = %d, want the 1 episode that answered", n)
	}
	// And it is the weak flag: the bracket measured episode 2 absent, so the
	// end of the series was located and only the re-check of it was lost.
	if !errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrUnconfirmedEpisodeList", err)
	}
	// Proof the arm under test is the one reached, and not the between-waves
	// budget check: episode 2 has to have been asked about twice, once by the
	// bracket and once by the confirmation probe that then stalled.
	asked := 0
	for _, r := range f.requests() {
		if strings.HasSuffix(r.path, "/2/manifest.mpd") {
			asked++
		}
	}
	if asked != 2 {
		t.Fatalf("episode 2 was probed %d times, want 2 (the bracket, then the confirmation probe); this test is not reaching the confirmation probe at all", asked)
	}
}

// A boundary the search located and could not re-confirm is a different answer
// from a list that never found the end, and the two have to be separable by a
// caller that is not reading English.
//
// Measured, live: nine `episodes` runs against KAMUI (12 episodes) returned the
// right 12 eight times, and the one incompleteness code fired on six of the
// nine — five of those on a correct list. Under one code the signal that says
// "this list is short" fires on two thirds of healthy runs, which is how a
// caller learns to ignore it. These three tests pin which state produces which
// sentinel.
//
// This one is the shed confirmation probe: the search settles on 12, and the
// solitary HEAD of episode 13 that would confirm it is refused until the retry
// policy gives up.
func TestAnimeOnsenAShedConfirmationProbeReportsAnUnconfirmedEnd(t *testing.T) {
	animeOnsenNoSleep(t)
	// A one-episode series, so the bracket lands on the boundary directly
	// (1 present, 2 absent) and the narrowing never runs. Episode 2 answers
	// the bracket's probe with a real 404 and sheds every request after it,
	// which makes the shed requests the confirmation probe and its retries and
	// nothing else.
	//
	// The first version of this test shed episode 13 of a twelve-episode
	// series from the first request, and it scored the strong code — correctly,
	// as it turned out: 13 is inside the narrowing wave, so shedding it leaves
	// episode 13 unknown between a present 12 and an absent 14, which is a gap
	// and not a located end. The arm under test was never reached.
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 1},
		throttleAfter: map[int]int{2: 1},
	})
	p := f.provider()
	n, err := p.episodeCount("x")
	if n != 1 {
		t.Fatalf("episodeCount = %d, want the 1 episode the bracket measured", n)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	if !errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v; the bracket measured episode 2 absent and only the re-check was refused, which is not the same as never finding the end", err)
	}
	// Proof the arm reached is the confirmation probe's and not the wave's:
	// episode 2 answered the bracket once and was then asked three more times,
	// which is the solitary re-check plus the retry policy's two retries.
	asked := 0
	for _, r := range f.requests() {
		if strings.HasSuffix(r.path, "/2/manifest.mpd") {
			asked++
		}
	}
	if asked != 4 {
		t.Fatalf("episode 2 was probed %d times, want 4 (the bracket, then the confirmation probe and its two retries); this test is not reaching the confirmation probe", asked)
	}
}

// The other half of the split, and the live symptom: a measurement that never
// located the end at all. Every probe above 12 is shed, so the bracket proves
// 1,2,4,8 present, learns nothing from its second wave, and stops at 8 — four
// episodes short of a series it could have listed. That list really is missing
// episodes and must keep the strong code.
func TestAnimeOnsenAListThatNeverFoundTheEndIsNotMerelyUnconfirmed(t *testing.T) {
	animeOnsenNoSleep(t)
	f := newAnimeOnsenFake(t, &animeOnsenFake{
		episodes:      map[string]int{"x": 12},
		throttleAbove: 12,
	})
	n, err := f.provider().episodeCount("x")
	if n != 8 {
		t.Fatalf("episodeCount = %d, want the measured prefix 8", n)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	if errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v; nothing above episode 8 ever answered, so episodes 9-12 are missing from this list and calling the end merely unconfirmed understates it", err)
	}
}

// The ceiling is the other never-found-the-end case: a host that answers 200 to
// everything is not a series whose end was located.
func TestAnimeOnsenTheCeilingIsNotAnUnconfirmedEnd(t *testing.T) {
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": -1}})
	_, err := f.provider().episodeCount("x")
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrIncompleteEpisodeList", err)
	}
	if errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v; the probe stopped at its own ceiling without ever seeing an absent episode, so there is no located end to be unconfirmed about", err)
	}
}

// The third way a located boundary goes unconfirmed: the budget is gone by the
// time the search hands the boundary back, so the confirmation probe is never
// sent at all.
//
// It is a separate arm from the two above — the check between searchBoundary
// returning and the probe going out — and the request count is what proves this
// test reaches it rather than one of them: eight requests is the bracket wave
// and nothing else, so no confirmation probe was ever attempted.
func TestAnimeOnsenABoundaryHandedBackWithNoBudgetLeftIsUnconfirmed(t *testing.T) {
	animeOnsenNoSleep(t)
	// One episode, so the bracket lands on the boundary directly (1 present,
	// 2 absent) and the narrowing never runs.
	f := newAnimeOnsenFake(t, &animeOnsenFake{episodes: map[string]int{"x": 1}})
	p := f.provider()
	// A clock that stays put until the bracket's whole wave has arrived and
	// then jumps past the deadline. It cannot jump earlier: headManifest sizes
	// each request's context from the remaining budget, so a clock already past
	// the deadline would cancel every request instead of letting the wave
	// measure the boundary.
	base := time.Now()
	p.now = func() time.Time {
		if len(f.requests()) >= 8 {
			return base.Add(2 * animeOnsenProbeBudget)
		}
		return base
	}
	n, err := p.episodeCount("x")
	if n != 1 {
		t.Fatalf("episodeCount = %d, want the 1 episode the bracket measured", n)
	}
	if !errors.Is(err, ErrUnconfirmedEpisodeList) {
		t.Fatalf("episodeCount err = %v; the bracket measured episode 2 absent, so the end was located and only the re-check was missed", err)
	}
	if !errors.Is(err, ErrIncompleteEpisodeList) {
		t.Fatalf("episodeCount err = %v, want one wrapping ErrIncompleteEpisodeList as well", err)
	}
	if got := len(f.requests()); got != 8 {
		t.Fatalf("the probe made %d requests, want the bracket's 8 and no confirmation probe; this test is not reaching the arm between the search and the probe", got)
	}
}
