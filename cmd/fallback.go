package cmd

import (
	"fmt"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// fallbackProviders returns a list of fallback providers to try when
// the primary provider's servers all fail. The primary provider is excluded.
func fallbackProviders(primary provider.Provider) []provider.StreamProvider {
	var fallbacks []provider.StreamProvider

	// Soap2Day is the main fallback for movies/TV
	if _, ok := primary.(*provider.Soap2Day); !ok {
		fallbacks = append(fallbacks, provider.NewSoap2Day())
	}

	return fallbacks
}

// tryFallbackStream attempts to resolve a stream using fallback providers.
// It searches for the title on each fallback provider, finds the matching
// season/episode, and tries to get a stream via Watch().
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

// tryFallbackProvider tries a single fallback StreamProvider.
func tryFallbackProvider(fb provider.StreamProvider, title string, mediaType media.MediaType, season, episode int) (*media.Stream, error) {
	// Search for the title
	results, err := fb.Search(title)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Find best match: same type
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

	if mediaType == media.Movie || (season == 0 && episode == 0) {
		// Movie
		quality := "1080"
		if cfg != nil && cfg.Quality != "" {
			quality = cfg.Quality
		}
		return fb.Watch(match.ID, "", "Default", quality)
	}

	// TV: need to construct the episode ID that Watch() expects
	// For Soap2Day, Watch expects episodeID in format "tmdbID:season:episode"
	// The match.ID is "tv/{tmdbID}", so extract tmdbID
	tmdbID := match.ID
	if idx := len("tv/"); len(tmdbID) > idx && tmdbID[:idx] == "tv/" {
		tmdbID = tmdbID[idx:]
	} else if idx := len("movie/"); len(tmdbID) > idx && tmdbID[:idx] == "movie/" {
		tmdbID = tmdbID[idx:]
	}

	episodeID := fmt.Sprintf("%s:%d:%d", tmdbID, season, episode)
	debugf("fallback episode ID: %s", episodeID)

	quality := "1080"
	if cfg != nil && cfg.Quality != "" {
		quality = cfg.Quality
	}
	return fb.Watch(match.ID, episodeID, "Default", quality)
}
