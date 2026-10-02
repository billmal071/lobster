package provider

import (
	"encoding/json"
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
)

// animeOnsenProbeBudget is the probe's own deadline, as a backstop rather than
// as the binding limit. The agent-facing callers bound it from outside and more
// tightly — cmd.episodesFallbackTimeout is 5 s and abandons the call — so what
// this covers is the paths with no deadline of their own: resolver.Resolve's
// 30 s per-attempt budget, and the TUI, which has none at all.
//
// A var, not a const, so a test can shrink it and watch the probe give up
// rather than having to serve thousands of manifests to reach the ceiling.
var animeOnsenProbeBudget = 10 * time.Second

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
// (internal/player/headers.go folds Referer and User-Agent into
// --demuxer-lavf-o=headers=, which lavf's DASH demuxer honours for its segment
// requests exactly as it does for HLS).
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

func (p *AnimeOnsen) do(method, rawURL string) (int, []byte, error) {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", animeOnsenUA)
	req.Header.Set("Referer", animeOnsenReferer)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	if method == http.MethodHead {
		return resp.StatusCode, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, body, err
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
	status, body, err := p.do(http.MethodGet, p.apiBase+"/search/"+url.PathEscape(query))
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

// episodeAvailable reports whether contentID has episode n, by HEADing its
// manifest. A present episode answers 200 and one past the end answers 404 —
// and a bogus content id answers 404 too, so there is no blanket-200 fallback
// to mistake for a hit.
func (p *AnimeOnsen) episodeAvailable(contentID string, n int) (bool, error) {
	status, _, err := p.do(http.MethodHead, p.manifestURL(contentID, n))
	if err != nil {
		return false, err
	}
	switch {
	case status >= 200 && status < 300:
		return true, nil
	case status == http.StatusNotFound:
		return false, nil
	default:
		// 403 (missing/rejected Referer or UA) is not "no such episode", and
		// reporting it as one would silently shorten every episode list.
		return false, fmt.Errorf("animeonsen: manifest status %d", status)
	}
}

// probeSet HEADs every manifest in ns concurrently and reports which answered.
//
// A probe that errors at the transport level, or answers something that is
// neither 2xx nor 404, fails the whole wave: 403 is what a rejected Referer or
// User-Agent looks like here, and reading it as "no such episode" would
// silently shorten every episode list to nothing instead of saying what broke.
func (p *AnimeOnsen) probeSet(contentID string, ns []int) (map[int]bool, error) {
	present := make(map[int]bool, len(ns))
	errs := make([]error, len(ns))
	oks := make([]bool, len(ns))

	var wg sync.WaitGroup
	for i, n := range ns {
		wg.Add(1)
		go func(i, n int) {
			defer wg.Done()
			oks[i], errs[i] = p.episodeAvailable(contentID, n)
		}(i, n)
	}
	wg.Wait()

	for i, n := range ns {
		if errs[i] != nil {
			return nil, errs[i]
		}
		present[n] = oks[i]
	}
	return present, nil
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
// that answered 200, and N+1 answered 404. That is the property this search
// establishes by construction, and it is why this is not the
// fabricated-episode-list problem of #61 — nothing is reported that was not
// probed or bracketed by two probes either side of it. The one exception is
// named in the code: when every episode up to animeOnsenMaxEpisodes answers,
// the ceiling is reported, because that is as far as anything was measured.
//
// # Why it is shaped this way
//
// Both halves of the search probe animeOnsenProbeWidth manifests at a time,
// concurrently, so the cost is in *round trips* rather than in requests:
//
//	bracket  probe 1, 2, 4, 8, ... 2^k in one wave, and keep widening while
//	         everything answers, until some power of two 404s
//	narrow   probe animeOnsenProbeWidth evenly spaced points inside the
//	         bracket at once, shrinking it by a factor of width+1 per wave
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
	expired := func() error {
		if p.now().After(deadline) {
			return fmt.Errorf("animeonsen: episode probe exceeded %s", animeOnsenProbeBudget)
		}
		return nil
	}

	// lo is the highest episode known to exist (0: none yet). hi is the lowest
	// known not to (0: not yet found).
	lo, hi := 0, 0
	note := func(present map[int]bool) {
		for n, ok := range present {
			if ok {
				if n > lo {
					lo = n
				}
			} else if hi == 0 || n < hi {
				hi = n
			}
		}
		// Contiguity: a present episode above the first absent one would
		// contradict the scheme. Trust the absent one, which is the direction
		// that under-reports.
		if hi != 0 && lo >= hi {
			lo = hi - 1
		}
	}

	// Bracket.
	for step := 1; hi == 0 && step <= animeOnsenMaxEpisodes; {
		if err := expired(); err != nil {
			return 0, err
		}
		ns := make([]int, 0, animeOnsenProbeWidth)
		for len(ns) < animeOnsenProbeWidth && step <= animeOnsenMaxEpisodes {
			ns = append(ns, step)
			step *= 2
		}
		present, err := p.probeSet(contentID, ns)
		if err != nil {
			return 0, err
		}
		note(present)
		if lo == 0 {
			// Episode 1 itself is absent, which is also what a bogus content
			// id answers. Either way this is "the catalogue has nothing
			// here", not a transport failure, so it must not poison the
			// fallback chain.
			return 0, fmt.Errorf("%w: animeonsen has no episodes for %q", ErrNoResults, contentID)
		}
	}
	if hi == 0 {
		// Everything up to the ceiling answered. Report what was measured
		// rather than widening forever.
		return lo, nil
	}

	// Narrow.
	for hi-lo > 1 {
		if err := expired(); err != nil {
			return 0, err
		}
		gap := hi - lo - 1 // unknown episodes strictly between lo and hi
		ns := make([]int, 0, animeOnsenProbeWidth)
		seen := map[int]bool{}
		for k := 1; k <= animeOnsenProbeWidth; k++ {
			n := lo + k*(gap+1)/(animeOnsenProbeWidth+1)
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
			return lo, nil
		}
		present, err := p.probeSet(contentID, ns)
		if err != nil {
			return 0, err
		}
		note(present)
	}
	return lo, nil
}

// GetSeasons reports the single season AnimeOnsen models. Like every anime
// source here, a second season is a separate catalogue entry with its own
// content id, not a season of this one.
func (p *AnimeOnsen) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}

func (p *AnimeOnsen) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	n, err := p.episodeCount(id)
	if err != nil {
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
	return out, nil
}

// Watch resolves an episode to its DASH manifest.
//
// Native episode IDs are bare episode numbers. The fallback resolver instead
// passes "showID:season:episode" (resolver.tryStreamProviderFallback), which
// resolveNumericEpisodeID converts — and which refuses season > 1, because
// here that is a different show.
//
// server and quality are accepted and ignored, honestly: there is one server
// and one 720p rendition. Quality is reported as what the manifest actually
// contains rather than echoing back what was asked for.
func (p *AnimeOnsen) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	if episodeID == "" || strings.Contains(episodeID, ":") {
		nid, err := resolveNumericEpisodeID(p.GetEpisodes, mediaID, episodeID)
		if err != nil {
			return nil, err
		}
		episodeID = nid
	}
	n, err := strconv.Atoi(episodeID)
	if err != nil || n < 1 {
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
