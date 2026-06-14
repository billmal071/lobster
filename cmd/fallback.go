package cmd

import (
	"fmt"
	"strings"

	"lobster/internal/dlmanager"
	"lobster/internal/extract"
	"lobster/internal/media"
	"lobster/internal/provider"
)

const maxFallbackCandidates = 5

// fallbackProviders returns all available fallback providers, excluding the primary.
// Keep this list limited to sources that can resolve normal movie/series
// streams reliably enough for automatic retries.
func fallbackProviders(primary provider.Provider) []provider.Provider {
	var fallbacks []provider.Provider

if _, ok := primary.(*provider.TBCPL); !ok {
		fallbacks = append(fallbacks, provider.NewTBCPL("tbcpl"))
	}

	// MovieBox removed: play-info endpoint returns 407 (needs app auth).
	// FlixHQ.to removed from automatic fallback: the host routinely times out.
	// KimCartoon is category-specific and should not satisfy normal series.

	if _, ok := primary.(*provider.Soap2Day); !ok {
		fallbacks = append(fallbacks, provider.NewSoap2Day())
	}

	if _, ok := primary.(*provider.FlixHQWS); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQWS("flixhq.ws"))
	}

	return fallbacks
}

// tryPrimaryStream resolves a stream from the selected provider and exact
// media/episode IDs before falling back to title search.
func tryPrimaryStream(p provider.Provider, mediaID, episodeID string, excludeNames map[string]bool) (*media.Stream, string, error) {
	quality := configuredQuality()

	if sp, ok := p.(provider.StreamProvider); ok {
		servers, err := p.GetServers(mediaID, episodeID)
		if err != nil {
			return nil, "", fmt.Errorf("getting primary servers: %w", err)
		}
		if len(servers) == 0 {
			return nil, "", fmt.Errorf("no primary servers found")
		}

		ordered := orderServersWithCache(servers, configuredServer(), cachedServerName)
		var lastErr error
		for _, srv := range ordered {
			if serverExcluded(excludeNames, srv.Name) {
				continue
			}

			debugf("trying primary server: %s (ID: %s)", srv.Name, srv.ID)
			stream, err := sp.Watch(mediaID, episodeID, srv.Name, quality)
			if err == nil {
				cachedServerName = srv.Name
				return stream, srv.Name, nil
			}
			lastErr = err
			debugf("primary server %s failed: %v", srv.Name, err)
		}

		if lastErr == nil {
			lastErr = fmt.Errorf("no primary servers left to try")
		}
		return nil, "", lastErr
	}

	return tryPrimaryEmbedProvider(p, mediaID, episodeID, quality, excludeNames)
}

func tryPrimaryEmbedProvider(p provider.Provider, mediaID, episodeID, quality string, excludeNames map[string]bool) (*media.Stream, string, error) {
	servers, err := p.GetServers(mediaID, episodeID)
	if err != nil {
		return nil, "", fmt.Errorf("getting primary servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, "", fmt.Errorf("no primary servers found")
	}

	ordered := orderServersWithCache(servers, configuredServer(), cachedServerName)
	var lastErr error
	for _, srv := range ordered {
		if serverExcluded(excludeNames, srv.Name) {
			continue
		}

		debugf("trying primary embed server: %s (ID: %s)", srv.Name, srv.ID)
		embedURL, err := p.GetEmbedURL(srv.ID)
		if err != nil {
			lastErr = err
			debugf("primary server %s embed failed: %v", srv.Name, err)
			continue
		}

		ext, resolvedURL := extract.ResolveForURL(embedURL)
		stream, err := ext.Extract(resolvedURL, quality)
		if err != nil {
			lastErr = err
			debugf("primary server %s extract failed: %v", srv.Name, err)
			continue
		}

		cachedServerName = srv.Name
		return stream, srv.Name, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no primary servers left to try")
	}
	return nil, "", lastErr
}

func configuredQuality() string {
	if cfg != nil && cfg.Quality != "" {
		return cfg.Quality
	}
	return "1080"
}

func configuredServer() string {
	if cfg != nil && cfg.Provider != "" {
		return cfg.Provider
	}
	return "Default"
}

func serverExcluded(excludeNames map[string]bool, name string) bool {
	if len(excludeNames) == 0 {
		return false
	}
	if excludeNames[name] {
		return true
	}
	for excluded := range excludeNames {
		if strings.EqualFold(excluded, name) {
			return true
		}
	}
	return false
}

// tryFallbackStream attempts to resolve a stream using fallback providers.
// It tries each fallback in order — StreamProviders use Watch(), regular
// Providers use the GetServers + GetEmbedURL + Extract path.
func tryFallbackStream(primary provider.Provider, title string, mediaType media.MediaType, season, episode int) (*media.Stream, error) {
	fallbacks := fallbackProviders(primary)
	if len(fallbacks) == 0 {
		return nil, fmt.Errorf("no fallback providers available")
	}

	for _, fb := range fallbacks {
		debugf("trying fallback provider: %T", fb)
		stream, err := tryFallbackProvider(fb, title, mediaType, season, episode)
		if err != nil {
			debugf("fallback %T failed: %v", fb, err)
			continue
		}
		return stream, nil
	}

	return nil, fmt.Errorf("all fallback providers failed")
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

	quality := configuredQuality()

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

	servers, err := fb.GetServers(match.ID, episodeID)
	if err != nil {
		return nil, fmt.Errorf("getting servers: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers found")
	}

	// Try each server
	for _, srv := range servers {
		debugf("fallback trying server: %s (ID: %s)", srv.Name, srv.ID)

		embedURL, err := fb.GetEmbedURL(srv.ID)
		if err != nil {
			debugf("fallback server %s embed failed: %v", srv.Name, err)
			continue
		}

		ext, resolvedURL := extract.ResolveForURL(embedURL, providerReferer(fb))
		stream, err := ext.Extract(resolvedURL, quality)
		if err != nil {
			debugf("fallback server %s extract failed: %v", srv.Name, err)
			continue
		}

		debugf("fallback stream URL: %s (server: %s)", stream.URL, srv.Name)
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

// makeStreamResolver builds a StreamResolver that tries the primary provider
// and all fallbacks to resolve a stream URL for downloads.
func makeStreamResolver(primary provider.Provider) dlmanager.StreamResolver {
	return func(title, mediaID, episodeID, mediaType string, season, episode int) (*dlmanager.StreamResult, error) {
		mt := media.Movie
		if mediaType == "tv" {
			mt = media.TV
		}

		if mediaID != "" && (mt == media.Movie || episodeID != "") {
			stream, _, err := tryPrimaryStream(primary, mediaID, episodeID, nil)
			if err == nil {
				return streamToResult(stream), nil
			}
			debugf("primary resolver failed: %v", err)
		}

		fbStream, err := tryFallbackStream(primary, title, mt, season, episode)
		if err != nil {
			return nil, fmt.Errorf("all providers failed: %w", err)
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
