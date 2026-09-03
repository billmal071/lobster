package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
	"lobster/internal/torrentstream"
)

var (
	sharedHealthOnce  sync.Once
	sharedHealthStore *resolver.HealthStore
)

func sharedHealth() *resolver.HealthStore {
	sharedHealthOnce.Do(func() {
		p, err := config.HealthPath()
		if err != nil {
			sharedHealthStore = resolver.NewHealthStore()
			return
		}
		sharedHealthStore = resolver.LoadHealth(p)
	})
	return sharedHealthStore
}

// flixhqDomain resolves a healthy FlixHQ mirror, memoized per session.
// Package var so tests can stub the probe.
var flixhqDomain = provider.FirstHealthyDomainCached

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
// Both StreamProviders (Soap2Day, MovieBox, TBCPL) and regular Providers
// (FlixHQWS, KimCartoon) are included so the app tries every source before
// giving up. Consumet joins them only when api_url is configured.
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
		tb := provider.NewTBCPL("tbcpl")
		if cfg != nil {
			tb.SetAudioLanguage(cfg.AudioLanguage)
		}
		fallbacks = append(fallbacks, tb)
	}

	// Consumet is an aggregator, so it is worth more than any single scraper —
	// but it has no public instance, only whatever the user self-hosts. Without
	// api_url there is nothing to talk to, so it joins the chain only when one
	// is configured rather than failing every request.
	if _, ok := primary.(*provider.Consumet); !ok {
		if cfg != nil && cfg.APIURL != "" {
			fallbacks = append(fallbacks, provider.NewConsumet(cfg.APIURL))
		}
	}

	if _, ok := primary.(*provider.FlixHQWS); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQWS("flixhq.ws"))
	}

	// The flixhq.to engine family (flixhq.to, sflix.to, myflixerz.to, ...) has
	// been origin-down since ~Aug 2026, so the scraper joins the chain only when
	// a health probe finds a live mirror. The probe runs in parallel across all
	// candidates and is cached for the session, so while everything is dead this
	// costs one probe timeout per run — and the provider revives automatically
	// the moment any mirror answers again.
	if _, ok := primary.(*provider.FlixHQ); !ok {
		var overrides map[string][]string
		if cfg != nil {
			overrides = cfg.DomainOverrides
		}
		if d := flixhqDomain("flixhq", overrides); d != "" {
			fallbacks = append(fallbacks, provider.NewFlixHQ(d))
		}
	}

	if _, ok := primary.(*provider.KimCartoon); !ok {
		fallbacks = append(fallbacks, provider.NewKimCartoon("kimcartoon.com.co"))
	}

	// Last so movie/TV scrapers keep priority; these catch anime the others
	// lack. AniPub is the anime path. AllAnime is retired: its API now sits behind a
	// Cloudflare bot challenge on top of the crypto-gated sources endpoint
	// (AA_CRYPTO_MISSING, mid-2026), so it can neither search nor stream. The
	// provider code stays for the day either gate lifts.

	if _, ok := primary.(*provider.AniPub); !ok {
		fallbacks = append(fallbacks, provider.NewAniPub())
	}

	// YTS last, and only on request. It resolves to a magnet rather than an HTTP
	// stream, so falling back to it joins a BitTorrent swarm and makes the user's
	// IP visible to its peers. Reaching it via `--base yts` is a deliberate act;
	// reaching it because a scraper broke is not, so it stays opt-in.
	if _, ok := primary.(*provider.YTS); !ok {
		if cfg != nil && cfg.TorrentFallback {
			fallbacks = append(fallbacks, provider.NewYTS())
		}
	}

	return fallbacks
}

// tryFallbackStream attempts to resolve a stream using the resilient Resolver,
// which races fallback providers and selects the first valid result.
// content carries the ID and year of the work the user actually selected so the
// resolver can tell a franchise entry apart from its sequels.
func tryFallbackStream(primary provider.Provider, content media.SearchResult, season, episode int) (*media.Stream, error) {
	r := resolver.New(fallbackProviders(primary), sharedHealth(), debugf)
	req := resolver.Request{
		ID:        content.ID,
		Title:     content.Title,
		Year:      content.Year,
		MediaType: content.Type,
		Season:    season,
		Episode:   episode,
		Quality:   cfgQuality(),
	}
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
	return func(req dlmanager.ResolveRequest) (*dlmanager.StreamResult, error) {
		mt := media.Movie
		if req.MediaType == "tv" {
			mt = media.TV
		}

		// Use fallback providers to resolve a stream for downloads.
		content := media.SearchResult{ID: req.MediaID, Title: req.Title, Year: req.Year, Type: mt}
		fbStream, err := tryFallbackStream(primary, content, req.Season, req.Episode)
		if err != nil {
			return nil, fmt.Errorf("all providers failed: %w", err)
		}
		return streamToResultChecked(fbStream)
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

// streamToResultChecked is streamToResult for the download path, which cannot
// open a magnet. The classification above is substring-based, so a magnet would
// otherwise be labelled "http" and handed to an engine that fails on it long
// after the user stopped watching. Refuse it up front and say why.
func streamToResultChecked(s *media.Stream) (*dlmanager.StreamResult, error) {
	if torrentstream.IsMagnet(s.URL) {
		return nil, fmt.Errorf("this source is a torrent, which cannot be downloaded this way: play it instead, or pick another source")
	}
	return streamToResult(s), nil
}
