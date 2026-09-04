package resolver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"lobster/internal/extract"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// probeResult is the outcome of a single probe call.
type probeResult struct {
	Stream   *media.Stream
	Provider string
	Stage    string
	Err      error
	Latency  time.Duration
}

// probe runs resolveWithProvider for provider p, retrying once on a transient
// error, optionally validating the stream, and recording health.
func (r *Resolver) probe(p provider.Provider, req Request) probeResult {
	name := ProviderName(p)
	start := time.Now()
	var (
		stream *media.Stream
		stage  string
		err    error
	)
	// Up to one retry on a transient failure — covering both resolution AND the
	// validation hop, since validation is itself a network call that can fail
	// transiently and discard an otherwise-good stream.
	for attempt := 0; attempt < 2; attempt++ {
		stream, stage, err = resolveWithProvider(p, req, r.log)
		if err == nil && r.validate {
			if verr := validateStream(r.client, stream); verr != nil {
				err, stage = verr, "validate"
				stream = nil
			}
		}
		if err == nil || !isTransient(err) || attempt == 1 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	latency := time.Since(start)
	// Health is recorded by Resolve on the receive side, not here: an abandoned
	// probe that finishes after its batch deadline must NOT self-report a late
	// success (which would keep a chronically-slow provider ranked first forever).
	return probeResult{Stream: stream, Provider: name, Stage: stage, Err: err, Latency: latency}
}

// MaxCandidates is the maximum number of search candidates to try per provider.
const MaxCandidates = 5

// maxCandidates is the package-internal alias.
const maxCandidates = MaxCandidates

// Request parameterizes a single stream-resolution attempt.
//
// ID and Year identify the exact work the user picked. Each provider still
// re-searches by Title in its own catalog (IDs are not portable across all of
// them), so without those two fields the provider's own relevance ordering
// decides — and for a franchise that means whatever sequel is in cinemas right
// now, not the film that was selected.
type Request struct {
	ID        string
	Title     string
	Year      string
	MediaType media.MediaType
	Season    int
	Episode   int
	Quality   string
}

// resolveWithProvider tries to resolve a stream using the given provider and
// request parameters. It returns the stream, the stage reached ("search",
// "match", or "resolve"), and any error. The log function receives debug
// messages in the same format as fmt.Sprintf.
func resolveWithProvider(p provider.Provider, req Request, log func(string, ...any)) (*media.Stream, string, error) {
	results, err := p.Search(req.Title)
	if err != nil {
		return nil, "search", fmt.Errorf("search failed: %w", err)
	}

	candidates := candidatesFor(results, req)
	if len(candidates) == 0 {
		return nil, "match", fmt.Errorf("no matching result for %q", req.Title)
	}

	quality := req.Quality
	if quality == "" {
		quality = "1080"
	}

	var lastErr error
	for i := range candidates {
		match := &candidates[i]
		log("fallback candidate: %s (ID: %s)", match.Title, match.ID)

		var stream *media.Stream
		if sp, ok := p.(provider.StreamProvider); ok {
			stream, err = tryStreamProviderFallback(sp, match, req.MediaType, req.Season, req.Episode, quality, log)
		} else {
			stream, err = tryEmbedProviderFallback(p, match, req.MediaType, req.Season, req.Episode, quality, log)
		}
		if err == nil {
			return stream, "resolve", nil
		}
		lastErr = err
		log("fallback candidate %s failed: %v", match.Title, err)
	}

	return nil, "resolve", fmt.Errorf("all %d candidates failed: %w", len(candidates), lastErr)
}

// FallbackCandidates filters and deduplicates search results, preferring
// results of the requested media type, limited to MaxCandidates.
func FallbackCandidates(results []media.SearchResult, mediaType media.MediaType) []media.SearchResult {
	return fallbackCandidates(results, mediaType)
}

// Candidates ranks results against the work req identifies, best first, capped
// at MaxCandidates. Unlike FallbackCandidates it uses ID, Title and Year rather
// than the provider's own relevance order — the same ranking resolveWithProvider
// applies, exported for callers outside the resolver that re-search a foreign
// catalog by title (cmd.seasonSource).
//
// Ranking is not filtering. Every candidate of the right media type is
// returned, including ones that match nothing about req: within resolution
// that is safe, because a candidate still has to yield a playable stream. A
// caller that only reads metadata off the candidate has no such check and must
// gate on Matches first.
func Candidates(results []media.SearchResult, req Request) []media.SearchResult {
	return candidatesFor(results, req)
}

// Matches reports whether r can plausibly be the work req identifies.
//
// The test is the identity of the work, not the strength of the ranking:
// candidateScore awards points for a year within one of req's, so a score
// above zero is reachable by an unrelated show that happens to share a
// release year. Admission needs an identical ID — conclusive, the provider
// indexes the same catalog — or titles that reduce to the same normalized key.
//
// The rule is equality of keys, never a prefix. A prefix test admits a
// *different* work whose title merely starts with the ref's: "Lost" would take
// "Lost in Space", "Star Trek" would take "Star Trek: Discovery", and the
// envelope would print the ref's own title over the impostor's seasons with
// nothing for the caller to detect. normalize is what absorbs the harmless
// disagreements instead — case, accents, "&" against "and", hyphens, dots and
// apostrophes — so "Rick & Morty", "Spider-Man" and "Pokémon" still meet
// "Rick and Morty", "Spider Man" and "Pokemon".
//
// One controlled inequality survives: a *qualifier* one side carries and the
// other does not — a trailing parenthetical ("The Office (US)") or a
// season/part subtitle ("Attack on Titan: Final Season"). stripQualifier is
// applied to one side at a time and only where it removes something, so the
// side being compared against keeps its own qualifier intact. That is what
// keeps siblings apart: "The Office (US)" against "The Office (UK)" compares
// "the office" to "the office uk" and "the office us" to "the office", and
// fails both ways. An arbitrary subtitle is not a qualifier, so
// "Star Trek: Discovery" is never reduced to "Star Trek" in either direction.
//
// This is deliberately stricter than candidateScore, which still awards a
// point for a bare prefix. Ranking may prefer a near miss; admission may not
// accept one.
//
// # Media type
//
// A candidate of the wrong media type is not the work req identifies, however
// well its title reads. "Spider-Man" is the 2002 film and the 1994 animated
// series under one title, so no title rule can separate them. Nor does ranking
// remove the film: candidatesFor delegates to dedupeByType, which returns the
// *other* type when the requested one is absent, so a provider holding only
// the film offers the film. Without this check cmd.probeSeasons went on to call
// GetSeasons on a movie ID and printed whatever came back as the show's
// seasons, under the ref's own title, exit 0.
//
// Two things about where the check sits and what it assumes:
//
// The check is BELOW the identical-ID return, so an ID match still wins. IDs
// here carry their own type — "tv/1396", "movie/557",
// "series/watch-breaking-bad-39516" — so a candidate that shares a ref's ID
// while disagreeing about its type has contradicted itself, and the ID is the
// half that came from the catalogue rather than from a scraper inferring a type
// out of a URL path. Above the ID return, one mislabelled row would throw away
// the only conclusive evidence of identity there is.
//
// Request.MediaType has no "unspecified": media.Movie is the zero value of
// media.MediaType, so a Request that never sets it asks for a film and this
// gate refuses every TV candidate. Callers must set it. Both production sites
// do — cmd/episodes.go's seasonSource passes media.TV outright, and
// cmd/fallback.go's tryFallbackStream copies Type off the search result the
// user picked — and TestMatchesTreatsAnUnsetRequestMediaTypeAsMovie pins the
// consequence so a third caller finds out from a test rather than from an
// empty season list. Adding a sentinel to media.MediaType was the alternative;
// it renumbers an iota compared across the codebase to fix a hazard with no
// instances, which is the worse trade.
func Matches(r media.SearchResult, req Request) bool {
	if req.ID != "" && r.ID == req.ID {
		return true
	}
	if r.Type != req.MediaType {
		return false
	}
	title, want := normalize(r.Title), normalize(req.Title)
	if title == "" || want == "" {
		return false
	}
	if title == want {
		return true
	}
	// Qualifier stripped from one side only, never both at once.
	if base, ok := stripQualifier(r.Title); ok && base == want {
		return true
	}
	if base, ok := stripQualifier(req.Title); ok && base == title {
		return true
	}
	return false
}

// stripQualifier returns the normalized key of s with an edition qualifier
// removed, and whether one was actually there. A qualifier is a trailing
// parenthesised or bracketed group ("The Office (US)", "Doctor Who (2005)"),
// or a subtitle after a colon or a spaced dash that names a season, part or
// cour rather than a distinct work ("Attack on Titan: Final Season").
//
// Subtitles that are not season designators are left alone precisely so that
// "Star Trek: Discovery" cannot be reduced to "Star Trek".
func stripQualifier(s string) (string, bool) {
	t := strings.TrimSpace(s)
	full := normalize(t)

	for {
		trimmed := trimTrailingGroup(t)
		if trimmed == t {
			break
		}
		t = trimmed
	}

	if head, tail, ok := splitSubtitle(t); ok && isSeasonQualifier(tail) {
		t = head
	}

	key := normalize(t)
	if key == "" || key == full {
		return "", false
	}
	return key, true
}

// trimTrailingGroup removes one trailing "(...)" or "[...]" group from s.
func trimTrailingGroup(s string) string {
	s = strings.TrimSpace(s)
	var open byte
	switch {
	case strings.HasSuffix(s, ")"):
		open = '('
	case strings.HasSuffix(s, "]"):
		open = '['
	default:
		return s
	}
	i := strings.LastIndexByte(s[:len(s)-1], open)
	if i <= 0 {
		return s
	}
	return strings.TrimSpace(s[:i])
}

// splitSubtitle splits s at the first colon or spaced dash separator. The
// dash must be spaced so that "Spider-Man" is one title, not two.
func splitSubtitle(s string) (head, tail string, ok bool) {
	if i := strings.IndexByte(s, ':'); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
	}
	for _, sep := range []string{" - ", " – ", " — "} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(sep):]), true
		}
	}
	return s, "", false
}

