package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	animeOnsenAPIBase = "https://api.animeonsen.xyz/v4"
	animeOnsenCDNBase = "https://cdn.animeonsen.xyz/video/mp4-dash"

	// animeOnsenReferer is required by the CDN and is matched exactly: the
	// bare apex ("https://animeonsen.xyz/") answers 403, only the www host
	// with the trailing slash answers 200. Verified 2026-10-02.
	animeOnsenReferer = "https://www.animeonsen.xyz/"

	// animeOnsenUA is not cosmetic. The CDN denies ffmpeg's default
	// "Lavf/<version>" User-Agent with 403 while serving any other value,
	// including none at all — so a stream handed to a player or to ffmpeg
	// without a UA of its own fails on the manifest fetch, not on playback.
	// media.Stream.UserAgent carries this to both.
	animeOnsenUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	// animeOnsenMaxEpisodes caps the availability probe. The catalogue holds
	// long-running series (One Punch Man, One Piece), so the ceiling has to
	// clear a four-digit episode count, but it must exist: without one a host
	// that answered 200 to everything would make the probe walk forever.
	animeOnsenMaxEpisodes = 4096

	// animeOnsenProbeWidth is how many manifests one wave of the episode probe
	// HEADs at once. The probe is the only fan-out this provider does, and it
	// is on `episodes` and `play`, so it is parallel for the same reason
	// cmd/multisearch.go races the search chain: a serial walk is a sequence of
	// round trips against a 30 s HTTP timeout, and an agent-facing command may
	// not be left waiting on it. Eight is enough to settle an ordinary
	// 12-to-26-episode series in two waves without opening a connection per
	// episode.
	animeOnsenProbeWidth = 8

	// animeOnsenProbeInFlight caps how many of a wave's probes are on the
	// wire at once, and it is not the same number as the width.
	//
	// The CDN is Cloudflare in front of an origin, and only a request that
	// misses the edge cache reaches origin. Origin sheds concurrent misses
	// with 429, and the shedding scales with how many are in flight: measured
	// against KAMUI on 2026-10-02, a wave of eight concurrent HEADs lost two
	// to three of them, two-at-a-time lost one in eight, and the same eight
	// sent strictly sequentially lost none. Neither the protocol nor the
	// connection count mattered — HTTP/1.1 over eight separate connections
	// shed as readily as one multiplexed HTTP/2 connection.
	//
	// Halving the in-flight count is therefore a real reduction and not a
	// cure; the retry in probeEpisode is the fix. Serialising the wave
	// outright would cure it and cannot be afforded: these probes are on
	// `episodes`, whose whole season scan is abandoned at 5s.
	animeOnsenProbeInFlight = 4

	// animeOnsenNarrowWidth is how many points one narrowing wave probes, and
	// it is deliberately the in-flight cap and not animeOnsenProbeWidth.
	//
	// The bracket wants to be wide: each of its probes doubles the range it
	// covers, so eight of them reach 256 episodes however few are on the wire
	// at once. The narrowing does not scale that way. A wave of W evenly
	// spaced points shrinks the unknown gap by a factor of W+1 and costs
	// ceil(W/F) serial tranches at an in-flight cap of F, so closing a gap of
	// g costs ceil(W/F)*log(g)/log(W+1) round trips — which is minimised at
	// W == F. Past the cap each extra probe buys less than the tranche it
	// forces.
	//
	// Simulated over the real schedule for every episode count from 1 to 300
	// at F = 4: W = 8 costs 7.96 round trips and 26.2 requests on average,
	// W = 4 costs 6.81 and 22.1, W = 3 costs 7.03, W = 2 costs 7.84. Fewer
	// requests is a second gain and not a rounding one — origin sheds
	// concurrent cache misses, so every probe not sent is one that cannot be
	// shed.
	animeOnsenNarrowWidth = animeOnsenProbeInFlight
)

// animeOnsenProbeBudget is the probe's own deadline, and it is deliberately
// *below* the tightest caller's, not above it.
//
// It was 10 s, on the reasoning that cmd.episodesFallbackTimeout (5 s) binds
// first anyway and the 10 s only covers the paths with no deadline of their
// own — resolver.Resolve's 30 s per attempt, and the TUI, which has none.
// That reasoning had the sign wrong. A budget above the caller's cap can never
// produce this provider's own answer on the path that matters: the caller
// abandons the call at 5 s and asks the fallback chain, which is precisely the
// silent downgrade the rest of this file exists to prevent — the reported bug
// was `episodes` printing ten episodes of a twelve-episode series, sourced
// from a provider that cannot stream any of them.
//
// Under 4.5 s the probe gets to answer, and when it has to stop early it
// answers with the prefix it measured and the ErrIncompleteEpisodeList flag.
//
// "Gets to answer" used to read "always gets to answer", and the measurement
// says otherwise: 20 live `episodes` runs on a 12-episode series returned the
// right count 19 times, and 7 of the 20 came back flagged — every flagged run
// at 4.51 s, i.e. at this ceiling, while every silent run finished between
// 3.05 s and 4.36 s. So the budget is the binding constraint on the last thing
// the enumeration does, and the honest statement is that 4.5 s covers the
// common case rather than every case.
//
// It cannot be answered by reserving part of this budget for that last step.
// The time available to it is deadline minus whenever the search finished, and
// a reserve moves neither term — it can only stop the search early, which
// turns a correct count with a weak flag into a short count with a strong one.
// What the budget buys is round trips, and the only levers that remove one are
// the probe schedule (see animeOnsenNarrowWidth, and episodeCount's shortcut
// for a boundary already measured alone) and the caller's cap, which is fixed.
//
// "Gets to answer" also rests on the budget reaching the requests and not only
// the gaps between them. It did not, at first: every probe was built with
// http.NewRequest, so the budget was consulted between waves while a single
// stalled round trip ran against the HTTP client's 30 s timeout — six times
// the caller's cap, i.e. the same silent downgrade by another route, with the
// abandoned goroutine still probing behind it. headManifest carries the
// remaining budget into each request's context, and a request the deadline
// cancels is read as errAnimeOnsenProbeExpired: unknown, not absent and not a
// transport failure. The cost is that a four-digit series against a slow CDN now reports a
// flagged prefix where it would previously have been cancelled outright; that
// is the same contract the animeOnsenMaxEpisodes ceiling already has, and it
// is visible in the JSON rather than silent.
//
// The 500 ms margin is for the probe to notice and return, not for more
// requests.
//
// What fits inside it, in measured round trips against the live CDN (~1 s for a
// cache miss, ~0.2 s for an edge hit): the cost is one tranche of
// animeOnsenProbeInFlight requests at a time, and a tranche costs a miss if any
// of its probes is above the end of the series. A 12-episode series spends five
// — the bracket's low half (edge hits), its upper half (all misses), the
// narrowing's wave, the single point left over, and, unless that point was
// alone on the wire, the re-check of it. Three of those are misses, which is
// the ~3.2-3.4 s the live runs show, and the spread above it is CDN jitter
// rather than extra work.
//
// A var, not a const, so a test can shrink it and watch the probe give up
// rather than having to serve thousands of manifests to reach the ceiling.
var animeOnsenProbeBudget = 4500 * time.Millisecond

