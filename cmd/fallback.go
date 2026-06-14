package cmd

import (
	"fmt"
	"os"
	"strings"

	"lobster/internal/dlmanager"
	"lobster/internal/extract"
	"lobster/internal/media"
	"lobster/internal/provider"
)

const maxFallbackCandidates = 5

// fallbackProviders returns all available fallback providers, excluding the primary.
// Both StreamProviders (Soap2Day, Consumet, MovieBox, TBCPL) and regular
// Providers (FlixHQ, FlixHQWS) are included so the app tries every source
// before giving up.
func fallbackProviders(primary provider.Provider) []provider.Provider {
	var fallbacks []provider.Provider

	if _, ok := primary.(*provider.VaPlayer); !ok {
		fallbacks = append(fallbacks, provider.NewVaPlayer())
	}

	if _, ok := primary.(*provider.VidNest); !ok {
		fallbacks = append(fallbacks, provider.NewVidNest())
	}

	if _, ok := primary.(*provider.Soap2Day); !ok {
		fallbacks = append(fallbacks, provider.NewSoap2Day())
	}

	if _, ok := primary.(*provider.MovieBox); !ok {
		fallbacks = append(fallbacks, provider.NewMovieBox())
	}

	if _, ok := primary.(*provider.TBCPL); !ok {
		fallbacks = append(fallbacks, provider.NewTBCPL("tbcpl"))
	}

	if _, ok := primary.(*provider.FlixHQWS); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQWS("flixhq.ws"))
	}

	if _, ok := primary.(*provider.FlixHQ); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQ("flixhq.to"))
	}

	if _, ok := primary.(*provider.KimCartoon); !ok {
		fallbacks = append(fallbacks, provider.NewKimCartoon("kimcartoon.com.co"))
	}

	return fallbacks
}

func resolveQuality() string {
	if cfg != nil && cfg.Quality != "" {
		return cfg.Quality
	}
	return "1080"
}

func markPermanentSkip(sessionSkip map[string]bool, name string, err error) {
	if sessionSkip == nil || err == nil || !isPermanentProviderError(err) {
		return
	}
	sessionSkip[name] = true
}

func logStreamResolved(name string) {
	if name == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "Stream resolved via %s\n", name)
}

func isEmbedPrimary(p provider.Provider) bool {
	switch p.(type) {
	case *provider.FlixHQ, *provider.FlixHQWS, *provider.KimCartoon:
		return true
	default:
		return false
	}
}

// tryPrimaryEmbedFromContext resolves via GetServers on known content/episode IDs
// without re-searching by title. Used when the user already picked an episode.
func tryPrimaryEmbedFromContext(primary provider.Provider, contentID, episodeID string) (*media.Stream, string, error) {
	if !isEmbedPrimary(primary) || contentID == "" {
		return nil, "", fmt.Errorf("embed context not available")
	}
	stream, err := tryEmbedServers(primary, contentID, episodeID, resolveQuality())
	if err != nil {
		return nil, "", err
	}
	name := providerDisplayName(primary)
	logStreamResolved(name)
	return stream, name, nil
}

// tryEmbedServers resolves a stream via GetServers + GetEmbedURL + Extract.
func tryEmbedServers(fb provider.Provider, contentID, episodeID, quality string) (*media.Stream, error) {
	servers, err := fb.GetServers(contentID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("getting servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers found")
	}

	for _, srv := range servers {
		debugf("embed trying server: %s (ID: %s)", srv.Name, srv.ID)

		embedURL, err := fb.GetEmbedURL(srv.ID)
		if err != nil {
			debugf("embed server %s failed: %v", srv.Name, err)
			continue
		}

		ext, resolvedURL := extract.ResolveForURL(embedURL, providerReferer(fb))
		stream, err := ext.Extract(resolvedURL, quality)
		if err != nil {
			debugf("embed server %s extract failed: %v", srv.Name, err)
			continue
		}

		debugf("embed stream URL: %s (server: %s)", stream.URL, srv.Name)
		return stream, nil
	}

	return nil, fmt.Errorf("all embed servers failed")
}

