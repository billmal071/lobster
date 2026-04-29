package cmd

import (
	"fmt"

	"lobster/internal/extract"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// fallbackProviders returns all available fallback providers, excluding the primary.
// Both StreamProviders (Soap2Day, Consumet) and regular Providers (FlixHQ, FlixHQWS)
// are included so the app tries every source before giving up.
func fallbackProviders(primary provider.Provider) []provider.Provider {
	var fallbacks []provider.Provider

	if _, ok := primary.(*provider.Soap2Day); !ok {
		fallbacks = append(fallbacks, provider.NewSoap2Day())
	}

	if _, ok := primary.(*provider.FlixHQWS); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQWS("flixhq.ws"))
	}

	if _, ok := primary.(*provider.FlixHQ); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQ("flixhq.to"))
	}

	return fallbacks
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

	// Find best match: prefer same media type
	var match *media.SearchResult
	for i, r := range results {
		if r.Type == mediaType {
			match = &results[i]
			break
		}
	}
	if match == nil && len(results) > 0 {
		match = &results[0]
	}
	if match == nil {
		return nil, fmt.Errorf("no matching result for %q", title)
	}

	debugf("fallback matched: %s (ID: %s)", match.Title, match.ID)

	quality := "1080"
	if cfg != nil && cfg.Quality != "" {
		quality = cfg.Quality
	}

	// StreamProvider path: use Watch() directly
	if sp, ok := fb.(provider.StreamProvider); ok {
		return tryStreamProviderFallback(sp, match, mediaType, season, episode, quality)
	}

	// Regular Provider path: GetServers + GetEmbedURL + Extract
	return tryEmbedProviderFallback(fb, match, mediaType, season, episode, quality)
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

		ext, resolvedURL := extract.ResolveForURL(embedURL)
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