// animeOnsenConfirmRounds bounds how many times the episode search is re-run
// after a 404 turned out not to be the end of the series.
//
// One round is the normal case: the search settles on a boundary, a solitary
// HEAD of the next episode agrees it is absent, done. A round is spent only
// when that HEAD disagrees, and the re-run starts from the episode it just
// proved present, so each round strictly advances. Four is far more than a
// real CDN needs and still bounds a host that answers 404 to every burst — the
// probe then reports what it measured and says the list is incomplete, rather
// than walking forward one episode at a time until the budget runs out.
//
// A var for the same reason the budget is one: a test can shrink it and watch
// the probe give up, instead of needing a fixture that lies four times in a
// row in exactly the right places.
var animeOnsenConfirmRounds = 4

// animeOnsenSleep is the probe's backoff sleep, as a seam so a retry test
// asserts on the wait the policy chose instead of spending it.
var animeOnsenSleep = time.Sleep

// The retry policy for a shed probe. 429 is the status the CDN actually sends
// and it means "ask again", not "no such episode" and not "this provider is
// broken" — both of which cost the whole enumeration and send `episodes` down
// the fallback chain to a provider with a shorter list it cannot stream.
//
// The numbers are chosen against cmd.episodesFallbackTimeout, which abandons
// the season scan at 5s: a retry policy that outlives it converts a
// recoverable 429 into exactly the silent fallback it was meant to prevent.
// Two retries at 150ms then 300ms is 450ms of waiting per probe, and the
// probes of a wave wait concurrently, so a wave costs at most one 450ms
// backoff on top of its round trips however many of its probes are shed.
//
// Vars rather than consts for the usual reason here: a test shrinks them, and
// one asserts the defaults as written-out literals so shrinking the policy
// cannot satisfy it.
var (
	animeOnsenProbeRetries = 2
	animeOnsenRetryBase    = 150 * time.Millisecond
	animeOnsenMaxRetryWait = 2 * time.Second
)

// errAnimeOnsenThrottled marks a probe that kept being shed. It is
// deliberately neither ErrNoResults nor a bare status error: the caller has to
// be able to tell "the CDN would not answer" from "the episode is not there",
// because those two readings produce a short list and a missing show
// respectively, and both look like a finished answer from outside.
var errAnimeOnsenThrottled = errors.New("animeonsen: the CDN throttled the availability probe")

// errAnimeOnsenProbeExpired marks a probe whose request was still in flight
// when the probe budget ran out.
//
// It is a third answer alongside "present", "absent" and "shed", and it is
// read the same way as "shed": the episode is unknown, the measurement carries
// on with what it did learn, and the prefix is reported with
// ErrIncompleteEpisodeList. Reading it as a transport failure instead would
// lose the whole provider to the fallback chain — the exact silent downgrade
// the retry policy above exists to prevent — and reading it as "absent" would
// end the series wherever the CDN happened to be slow.
//
// It is never retried: there is by definition no budget left to retry inside.
var errAnimeOnsenProbeExpired = errors.New("animeonsen: the availability probe ran out of budget mid-request")

// animeOnsenRetryable reports whether status is worth asking again.
//
// 429 is the measured one. The 5xx family and 408 are included because they
// say the same thing — nothing was learnt about the episode — while 403 is
// pointedly excluded: here it means the Referer or User-Agent was rejected,
// which no amount of asking again will change, and retrying it would spend
// the budget three times over to reach the same error.
func animeOnsenRetryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		return true
	}
	return status >= 500 && status < 600
}

// animeOnsenRetryWait is how long to wait before asking again.
//
// Retry-After wins when the server sends one, in either of the forms RFC 9110
// allows, clamped to animeOnsenMaxRetryWait so a server cannot park the probe.
// The live 429s carried no Retry-After at all, which is why there is a default
// to fall back on rather than a policy built around the header.
func (p *AnimeOnsen) animeOnsenRetryWait(attempt int, h http.Header) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return min(time.Duration(secs)*time.Second, animeOnsenMaxRetryWait)
		}
		if when, err := http.ParseTime(v); err == nil {
			if d := when.Sub(p.now()); d > 0 {
				return min(d, animeOnsenMaxRetryWait)
			}
			return 0
		}
	}
	return min(animeOnsenRetryBase<<attempt, animeOnsenMaxRetryWait)
}