// tryFallbackStream resolves a stream using the primary provider, then each
// fallback in order. A provider is skipped when it appears in transientSkip
// (e.g. playback failed for it this episode) or sessionSkip (permanent resolve
// failures earlier this session). Permanent failures are recorded into
// sessionSkip so later episodes skip dead providers instead of retrying them.
func tryFallbackStream(primary provider.Provider, title string, mediaType media.MediaType, season, episode int, transientSkip map[string]bool, sessionSkip map[string]bool) (*media.Stream, string, error) {
	var tries []providerAttempt

	skipped := func(name string) bool {
		return transientSkip[name] || sessionSkip[name]
	}

	primaryName := providerDisplayName(primary)
	if !skipped(primaryName) {
		debugf("trying primary provider: %T", primary)
		stream, err := tryFallbackProvider(primary, title, mediaType, season, episode)
		if err == nil {
			logStreamResolved(primaryName)
			return stream, primaryName, nil
		}
		debugf("primary %T failed: %v", primary, err)
		markPermanentSkip(sessionSkip, primaryName, err)
		tries = append(tries, providerAttempt{
			Name: primaryName,
			Role: "primary",
			Err:  err,
		})
	}

	fallbacks := fallbackProviders(primary)
	for _, fb := range fallbacks {
		name := providerDisplayName(fb)
		if skipped(name) {
			debugf("skipping provider %s (marked skip)", name)
			continue
		}

		debugf("trying fallback provider: %T", fb)
		stream, err := tryFallbackProvider(fb, title, mediaType, season, episode)
		if err != nil {
			debugf("fallback %T failed: %v", fb, err)
			markPermanentSkip(sessionSkip, name, err)
			tries = append(tries, providerAttempt{
				Name: name,
				Role: "fallback",
				Err:  err,
			})
			continue
		}
		logStreamResolved(name)
		return stream, name, nil
	}

	return nil, "", newStreamResolveError(episodeLabel(title, season, episode), tries)
}

// tryFallbackProvider tries a single fallback provider.
// If the provider implements StreamProvider, it uses Watch() directly.
// Otherwise, it uses the GetServers + GetEmbedURL + Extract path.
func tryFallbackProvider(fb provider.Provider, title string, mediaType media.MediaType, season, episode int) (*media.Stream, error) {
	results, err := fb.Search(title)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	candidates := fallbackCandidates(results, mediaType)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no matching result for %q", title)
	}

	quality := resolveQuality()

	var lastErr error
	for i := range candidates {
		match := &candidates[i]
		debugf("fallback candidate: %s (ID: %s)", match.Title, match.ID)

		var stream *media.Stream
		if sp, ok := fb.(provider.StreamProvider); ok {
			stream, err = tryStreamProviderFallback(sp, match, mediaType, season, episode, quality)
		} else {
			stream, err = tryEmbedProviderFallback(fb, match, mediaType, season, episode, quality)
		}
		if err == nil {
			return stream, nil
		}
		lastErr = err
		debugf("fallback candidate %s failed: %v", match.Title, err)
	}

	return nil, fmt.Errorf("all %d candidates failed: %w", len(candidates), lastErr)
}

func fallbackCandidates(results []media.SearchResult, mediaType media.MediaType) []media.SearchResult {
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

	candidates := sameType
	if len(candidates) == 0 {
		candidates = otherType
	}
	if len(candidates) > maxFallbackCandidates {
		candidates = candidates[:maxFallbackCandidates]
	}
	return candidates
}

// tryStreamProviderFallback resolves a stream via StreamProvider.Watch().
func tryStreamProviderFallback(sp provider.StreamProvider, match *media.SearchResult, mediaType media.MediaType, season, episode int, quality string) (*media.Stream, error) {
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
	debugf("fallback episode ID: %s", episodeID)
	return sp.Watch(match.ID, episodeID, "Default", quality)
}

// tryEmbedProviderFallback resolves a stream via GetServers + GetEmbedURL + Extract.
func tryEmbedProviderFallback(fb provider.Provider, match *media.SearchResult, mediaType media.MediaType, season, episode int, quality string) (*media.Stream, error) {
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
		if seasonID == "" && len(seasons) > 0 {
			seasonID = seasons[0].ID
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

	return tryEmbedServers(fb, match.ID, episodeID, quality)
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

// makeStreamResolver builds a StreamResolver that tries the primary provider
// and all fallbacks to resolve a stream URL for downloads.
func makeStreamResolver(primary provider.Provider) dlmanager.StreamResolver {
	return func(title, mediaID, episodeID, mediaType string, season, episode int) (*dlmanager.StreamResult, error) {
		mt := media.Movie
		if mediaType == "tv" {
			mt = media.TV
		}

		fbStream, _, err := tryFallbackStream(primary, title, mt, season, episode, nil, nil)
		if err != nil {
			return nil, err
		}
		return streamToResult(fbStream), nil
	}
}

// streamToResult converts a media.Stream to a dlmanager.StreamResult.
func streamToResult(s *media.Stream) *dlmanager.StreamResult {
	streamType := "http"
	if strings.Contains(s.URL, ".m3u8") || strings.Contains(s.URL, "hls") {
		streamType = "hls"
	}
	return &dlmanager.StreamResult{
		URL:        s.URL,
		StreamType: streamType,
		Referer:    s.Referer,
	}
}
