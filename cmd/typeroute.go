package cmd

import (
	"context"
	"strconv"
	"strings"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

// newYTSProvider builds the YTS provider the movie route uses. A package var,
// seamed like agentProvider and agentResolveAndPlay (cmd/play.go), so tests can
// route to a stub instead of reaching the real API.
var newYTSProvider = func() provider.Provider { return provider.NewYTS() }

// ytsRouteTimeout bounds the one YTS lookup routeByType makes. It matches
// multiSearchTimeout, which bounds find's fan-out over the whole fallback
// chain: a single extra search must not cost more than searching everything.
// A var, not a const, so tests can shrink it and stay deterministic.
//
// It is a ceiling, not a cost: the lookup returns as soon as YTS answers, and
// only a YTS that is slow or unreachable spends the whole 5s. Kept at 5s
// deliberately, over the two alternatives:
//
//   - Shrinking it trades a rare wait for a silent downgrade. A YTS that
//     would have answered in 3s now misses its deadline, and the run plays
//     from the scraper with nothing but a debugf to say why.
//   - Racing it against the primary means committing to a source before the
//     answer is in, and switching afterwards would mean restarting playback.
//
// The exposure is also narrower than the number suggests. The route runs from
// resolveAndPlay only, so `find` and `episodes` never reach it; --json and
// --download return above before the lookup; and a detached `play` returns at
// playDetached (cmd/play.go), which is before agentResolveAndPlay, so the wait
// falls in the child after the caller has been answered. What remains is an
// attached play of a movie against an unhealthy YTS: at most 5s, then it
// plays from the primary.
var ytsRouteTimeout = multiSearchTimeout

// baseIsAuto reports whether the user has expressed no source preference, so
// lobster may pick one per content type.
//
// The signal is the config.BaseAuto sentinel, which is Base's default value.
// It is deliberately a value rather than a "was the flag passed" check: --base
// and a config.toml `base =` are the same act of choosing, and cfg.Base is
// where both land (applyConfig, cmd/root.go). A ref carries the base it was
// found under and applyRefBase copies it back into cfg.Base, so a ref minted
// under auto still routes by type, and a ref minted under an explicit base
// still pins that base — which is what playRef promises.
func baseIsAuto() bool { return cfg == nil || cfg.Base == config.BaseAuto }

// routeByType picks the provider for a selection whose media type is now
// known, and returns it alongside the selection to play through it.
//
// This is a *selection-time* decision, not a startup one, and that is why it
// does not live in newProvider (cmd/provider.go). newProvider runs before any
// query, when no type exists to route on, and one interactive search returns
// movies and series interleaved in a single list (gatherSearchResults merges
// every provider's rows and each row carries its own media.Type), so the type
// is not known until the user picks a row. It is not in the resolver either: the resolver races a chain by
// health and has no notion of a preferred source, so "prefer YTS" expressed
// there would be a suggestion rather than a route. resolveAndPlay is the one
// funnel every selection passes through — the interactive picker, the TUI, and
// `play --ref` via agentResolveAndPlay — so it is where the type is first
// known for every path at once.
//
// Movies prefer YTS: it serves complete release-group encodes over BitTorrent
// rather than whatever a scraped embed host happens to hold, so the file is
// the one the release was made from and the bitrate is not re-encoded down.
// Note what this does NOT claim: YTS stocks 2160p for some films, but
// pickTorrent (internal/provider/yts.go) matches the *requested* quality
// before falling back to the highest available, and config.Default().Quality
// is 1080 — so a default install gets 1080p from YTS just as it would from a
// scraper, and the 2160p rung is reachable only with `-q best`.
//
// Series never reach it — YTS has no TV catalogue at all ("no seasons found",
// measured 2026-09-09) — so a series is left with the provider that found it.
//
// An explicit base wins for both types (baseIsAuto), and so do --download and
// --json: YTS resolves to a magnet, which the download path refuses outright
// (streamToResultChecked, cmd/fallback.go) and which a --json consumer cannot
// open at all, so routing either to it would turn a working answer into an
// error message or an unusable URI.
//
// The YTS ID is looked up by title and year rather than assumed, because IDs are not
// portable: YTS's are "yts/<numeric>" and it rejects anything else
// (movieByID). The lookup borrows seasonSource's shape (cmd/episodes.go) —
// rank with resolver.Candidates, then admit only on resolver.Matches, under a
// context deadline. Matches is the load-bearing half: Candidates applies no
// score threshold, so without it the first film YTS returned for the query
// would be played under the selected title. When nothing matches, or the
// lookup times out, the original provider is returned unchanged — so a title
// YTS does not carry still plays from wherever it was found.
//
// The year check on top of Matches is the remake gate. Matches compares ID,
// media type and normalized title and never looks at Year (internal/resolver/
// probe.go), and Candidates ranks year-aware but applies no threshold — so a
// YTS catalogue holding only Dune (1984) both ranks first for and is admitted
// as a selection of Dune (2021). Since only the ID moves, the swap is
// invisible downstream: the wrong film is played, filed and resumed under the
// right title. Requiring the years to agree is the only place that can catch
// it, and a missing or unparsable year on either side refuses to route at all
// — the cost is a stream from the primary, which is what an unrouted movie
// gets anyway.
// yearsAgree reports whether two release years describe the same work,
// allowing one year of slack: catalogues disagree by a year over festival
// versus general release, and over a December film listed under the following
// year. An empty or unparsable year on either side is not a disagreement but
// an absence of evidence, and this returns false for it — the caller is
// choosing whether to swap one film for another, where "we cannot tell" must
// mean "do not".
func yearsAgree(a, b string) bool {
	ai, err1 := strconv.Atoi(strings.TrimSpace(a))
	bi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil {
		return false
	}
	d := ai - bi
	if d < 0 {
		d = -d
	}
	return d <= 1
}

func routeByType(p provider.Provider, sel media.SearchResult) (provider.Provider, media.SearchResult) {
	if !baseIsAuto() {
		return p, sel
	}
	// A live channel is neither a movie nor a series, but its SearchResult is
	// typed media.Movie all the same (internal/provider/livetv.go, channelResult).
	// Without this guard the route would look a channel up in a film catalogue.
	if _, isLive := p.(*provider.LiveTV); isLive {
		return p, sel
	}

	if sel.Type == media.TV {
		// Defensive rather than live: nothing reaches here with a YTS primary
		// today. This route only runs under config.BaseAuto, newProvider maps
		// BaseAuto to autoBase ("soap2day", cmd/provider.go), and an explicit
		// --base yts returns above — YTS is otherwise only a fallback *search*
		// source (fallbackProviders, cmd/fallback.go), never the provider a
		// selection is played through. It stays as insurance against autoBase
		// becoming a movie-only source. Note that sel goes back with its ID
		// untouched, so if this ever does fire the replacement is handed an ID
		// another provider minted: resolveAndPlay's TV branch treats an empty
		// or failing GetSeasons as "no season data" and falls through to
		// tryFallbackStream, which resolves by title, so the cost is a wasted
		// call rather than the wrong series.
		if _, isYTS := p.(*provider.YTS); isYTS {
			debugf("route: %q is a series and YTS has no TV catalogue; using the configured source instead", sel.Title)
			return newProvider(), sel
		}
		return p, sel
	}

	if flagDownload != "" {
		return p, sel
	}
	// --json is the same class of caller as --download: it wants something it
	// can act on, and a magnet is not it. playStream's JSON branch runs before
	// the magnet is handed to the local torrent server (cmd/search.go), so a
	// routed --json run emits the raw magnet URI as its "url" — which nothing
	// consuming --json can open — and a null "subtitles", since YTS carries
	// none. Leaving the primary in place gives the caller a playable HTTP URL
	// and whatever subtitles the scraper found.
	if flagJSON {
		return p, sel
	}
	if _, isYTS := p.(*provider.YTS); isYTS {
		return p, sel
	}

	y := newYTSProvider()
	ctx, cancel := context.WithTimeout(context.Background(), ytsRouteTimeout)
	defer cancel()
	results, err := searchWithContext(ctx, y, sel.Title)
	if err != nil {
		debugf("route: YTS lookup for %q failed (%v); keeping %T", sel.Title, err, p)
		return p, sel
	}

	req := resolver.Request{ID: sel.ID, Title: sel.Title, Year: sel.Year, MediaType: media.Movie}
	for _, c := range resolver.Candidates(results, req) {
		if !resolver.Matches(c, req) {
			debugf("route: YTS offered %q, which is not %q", c.Title, sel.Title)
			continue
		}
		if !yearsAgree(c.Year, sel.Year) {
			debugf("route: YTS offered %q (%s), but the selection is from %s; keeping %T",
				c.Title, c.Year, sel.Year, p)
			continue
		}
		// Only the ID moves. Title, Year and Poster stay as the user saw them
		// in the picker, and they are what history, the resume checkpoint and
		// the subtitle search key on; Matches has already established the two
		// name the same work, so keeping them costs no accuracy.
		routed := sel
		routed.ID = c.ID
		debugf("route: playing movie %q from YTS as %s", sel.Title, c.ID)
		return y, routed
	}
	debugf("route: YTS has no match for %q; keeping %T", sel.Title, p)
	return p, sel
}