// AnimeOnsen streams anime from animeonsen.xyz's public v4 API.
//
// Three plain GETs, no crypto, no token, no DRM:
//
//	GET api.animeonsen.xyz/v4/search/<query>                      -> JSON, content ids
//	HEAD/GET cdn.animeonsen.xyz/video/mp4-dash/<id>/<ep>/manifest.mpd
//
// Only /v4/search is public; every /v4/content/... endpoint answers 401, so
// episode counts come from probing the CDN rather than from a catalogue call,
// and lobster ships no credential.
//
// # This source is DASH, and lobster is otherwise an HLS codebase
//
// The manifest is MPEG-DASH with a single 720p h264 Representation and a
// single AAC Representation — no quality ladder, so --quality has nothing to
// select here and nothing is invented to give it one. media.Stream carries the
// .mpd URL unchanged, with Deobfuscate false: the fake-PNG HLS proxy
// (internal/hlsproxy) rewrites m3u8 playlists and would corrupt a manifest it
// does not understand, and there is nothing to de-obfuscate in the first
// place. Players reach it through the headers media.Stream already models
// (internal/player/headers.go emits --referrer and --user-agent, which mpv
// carries onto the demuxer's own fetches: measured against a logging server,
// the DASH manifest, its init segment and every media segment arrive with both
// headers, exactly as HLS segment requests do).
//
// # No subtitles come from this source
//
// Every manifest carries exactly two Representations, one video and one audio
// (lang="jpn"); there is no text AdaptationSet, and the site's own subtitle
// data sits behind the 401 catalogue API. So Watch returns no media.Subtitle
// tracks, and a stream from here is raw Japanese audio unless lobster's own
// subtitle layer (internal/subtitle, SubDL) supplies a track — which needs
// subdl_api_key to be set. That is worth knowing before naming this base for
// a show you cannot follow unsubtitled; it is not something the provider can
// fix by guessing a URL.
//
// The download side is NOT symmetrical, and the asymmetry is deliberate:
// internal/download goes through ffmpeg, which demuxes DASH natively, while
// internal/dlmanager's engine speaks only "hls" and "http". cmd.streamToResult
// classifies by substring, so a .mpd would be labelled "http" and the HTTP
// engine would save the manifest XML under a video filename — a corrupt
// artifact that looks like a finished download. cmd.streamToResultChecked
// refuses it instead and says so.
type AnimeOnsen struct {
	client  httpDoer
	apiBase string
	cdnBase string
	now     func() time.Time

	// confirmedMu guards confirmed, which remembers the episodes this
	// provider has watched the CDN serve, so a run does not ask twice.
	//
	// Only 200s are remembered, and that asymmetry is the whole design. A
	// 404 has to stay re-askable: re-asking one on its own is the mechanism
	// that catches a shed burst, and a cached 404 would feed the
	// confirmation probe the very answer it exists to re-test. A 200, by
	// contrast, cannot have been a lie — the CDN does not serve a manifest
	// for an episode it does not have — so reusing it is free.
	//
	// The map is per-provider-instance, i.e. per command, and bounded by
	// animeOnsenMaxEpisodes entries per content id.
	confirmedMu sync.Mutex
	confirmed   map[string]bool

	// probeGate admits one episode enumeration at a time per instance, and it
	// is what makes "this 404 arrived with nothing else in flight" true of the
	// instance rather than only of the wave that sent it.
	//
	// probeSet decides solitariness by construction — a wave of one request is
	// alone, and the serial re-ask pass is one at a time by definition — and
	// that reasoning is sound for one enumeration and silently false for two.
	// Two GetEpisodes calls can overlap on one instance: the TUI's download
	// dialog closes on Escape without cancelling the tea.Cmd that is fetching
	// its episode list (internal/tui/download_dialog.go: cancel() clears the
	// flags, nothing cancels the command), so reopening it starts a second
	// fetch against the same provider the first is still probing with. A
	// multi-season batch reaches it too: seasonLister caches its chain hits and
	// episodesWithContext abandons a provider call at the 5s cap while the
	// goroutine behind it goes on probing (cmd/batch.go, cmd/episodes.go).
	// Under either, a wave of one shares the wire with another enumeration's
	// wave of four, the CDN sheds the concurrent miss, and the 404 that comes
	// back is accepted as the end of the series — which is the exact
	// observation this whole file exists to distrust.
	//
	// Serialising is the right answer rather than detecting the overlap and
	// degrading to ErrUnconfirmedEpisodeList, because serialising still returns
	// a confirmed count to both callers; detection would return a warning to
	// the second one, and suppressing that warning on healthy runs is what §7
	// of this work was spent on.
	//
	// It is a channel and not a sync.Mutex because the wait has to be bounded:
	// episodeCount's contract is that it answers inside
	// animeOnsenProbeBudget — that is what lets `episodes` and `play` call it
	// without being left waiting — and a mutex would let a second caller block
	// past its own deadline, past cmd/episodes.go's 5s abandonment, and lose
	// its season's list to a timeout instead of to an answer.
	probeGateOnce sync.Once
	probeGate     chan struct{}
}

// gate returns the instance's probe gate, created on first use so a provider
// built as a literal (tests outside the constructors) is not a nil gate.
func (p *AnimeOnsen) gate() chan struct{} {
	p.probeGateOnce.Do(func() { p.probeGate = make(chan struct{}, 1) })
	return p.probeGate
}

// acquireProbe takes the instance's probe gate, waiting no longer than the
// probe's own budget. It reports false when the budget ran out first, which the
// caller must report rather than probe anyway: probing anyway is the bug.
//
// The wait is sized from the remaining budget rather than from the deadline for
// the same reason headManifest sizes its request contexts that way — the
// deadline is measured on p.now, the clock a test replaces, while a timer
// counts real time.
func (p *AnimeOnsen) acquireProbe(deadline time.Time) bool {
	g := p.gate()
	// The uncontended case, which is every single-caller run: no timer, and no
	// dependence on which of two ready cases select happens to pick.
	select {
	case g <- struct{}{}:
		return true
	default:
	}
	t := time.NewTimer(deadline.Sub(p.now()))
	defer t.Stop()
	select {
	case g <- struct{}{}:
		return true
	case <-t.C:
		return false
	}
}

func (p *AnimeOnsen) releaseProbe() {
	<-p.gate()
}

// NewAnimeOnsen returns a provider against the live hosts.
//
// There is no dub knob, and cfg.AnimeDub is deliberately not wired here. The
// CDN exposes one audio Representation per episode, lang="jpn", and no dub
// path exists to ask for: "mp4-dash-dub/...", ".../<ep>/dub/manifest.mpd" and
// ".../manifest-dub.mpd" all 404, and a title the other sources carry a dub
// for (Frieren S1) still serves a single jpn track here. A dub flag this
// source cannot honour would be a setting that silently does nothing.
func NewAnimeOnsen() *AnimeOnsen {
	return NewAnimeOnsenAt(animeOnsenAPIBase, animeOnsenCDNBase)
}

// NewAnimeOnsenAt returns a provider against the given API and CDN bases, in
// the same spirit as NewTBCPL and NewKimCartoon taking a host. It exists so a
// test outside this package — internal/resolver, which cannot be imported from
// here without a cycle — can drive the real provider against an httptest
// server instead of re-stating what its Search would have returned. The
// admission gate those tests cover (resolver.Matches) is the difference
// between this provider working and appearing to do nothing, so it has to be
// exercised against the provider itself.
func NewAnimeOnsenAt(apiBase, cdnBase string) *AnimeOnsen {
	return &AnimeOnsen{
		client:  httputil.NewClient(),
		apiBase: strings.TrimRight(apiBase, "/"),
		cdnBase: strings.TrimRight(cdnBase, "/"),
		now:     time.Now,
	}
}