// isSeasonQualifier reports whether a subtitle names a slice of the same work
// ("Final Season", "Part 2", "Cour 1") rather than a separate one.
func isSeasonQualifier(tail string) bool {
	for _, w := range strings.Fields(normalize(tail)) {
		switch w {
		case "season", "seasons", "part", "parts", "cour":
			return true
		}
	}
	return false
}

func fallbackCandidates(results []media.SearchResult, mediaType media.MediaType) []media.SearchResult {
	return truncateCandidates(dedupeByType(results, mediaType))
}

// truncateCandidates caps a candidate list at MaxCandidates.
func truncateCandidates(candidates []media.SearchResult) []media.SearchResult {
	if len(candidates) > maxCandidates {
		return candidates[:maxCandidates]
	}
	return candidates
}

// dedupeByType keeps the results matching mediaType (falling back to the others
// when none do), deduplicated, in provider order.
func dedupeByType(results []media.SearchResult, mediaType media.MediaType) []media.SearchResult {
	var sameType []media.SearchResult
	var otherType []media.SearchResult
	seen := make(map[string]bool)

	appendUnique := func(dst []media.SearchResult, r media.SearchResult) []media.SearchResult {
		key := r.ID
		if key == "" {
			key = r.Title + r.URL
		}
		if seen[key] {
			return dst
		}
		seen[key] = true
		return append(dst, r)
	}

	for _, r := range results {
		if r.Type == mediaType {
			sameType = appendUnique(sameType, r)
		} else {
			otherType = appendUnique(otherType, r)
		}
	}

	if len(sameType) > 0 {
		return sameType
	}
	return otherType
}

