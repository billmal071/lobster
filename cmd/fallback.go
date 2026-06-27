package cmd

import (
	"context"
	"fmt"
	"strings"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

var sharedHealth = func() *resolver.HealthStore {
	p, err := config.HealthPath()
	if err != nil {
		return resolver.NewHealthStore()
	}
	return resolver.LoadHealth(p)
}()

func cfgQuality() string {
	if cfg != nil && cfg.Quality != "" {
		return cfg.Quality
	}
	return "1080"
}

// maxFallbackCandidates mirrors resolver.MaxCandidates for backward compatibility
// with tests that reference it in the cmd package.
const maxFallbackCandidates = resolver.MaxCandidates

// fallbackCandidates is a thin shim that delegates to resolver.FallbackCandidates.
// It exists so that cmd tests that were written before the move continue to compile.
func fallbackCandidates(results []media.SearchResult, mediaType media.MediaType) []media.SearchResult {
	return resolver.FallbackCandidates(results, mediaType)
}

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

// tryFallbackStream attempts to resolve a stream using the resilient Resolver,
// which races fallback providers and selects the first valid result.
func tryFallbackStream(primary provider.Provider, title string, mediaType media.MediaType, season, episode int) (*media.Stream, error) {
	r := resolver.New(fallbackProviders(primary), sharedHealth, debugf)
	req := resolver.Request{Title: title, MediaType: mediaType, Season: season, Episode: episode, Quality: cfgQuality()}
	stream, report, err := r.Resolve(context.Background(), req)
	if err != nil {
		debugf("resolve failed: %s", report.Summary())
		return nil, err
	}
	debugf("resolve ok via report: %s", report.Summary())
	return stream, nil
}

// makeStreamResolver builds a StreamResolver that tries the primary provider
// and all fallbacks to resolve a stream URL for downloads.
func makeStreamResolver(primary provider.Provider) dlmanager.StreamResolver {
	return func(title, mediaID, episodeID, mediaType string, season, episode int) (*dlmanager.StreamResult, error) {
		mt := media.Movie
		if mediaType == "tv" {
			mt = media.TV
		}

		// Use fallback providers to resolve a stream for downloads.
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
