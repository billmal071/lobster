package resolver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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

// normalize lowercases and trims a title for comparison.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
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