// candidatesFor filters results to the requested media type, then orders them
// by how well they match the work the user actually picked, best first. The
// provider's own relevance ordering is only the tiebreaker.
func candidatesFor(results []media.SearchResult, req Request) []media.SearchResult {
	// Rank before capping at MaxCandidates: the right match is often outside a
	// provider's own top five for a franchise title.
	candidates := dedupeByType(results, req.MediaType)

	scored := make([]struct {
		result media.SearchResult
		score  int
	}, len(candidates))
	for i, c := range candidates {
		scored[i].result = c
		scored[i].score = candidateScore(c, req)
	}
	sort.SliceStable(scored, func(a, b int) bool { return scored[a].score > scored[b].score })

	ranked := make([]media.SearchResult, len(scored))
	for i, s := range scored {
		ranked[i] = s.result
	}
	return truncateCandidates(ranked)
}

// candidateScore rates how confidently a search result is the requested work.
func candidateScore(r media.SearchResult, req Request) int {
	score := 0

	// An identical ID is conclusive — the provider indexes the same catalog
	// (TMDB IDs, in practice) the selection came from.
	if req.ID != "" && r.ID == req.ID {
		score += 8
	}

	if normalize(r.Title) == normalize(req.Title) {
		score += 4
	} else if strings.HasPrefix(normalize(r.Title), normalize(req.Title)) {
		score++
	}

	// Catalogs disagree by a year on release dates often enough that an exact
	// match is worth more than a near one, but a near one still beats nothing.
	if req.Year != "" && r.Year != "" {
		switch diff := yearDiff(r.Year, req.Year); {
		case diff == 0:
			score += 4
		case diff <= 1:
			score += 2
		}
	}

	return score
}