// do sends one request with the two headers the CDN enforces and returns the
// status, the response headers and the body.
//
// The headers are returned because Retry-After is the one input the retry
// policy must defer to; the User-Agent is set on every request, HEAD probes
// included, because the CDN's deny rule is not limited to ffmpeg's Lavf — a
// Go-http-client/1.1 User-Agent is answered 403 as well (measured
// 2026-10-02), so a probe that let net/http fill the header in would be
// locked out while a browser string sails through.
// The context is the probe's deadline, and it has to reach the request rather
// than only the gaps between requests: without it a single stalled round trip
// is bounded by the HTTP client's 30 s timeout, six times the 5 s at which
// cmd/episodes.go abandons the call and asks the fallback chain — and the
// goroutine behind it goes on probing after everyone has stopped listening.
func (p *AnimeOnsen) do(ctx context.Context, method, rawURL string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", animeOnsenUA)
	req.Header.Set("Referer", animeOnsenReferer)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	if method == http.MethodHead {
		return resp.StatusCode, resp.Header, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, resp.Header, body, err
}

// animeOnsenHit is one row of the search response.
type animeOnsenHit struct {
	ContentID    string `json:"content_id"`
	ContentTitle string `json:"content_title"`
	TitleEN      string `json:"content_title_en"`
}

// Search returns the catalogue's matches for query.
//
// Every row is media.TV. The search response carries no type, duration or
// year, and AnimeOnsen does hold films; claiming Movie for some of them would
// mean guessing from the title, and claiming a Year nobody sent would feed
// resolver.candidateScore a fabricated signal. AllAnime answers the same way
// for the same reason.
//
// # Which of the two titles a row reports
//
// This is load-bearing, not presentation. A row carries both a romaji
// content_title and an English content_title_en, and resolver.Matches — the
// admission gate for `episodes` and for routeByType — demands that the
// candidate's title reduce to the *same normalized key* as the ref's. The
// reported failing case is exactly where the two disagree:
//
//	content_title    "Ushiro no Shoumen Kamui-san"
//	content_title_en "KAMUI: He's Behind You"       <- what TMDB and AniList-free
//	                                                   catalogues call it
//
// Reporting only the romaji title would have this provider answer about the
// right show under a name no caller asked about, and Matches would refuse it.
// So the English title is preferred, and the romaji one is reported instead
// when it is the spelling the caller actually asked about — the row is the
// same content id either way, and naming it under the alias the query used is
// what lets both spellings through the gate. Emitting one row per alias is not
// an option: resolver.dedupeByType keys on ID and would collapse them,
// keeping whichever came first and dropping the spelling that matches.
func (p *AnimeOnsen) Search(query string) ([]media.SearchResult, error) {
	status, _, body, err := p.do(context.Background(), http.MethodGet, p.apiBase+"/search/"+url.PathEscape(query))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if status == http.StatusNotFound {
		// The API's own miss: a distinct 404 with a message body, not a
		// 200 placeholder page.
		return nil, fmt.Errorf("%w: no anime found for %q", ErrNoResults, query)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("animeonsen: status %d", status)
	}
	var r struct {
		Status int             `json:"status"`
		Result []animeOnsenHit `json:"result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%w: search: %v", ErrUnrecognisedResponse, err)
	}
	out := make([]media.SearchResult, 0, len(r.Result))
	for _, h := range r.Result {
		if h.ContentID == "" {
			continue
		}
		out = append(out, media.SearchResult{
			ID:    h.ContentID,
			Title: animeOnsenTitle(query, h),
			Type:  media.TV,
			URL:   "https://www.animeonsen.xyz/details/" + h.ContentID,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no anime found for %q", ErrNoResults, query)
	}
	return out, nil
}

// animeOnsenTitle picks which of a row's two titles to report. See Search.
func animeOnsenTitle(query string, h animeOnsenHit) string {
	if h.TitleEN == "" {
		return h.ContentTitle
	}
	if h.ContentTitle != "" && !strings.EqualFold(h.ContentTitle, h.TitleEN) &&
		animeOnsenSameTitle(h.ContentTitle, query) && !animeOnsenSameTitle(h.TitleEN, query) {
		return h.ContentTitle
	}
	return h.TitleEN
}

// animeOnsenSameTitle reports whether a and b are the same title up to the
// punctuation and case differences catalogues disagree about. It is a local,
// deliberately crude stand-in for resolver.normalize, which the provider
// package cannot import without a cycle (internal/resolver imports
// internal/provider).
func animeOnsenSameTitle(a, b string) bool {
	return animeOnsenKey(a) == animeOnsenKey(b)
}

func animeOnsenKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// Dropped, not spaced: catalogues write "Marvels", never
			// "Marvel s". Same asymmetry as resolver.normalize.
		default:
			space = true
		}
	}
	return b.String()
}

func (p *AnimeOnsen) manifestURL(contentID string, episode int) string {
	return fmt.Sprintf("%s/%s/%d/manifest.mpd", p.cdnBase, contentID, episode)
}

// episodeAvailable reports whether contentID has episode n, under a fresh
// probe budget. It is the entry point for the single-episode checks that are
// not part of an enumeration — Watch's availability check, and episodeCount's
// own confirmation probes.
func (p *AnimeOnsen) episodeAvailable(contentID string, n int) (bool, error) {
	return p.probeEpisode(contentID, n, p.now().Add(animeOnsenProbeBudget))
}

// probeEpisode reports whether contentID has episode n, by HEADing its
// manifest. A present episode answers 200 and one past the end answers 404 —
// and a bogus content id answers 404 too, so there is no blanket-200 fallback
// to mistake for a hit.
//
// A 429 is neither of those answers, and that is the correction this carries.
// The CDN sheds concurrent cache misses with 429 (see animeOnsenProbeInFlight
// for the measurements), and the status means "ask again": read as a 404 it
// ends the series early, and read as a transport failure it loses the whole
// provider to the fallback chain, which on the reported case answered with ten
// episodes of a twelve-episode series from a source that cannot stream any of
// them. So a retryable status is retried, on a budget, and if it still will
// not answer the caller is told that specifically — errAnimeOnsenThrottled,
// not ErrNoResults and not a bare status.
func (p *AnimeOnsen) probeEpisode(contentID string, n int, deadline time.Time) (bool, error) {
	return p.probeEpisodeWithRetries(contentID, n, deadline, animeOnsenProbeRetries)
}

// probeEpisodeWithRetries is probeEpisode with the retry count made explicit,
// because the two callers need different ones.
//
// A probe sent on its own retries: that is the measured recovery condition —
// every shed request, asked again by itself, answered truthfully. A probe sent
// as part of a concurrent wave does not, and retrying inside the wave would be
// asking again under exactly the conditions that produced the refusal. probeSet
// collects what the wave shed and re-asks those one at a time instead.
func (p *AnimeOnsen) probeEpisodeWithRetries(contentID string, n int, deadline time.Time, retries int) (bool, error) {
	if p.isConfirmed(contentID, n) {
		return true, nil
	}
	for attempt := 0; ; attempt++ {
		status, hdr, err := p.headManifest(contentID, n, deadline)
		if err != nil {
			return false, err
		}
		switch {
		case status >= 200 && status < 300:
			p.confirm(contentID, n)
			return true, nil
		case status == http.StatusNotFound:
			return false, nil
		case animeOnsenRetryable(status):
			if attempt >= retries {
				return false, fmt.Errorf("%w: episode %d of %q answered %d on all %d attempts",
					errAnimeOnsenThrottled, n, contentID, status, attempt+1)
			}
			wait := p.animeOnsenRetryWait(attempt, hdr)
			if left := deadline.Sub(p.now()); left <= 0 || wait > left {
				return false, fmt.Errorf("%w: episode %d of %q answered %d and there is no budget left to ask again",
					errAnimeOnsenThrottled, n, contentID, status)
			}
			animeOnsenSleep(wait)
		default:
			// 403 (missing/rejected Referer or UA) is not "no such episode",
			// and reporting it as one would silently shorten every episode
			// list. It is not retryable either — see animeOnsenRetryable.
			return false, fmt.Errorf("animeonsen: manifest status %d", status)
		}
	}
}

// headManifest sends one probe request, bounded by the probe's own deadline.
//
// The bound is expressed as the remaining budget rather than as the deadline
// itself, because the deadline is measured on p.now — the clock a test
// replaces — while a request context counts real time. Handing a fake clock's
// instant to context.WithDeadline would expire every request in a test that
// only meant to simulate a spent budget.
//
// A request the context cancels is reported as errAnimeOnsenProbeExpired
// rather than as a transport failure, but only when the probe's own deadline
// is what passed: the HTTP client has a 30 s timeout of its own that surfaces
// the same context.DeadlineExceeded, and that one is a real failure to reach
// the CDN.
func (p *AnimeOnsen) headManifest(contentID string, n int, deadline time.Time) (int, http.Header, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline.Sub(p.now()))
	defer cancel()
	status, hdr, _, err := p.do(ctx, http.MethodHead, p.manifestURL(contentID, n))
	if err != nil && errors.Is(err, context.DeadlineExceeded) && !p.now().Before(deadline) {
		return 0, nil, fmt.Errorf("%w: episode %d of %q was still unanswered when the budget ran out",
			errAnimeOnsenProbeExpired, n, contentID)
	}
	return status, hdr, err
}

func (p *AnimeOnsen) confirmKey(contentID string, n int) string {
	return contentID + "/" + strconv.Itoa(n)
}

func (p *AnimeOnsen) isConfirmed(contentID string, n int) bool {
	p.confirmedMu.Lock()
	defer p.confirmedMu.Unlock()
	return p.confirmed[p.confirmKey(contentID, n)]
}

func (p *AnimeOnsen) confirm(contentID string, n int) {
	p.confirmedMu.Lock()
	defer p.confirmedMu.Unlock()
	if p.confirmed == nil {
		p.confirmed = make(map[string]bool)
	}
	p.confirmed[p.confirmKey(contentID, n)] = true
}

// probeSet HEADs every manifest in ns and reports which answered, which of
// those answers came from a request that was the only one in flight, and
// whether any probe was left unanswered — shed for good, or still in flight
// when the budget ran out.
//
// The solitary half is not bookkeeping for its own sake. A 404 observed inside
// a concurrent wave is the unreliable observation this whole file is about, and
// one observed alone is the reliable one; a caller that knows which it got can
// tell whether the boundary still needs re-checking. It is decided by
// construction rather than measured: a wave of one request is alone on the
// wire, and the second pass below is serial by definition.
//
// At most animeOnsenProbeInFlight of them are on the wire at once: origin
// sheds concurrent cache misses, and the whole wave arriving together is the
// worst shape for that. The cap is a reduction rather than a cure — probes
// that are still shed after their retries are reported as *unknown* (left out
// of present) with throttled set, which lets the search carry on with what it
// did learn instead of losing the provider over one refused request.
//
// A probe that errors at the transport level, or answers something that is
// neither 2xx nor 404 nor retryable, still fails the whole wave: 403 is what a
// rejected Referer or User-Agent looks like here, and reading it as "no such
// episode" would silently shorten every episode list to nothing instead of
// saying what broke.
func (p *AnimeOnsen) probeSet(contentID string, ns []int, deadline time.Time) (map[int]bool, map[int]bool, bool, error) {
	present := make(map[int]bool, len(ns))
	alone := make(map[int]bool, len(ns))
	errs := make([]error, len(ns))
	oks := make([]bool, len(ns))
	solo := make([]bool, len(ns))
	for i := range solo {
		// A wave of one request has nothing to be concurrent with.
		solo[i] = len(ns) == 1
	}

	sem := make(chan struct{}, animeOnsenProbeInFlight)
	var wg sync.WaitGroup
	for i, n := range ns {
		wg.Add(1)
		go func(i, n int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// No retries inside the wave: asking again while the rest of the
			// wave is still in flight is asking again under the conditions
			// that caused the refusal.
			oks[i], errs[i] = p.probeEpisodeWithRetries(contentID, n, deadline, 0)
		}(i, n)
	}
	wg.Wait()

	// Second pass, strictly one request at a time. This is the shape the
	// recovery was measured under: every probe the live CDN shed answered
	// truthfully when it was re-sent on its own.
	// A probe the budget cut short is not re-asked: there is no budget left to
	// ask in, and the re-ask pass would spend what remains discovering that.
	unanswered := false
	for i, n := range ns {
		if errs[i] != nil && errors.Is(errs[i], errAnimeOnsenThrottled) {
			oks[i], errs[i] = p.probeEpisodeWithRetries(contentID, n, deadline, animeOnsenProbeRetries)
			solo[i] = true
		}
		switch {
		case errs[i] == nil:
			present[n] = oks[i]
			alone[n] = solo[i]
		case errors.Is(errs[i], errAnimeOnsenThrottled), errors.Is(errs[i], errAnimeOnsenProbeExpired):
			unanswered = true
		default:
			return nil, nil, false, errs[i]
		}
	}
	return present, alone, unanswered, nil
}

// episodeCount returns how many episodes contentID has, by probing the CDN.
//
// Only /v4/search is public — every catalogue endpoint answers 401 — so the
// count is not available to be read. What is available is a per-episode
// existence oracle, and AnimeOnsen numbers episodes contiguously from 1, so the
// count is the position of the boundary between the episodes that answer 200
// and the ones that answer 404.
//
// The result is a measurement, not a guess: the returned count N is an episode
// that answered 200, and N+1 answered 404 *to a request sent on its own*. That
// is the property this search establishes by construction, and it is why this
// is not the fabricated-episode-list problem of #61 — nothing is reported that
// was not probed or bracketed by two probes either side of it.
//
// The "on its own" is the correction to the first version of this, and it was
// worth two episodes of a 12-episode series. Every 404 the search sees arrives
// inside a wave of animeOnsenProbeWidth concurrent HEADs, and a CDN shedding
// that burst answers 404 for a manifest it will serve — indistinguishable,
// here, from the end of the series, because a 404 is the only thing that ends
// it. `episodes` reported 10 of KAMUI's 12 three runs in a row while a HEAD of
// episode 11 sent alone answered 200. So the boundary is re-tested by a
// solitary probe before it is reported, and a 200 there sends the search back
// out from the episode it just proved present.
//
// When enumeration stops somewhere other than a confirmed boundary — the
// animeOnsenMaxEpisodes ceiling, or a host that keeps answering 404 for
// episodes it then serves — the count is still returned, and it is returned
// with an error wrapping ErrIncompleteEpisodeList. A partial list reported as
// a whole one is the same dishonesty as an invented one: the caller cannot see
// the difference, and `lobster episodes` says so in its envelope instead.
//
// # Why it is shaped this way
//
// Both halves of the search probe animeOnsenProbeWidth manifests at a time,
// concurrently, so the cost is in *round trips* rather than in requests:
//
//	bracket  probe 1, 2, 4, 8, ... 2^k in one wave, and keep widening while
//	         everything answers, until some power of two 404s
//	narrow   probe animeOnsenNarrowWidth evenly spaced points inside the
//	         bracket at once, shrinking it by a factor of that width+1 per wave
//
// An ordinary 12-to-26-episode series therefore settles in two waves. A
// four-digit series settles in about five. A serial walk would have taken one
// round trip per probe against a 30 s HTTP timeout, which is what
// agent-facing commands may not be left waiting on.
//
// The contiguity assumption is the one thing taken on faith, and it is the
// assumption the source's own URL scheme encodes. A gap would make this
// under-report (the count stops at the gap) rather than over-report, which is
// the safe direction: a missing episode is visible, an invented one is not.
func (p *AnimeOnsen) episodeCount(contentID string) (int, error) {
	deadline := p.now().Add(animeOnsenProbeBudget)
	// One enumeration per instance at a time: every 404 below is trusted only
	// because nothing else of this provider's was on the wire beside it, and
	// that is a property of the instance, not of one wave. See probeGate.
	if !p.acquireProbe(deadline) {
		// Nothing was measured, so there is no prefix to flag — short's n < 1
		// arm is the plain error, and the chain is then the right place to ask.
		return p.short(contentID, 0, fmt.Sprintf(
			"another episode probe for this provider was still running when the %s budget ran out",
			animeOnsenProbeBudget))
	}
	defer p.releaseProbe()
	expired := func() error {
		if p.now().After(deadline) {
			return errors.New(expiredBudgetReason())
		}
		return nil
	}

	floor := 0
	for round := 0; round < animeOnsenConfirmRounds; round++ {
		n, short, confirmed, err := p.searchBoundary(contentID, floor, deadline)
		if err != nil {
			return 0, err
		}
		if short != "" {
			return p.short(contentID, n, short)
		}
		if confirmed {
			// The search already measured episode n+1 absent with nothing else
			// in flight, which is the standard the probe below exists to
			// apply. Asking again would spend the budget's last and most
			// expensive round trip re-establishing a fact already established
			// to that standard: the manifest after the end of a series is
			// never in the edge cache, so that probe is always an origin miss
			// — the ~1s round trip here, against a 4500ms budget — and
			// spending it is what left healthy runs reporting an unconfirmed
			// end.
			//
			// This is the only shortcut taken, and it is the same oracle
			// rather than a weaker one. A boundary whose absence was observed
			// inside a concurrent wave gets no shortcut: that is the
			// observation a shedding CDN falsifies, and re-asking it is what
			// two episodes of a twelve-episode series rested on.
			return n, nil
		}
		if err := expired(); err != nil {
			// searchBoundary found the end — it returned no reason, which it
			// only does with the next episode measured absent — so the one
			// thing missing is the solitary re-confirmation below.
			return p.unconfirmed(contentID, n, err.Error())
		}
		// Confirm the boundary with a HEAD sent on its own. The search's 404s
		// arrive inside a wave of animeOnsenProbeWidth concurrent requests,
		// and a CDN shedding that burst answers 404 for a manifest that
		// exists — which this probe cannot tell from the end of the series,
		// because a 404 is the only thing that ends it. A solitary request is
		// the condition under which the boundary is reproducible by hand, and
		// it is what the reported count now rests on.
		//
		// A 200 here means the 404 was a lie, so the search runs again from
		// the episode just proved present. It cannot loop: floor strictly
		// increases, and the ceiling bounds it.
		ok, err := p.probeEpisode(contentID, n+1, deadline)
		if err != nil {
			if errors.Is(err, errAnimeOnsenProbeExpired) {
				return p.unconfirmed(contentID, n, expiredBudgetReason())
			}
			if errors.Is(err, errAnimeOnsenThrottled) {
				return p.unconfirmed(contentID, n, fmt.Sprintf("the CDN would not answer whether episode %d exists", n+1))
			}
			return 0, err
		}
		if !ok {
			return n, nil
		}
		floor = n + 1
	}
	return p.short(contentID, floor,
		fmt.Sprintf("the CDN answered 404 for an episode it then served, %d times over", animeOnsenConfirmRounds))
}

// short reports an enumeration that stopped somewhere other than a confirmed
// boundary: the ceiling, the budget, a shed probe, or a host that keeps
// answering 404 for episodes it then serves.
//
// There is one rule, and it is the reason this is a single function rather
// than four call sites. A measured prefix is returned *with* the flag, because
// the alternative — an error — is read one layer up as "ask someone else", and
// asking someone else is what produced the reported bug: AnimeOnsen's
// enumeration failed on a single 429 and `episodes` printed ten episodes of a
// twelve-episode series, sourced from a provider that cannot stream any of
// them. A flagged ten from the provider that can play it beats an unflagged
// ten from one that cannot, and a flagged twelve beats both.
//
// Nothing measured at all is the one case that is still a plain error. There
// is no list to flag, and the chain is then the right place to ask. It is
// deliberately not ErrNoResults: "the CDN would not answer" is not "the
// catalogue does not have this", and the chain treats the latter as final.
//
// short is the strong form: the end of the series was never located, so the
// reported count is where the probe schedule stopped and episodes above it are
// missing. unconfirmed is the weak form — see there.
func (p *AnimeOnsen) short(contentID string, n int, reason string) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("animeonsen: %q could not be enumerated: %s", contentID, reason)
	}
	return n, fmt.Errorf("%w: animeonsen: %s, so %d is as far as %q could be confirmed",
		ErrIncompleteEpisodeList, reason, n, contentID)
}

// unconfirmed reports an enumeration that located the end of the series and
// could not re-confirm it: episode n answered present, episode n+1 answered
// absent inside a wave, and the solitary HEAD that re-tests that absence
// either was shed or had no budget left to be sent in.
//
// It is the weak form of short, and the two are separated because collapsing
// them made the warning unreadable. This one is the steady state of a probing
// enumeration against this CDN — the re-check is the last request inside the
// budget and it is always a cache miss, since nobody watches the episode after
// the last one — and it fired on five of nine live runs that returned a
// complete and correct list, then on six of twenty after the split, every one
// of them at the budget ceiling. short's cases are the ones where episodes
// really are absent from the list.
//
// The reachable cause is therefore the round trip itself, not the wording, and
// episodeCount no longer spends it when the search already measured that
// absence with nothing else in flight. What is left here is a boundary found
// inside a concurrent wave with no budget left to re-ask it alone — which is
// the one case where the doubt is real.
//
// Both wrap ErrIncompleteEpisodeList, so nothing that asks only "may this be
// short?" changes behaviour; this one additionally wraps
// ErrUnconfirmedEpisodeList, which is what cmd/episodes.go switches its warning
// code on.
func (p *AnimeOnsen) unconfirmed(contentID string, n int, reason string) (int, error) {
	if n < 1 {
		// Not reachable from the current call sites — a located boundary means
		// at least episode 1 answered — but the weak form must not be able to
		// claim a confirmed-looking zero either.
		return p.short(contentID, n, reason)
	}
	return n, fmt.Errorf("%w: %w: animeonsen: %s, so the %d episodes measured are reported without a second look at whether %q has an episode %d",
		ErrIncompleteEpisodeList, ErrUnconfirmedEpisodeList, reason, n, contentID, n+1)
}

// expiredBudgetReason is how a measurement the probe budget cut off is worded
// for short(). One function so the three places that can reach it — the check
// between waves, the check between confirmation rounds, and a request the
// deadline cancelled mid-flight — cannot drift into saying different things
// about the same event.
func expiredBudgetReason() string {
	return fmt.Sprintf("the episode probe exceeded %s", animeOnsenProbeBudget)
}

// searchBoundary finds the highest present episode above floor, which is
// itself known present (0: nothing is known yet).
//
// The second return is empty when the boundary is a real one — the returned
// episode answered 200 and the next one 404 — and otherwise says in words why
// the measurement stopped where it did: the animeOnsenMaxEpisodes ceiling, the
// probe budget, or a wave the CDN shed entirely. episodeCount turns that into
// the ErrIncompleteEpisodeList flag that reaches `episodes`' JSON.
//
// Which of the two flags it gets is decided by that emptiness and nothing else,
// so it is worth stating what emptiness implies. Both clean returns leave
// hi == lo+1: the narrowing loop exits only on that condition, and the arm
// above it returns a reason whenever hi is still 0. So an empty reason means
// the next episode was measured absent at least once, which is exactly the
// observation episodeCount's confirmation probe re-tests — and a non-empty one
// means it never was, which is the state where episodes really are missing
// from the list.
func (p *AnimeOnsen) searchBoundary(contentID string, floor int, deadline time.Time) (int, string, bool, error) {
	expired := func() error {
		if p.now().After(deadline) {
			return errors.New(expiredBudgetReason())
		}
		return nil
	}
	// lo is the highest episode known to exist. hi is the lowest known not to
	// (0: not yet found).
	lo, hi := floor, 0
	// hiAlone records whether hi's absence was measured by a request that was
	// the only one in flight. It is paired with hi and reassigned with it, so
	// it always describes the episode currently believed absent rather than
	// some earlier, higher one.
	hiAlone := false
	note := func(present, alone map[int]bool) {
		for n, ok := range present {
			if ok {
				if n > lo {
					lo = n
				}
			} else if hi == 0 || n < hi {
				hi, hiAlone = n, alone[n]
			}
		}
		// Contiguity: a present episode above the first absent one would
		// contradict the scheme. Trust the absent one, which is the direction
		// that under-reports — and which the confirmation probe in
		// episodeCount then re-tests, because a shed burst produces exactly
		// this shape.
		if hi != 0 && lo >= hi {
			lo = hi - 1
		}
	}

	// Bracket.
	for step := 1; hi == 0 && floor+step <= animeOnsenMaxEpisodes; {
		if err := expired(); err != nil {
			return lo, err.Error(), false, nil
		}
		ns := make([]int, 0, animeOnsenProbeWidth)
		for len(ns) < animeOnsenProbeWidth && floor+step <= animeOnsenMaxEpisodes {
			ns = append(ns, floor+step)
			step *= 2
		}
		wasLo, wasHi := lo, hi
		present, alone, unanswered, err := p.probeSet(contentID, ns, deadline)
		if err != nil {
			return 0, "", false, err
		}
		note(present, alone)
		if unanswered && lo == wasLo && hi == wasHi {
			// Every probe in the wave went unanswered, so the wave taught
			// nothing. Widening and asking again would spend the budget
			// learning nothing a second time; stop and say the measurement is
			// short. The budget is checked first so a wave the deadline cut
			// off is reported as the expiry it was, not as throttling.
			if err := expired(); err != nil {
				return lo, err.Error(), false, nil
			}
			return lo, "the CDN throttled the availability probe", false, nil
		}
		if lo == floor && floor == 0 {
			// Episode 1 itself answered absent, which is also what a bogus
			// content id answers. Either way this looks like "the catalogue
			// has nothing here", not a transport failure, so it must not
			// poison the fallback chain.
			//
			// But it is the same 404, from the same burst, as the one that
			// cost two episodes of a 12-episode series — and here it costs
			// the whole show: the provider would report ErrNoResults for a
			// series it can stream. So it is confirmed by a solitary probe
			// too, and a 200 means the wave lied and episode 1 is the floor.
			//
			// This arm is reachable only on the first round; a later round has
			// already watched this content id serve an episode, so "nothing
			// here" is no longer an available conclusion.
			if err := expired(); err != nil {
				return lo, err.Error(), false, nil
			}
			ok, err := p.probeEpisode(contentID, 1, deadline)
			if err != nil {
				if errors.Is(err, errAnimeOnsenThrottled) {
					// A shed probe of episode 1 must not become "animeonsen
					// does not have this show". That is the reading the
					// fallback chain treats as final, and it would retire a
					// series lobster can stream over one refused request.
					//
					// A probe the budget cancelled needs no arm of its own:
					// it falls through to the plain error below, which is
					// also not ErrNoResults, and there is nothing measured to
					// prefer over asking the chain. An arm here would only
					// reword it, and would be a line no test could reach.
					return 0, "the CDN would not answer whether episode 1 exists", false, nil
				}
				return 0, "", false, err
			}
			if !ok {
				return 0, "", false, fmt.Errorf("%w: animeonsen has no episodes for %q", ErrNoResults, contentID)
			}
			lo, floor, step = 1, 1, 1
			if hi != 0 && hi <= lo {
				// The wave's own upper probes are no more trustworthy than
				// the one just disproved; bracket again from the new floor.
				hi, hiAlone = 0, false
			}
			continue
		}
	}
	if hi == 0 {
		// Everything up to the ceiling answered. Report what was measured
		// rather than widening forever, and say that is what happened.
		return lo, fmt.Sprintf("every episode up to the %d-episode ceiling answered", animeOnsenMaxEpisodes), false, nil
	}

	// Narrow.
	for hi-lo > 1 {
		if err := expired(); err != nil {
			return lo, err.Error(), false, nil
		}
		gap := hi - lo - 1 // unknown episodes strictly between lo and hi
		ns := make([]int, 0, animeOnsenNarrowWidth)
		seen := map[int]bool{}
		for k := 1; k <= animeOnsenNarrowWidth; k++ {
			n := lo + k*(gap+1)/(animeOnsenNarrowWidth+1)
			if n <= lo || n >= hi || seen[n] {
				continue
			}
			seen[n] = true
			ns = append(ns, n)
		}
		if len(ns) == 0 {
			// Only reachable if the arithmetic above stopped making progress,
			// which would otherwise be an infinite loop. Report the measured
			// lower bound instead of spinning.
			return lo, "", hiAlone, nil
		}
		wasLo, wasHi := lo, hi
		present, alone, unanswered, err := p.probeSet(contentID, ns, deadline)
		if err != nil {
			return 0, "", false, err
		}
		note(present, alone)
		if unanswered && lo == wasLo && hi == wasHi {
			// Same as in the bracket: nothing was learnt, so narrowing again
			// would only burn budget. lo is still a measured episode.
			if err := expired(); err != nil {
				return lo, err.Error(), false, nil
			}
			return lo, "the CDN throttled the availability probe", false, nil
		}
	}
	return lo, "", hiAlone, nil
}

// GetSeasons reports the single season AnimeOnsen models. Like every anime
// source here, a second season is a separate catalogue entry with its own
// content id, not a season of this one.
func (p *AnimeOnsen) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}

// GetEpisodes lists the episodes the CDN actually served a manifest for.
//
// It returns a list *and* an error when enumeration could not be carried to a
// confirmed boundary: the error wraps ErrIncompleteEpisodeList, the list is
// everything that was measured, and cmd/episodes.go keeps the list while
// saying in its JSON that it may be short. Every other error returns no list.
func (p *AnimeOnsen) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	n, err := p.episodeCount(id)
	if err != nil && !errors.Is(err, ErrIncompleteEpisodeList) {
		return nil, fmt.Errorf("episodes: %w", err)
	}
	out := make([]media.Episode, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, media.Episode{
			Number: i,
			Title:  fmt.Sprintf("Episode %d", i),
			ID:     strconv.Itoa(i),
		})
	}
	if err != nil {
		return out, fmt.Errorf("episodes: %w", err)
	}
	return out, nil
}

// Watch resolves an episode to its DASH manifest.
//
// Native episode IDs are bare episode numbers. The fallback resolver instead
// passes "showID:season:episode" (resolver.tryStreamProviderFallback), and ""
// for a film or a request with no season — parseFallbackEpisodeRef reads both,
// and refuses season > 1, because here that is a different show.
//
// It reads them rather than looking them up, which is the correction this
// carries. resolveNumericEpisodeID resolves such an ID through the provider's
// own episode catalogue, and here that catalogue is a probe: GetEpisodes HEADs
// its way to the end of the series and returns its measured prefix *together
// with* ErrIncompleteEpisodeList whenever it could not reach a confirmed
// boundary. resolveNumericEpisodeID treats any error as fatal, so playback
// failed precisely when the shedding warning fired — and an episode above the
// prefix was "not found" while a solitary HEAD of its manifest answers 200.
//
// Nothing is lost by reading the number instead. On this source the native
// episode ID *is* the episode number (manifestURL interpolates it), so the
// lookup only ever reproduced the number it was handed; and the one real check
// it also made — that the episode exists — is made below by a solitary probe
// of that episode's own manifest, which is both the oracle the enumeration is
// built out of and not bounded by how far the enumeration got.
//
// server and quality are accepted and ignored, honestly: there is one server
// and one 720p rendition. Quality is reported as what the manifest actually
// contains rather than echoing back what was asked for.
func (p *AnimeOnsen) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	var n int
	if episodeID == "" || strings.Contains(episodeID, ":") {
		// The show the ref names is not used in place of mediaID: the
		// manifest is fetched under the ID the caller asked to play, which is
		// what this provider did before and what resolver.Resolve matched on.
		_, epNum, err := parseFallbackEpisodeRef(mediaID, episodeID)
		if err != nil {
			return nil, err
		}
		n = epNum
	} else {
		var err error
		if n, err = strconv.Atoi(episodeID); err != nil {
			return nil, fmt.Errorf("animeonsen: bad episode id %q", episodeID)
		}
	}
	if n < 1 {
		return nil, fmt.Errorf("animeonsen: bad episode id %q", episodeID)
	}
	ok, err := p.episodeAvailable(mediaID, n)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("animeonsen: no episode %d for %s", n, mediaID)
	}
	return &media.Stream{
		URL:       p.manifestURL(mediaID, n),
		Referer:   animeOnsenReferer,
		UserAgent: animeOnsenUA,
		Quality:   "720",
	}, nil
}

// --- remaining Provider surface ---

// GetDetails returns nothing. /v4/content/<id> is 401 for an unauthenticated
// client, and an empty detail is the honest answer; inventing one from the
// search row would put a synopsis-shaped blank in front of the user.
func (p *AnimeOnsen) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

func (p *AnimeOnsen) GetServers(id, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AnimeOnsen", ID: "default"}}, nil
}

func (p *AnimeOnsen) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("animeonsen: use Watch")
}

// Trending and Recent have no unauthenticated endpoint (/v4/content/index is
// 401), so they report nothing rather than reaching for a different source's
// idea of what is trending.
func (p *AnimeOnsen) Trending(mt media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (p *AnimeOnsen) Recent(mt media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