// normalize reduces a title to a comparison key, so that two catalogs
// punctuating the same work differently still agree. It case-folds, strips
// accents (NFD then drop the combining marks, so "Pokémon" keys as
// "pokemon"), spells "&" as "and", drops apostrophes outright so "Marvel's"
// keys as "marvels", and collapses every other non-alphanumeric run —
// hyphens, colons, dots, brackets — to a single space.
//
// Dropping rather than spacing apostrophes is the one asymmetry: catalogs
// write "Marvels", never "Marvel s". Dots become spaces because
// "S.H.I.E.L.D." is written "S H I E L D" as often as it is elided.
//
// A key can legitimately be empty (a title of pure punctuation); callers must
// treat that as "no title" rather than as something to compare.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A combining mark NFD split off an accented letter.
		case r == '\'' || r == '’':
			// Elided, not spaced.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
		case r == '&':
			// Always a word of its own, so "Rick&Morty" keys like "Rick & Morty".
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("and")
			pendingSpace = true
		default:
			pendingSpace = true
		}
	}
	return b.String()
}

// yearDiff returns the absolute difference between two year strings, or a large
// value if either does not parse.
func yearDiff(a, b string) int {
	ai, err1 := strconv.Atoi(a)
	bi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return 1 << 30
	}
	if d := ai - bi; d < 0 {
		return -d
	} else {
		return d
	}
}

// tryStreamProviderFallback resolves a stream via StreamProvider.Watch().
func tryStreamProviderFallback(sp provider.StreamProvider, match *media.SearchResult, mediaType media.MediaType, season, episode int, quality string, log func(string, ...any)) (*media.Stream, error) {
	if mediaType == media.Movie || (season == 0 && episode == 0) {
		return sp.Watch(match.ID, "", "Default", quality)
	}

	// TV: construct the episode ID
	tmdbID := match.ID
	if idx := len("tv/"); len(tmdbID) > idx && tmdbID[:idx] == "tv/" {
		tmdbID = tmdbID[idx:]
	} else if idx := len("movie/"); len(tmdbID) > idx && tmdbID[:idx] == "movie/" {
		tmdbID = tmdbID[idx:]
	}

	episodeID := fmt.Sprintf("%s:%d:%d", tmdbID, season, episode)
	log("fallback episode ID: %s", episodeID)
	return sp.Watch(match.ID, episodeID, "Default", quality)
}

// tryEmbedProviderFallback resolves a stream via GetServers + GetEmbedURL + Extract.
func tryEmbedProviderFallback(fb provider.Provider, match *media.SearchResult, mediaType media.MediaType, season, episode int, quality string, log func(string, ...any)) (*media.Stream, error) {
	var episodeID string

	if mediaType != media.Movie && (season > 0 || episode > 0) {
		// TV: get seasons and episodes to find the right episode ID
		seasons, err := fb.GetSeasons(match.ID)
		if err != nil {
			return nil, fmt.Errorf("getting seasons: %w", err)
		}

		var seasonID string
		for _, s := range seasons {
			if s.Number == season {
				seasonID = s.ID
				break
			}
		}
		if seasonID == "" {
			return nil, fmt.Errorf("season %d not found", season)
		}

		episodes, err := fb.GetEpisodes(match.ID, seasonID)
		if err != nil {
			return nil, fmt.Errorf("getting episodes: %w", err)
		}

		for _, ep := range episodes {
			if ep.Number == episode {
				episodeID = ep.ID
				break
			}
		}
		if episodeID == "" {
			return nil, fmt.Errorf("episode %d not found in season %d", episode, season)
		}
	}

	servers, err := fb.GetServers(match.ID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("getting servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers found")
	}

	// Try each server
	for _, srv := range servers {
		log("fallback trying server: %s (ID: %s)", srv.Name, srv.ID)

		embedURL, err := fb.GetEmbedURL(srv.ID)
		if err != nil {
			log("fallback server %s embed failed: %v", srv.Name, err)
			continue
		}

		ext, resolvedURL := extract.ResolveForURL(embedURL, providerReferer(fb))
		stream, err := ext.Extract(resolvedURL, quality)
		if err != nil {
			log("fallback server %s extract failed: %v", srv.Name, err)
			continue
		}

		log("fallback stream resolved (server: %s)", srv.Name)
		return stream, nil
	}

	return nil, fmt.Errorf("all fallback servers failed")
}

// providerReferer returns the Referer URL for a provider's embed requests.
// This ensures extractors send the correct origin domain instead of a hardcoded one.
func providerReferer(p provider.Provider) string {
	switch v := p.(type) {
	case *provider.FlixHQ:
		return v.BaseURL() + "/"
	case *provider.FlixHQWS:
		return v.BaseURL() + "/"
	case *provider.KimCartoon:
		return v.BaseURL() + "/"
	default:
		return ""
	}
}
