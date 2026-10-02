package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
	"lobster/internal/tbcpl"
	"lobster/internal/torrentstream"
)

var (
	sharedHealthOnce  sync.Once
	sharedHealthStore *resolver.HealthStore
)

var (
	tbcplCatOnce sync.Once
	tbcplCatVal  *tbcpl.Catalog
)

// tbcplCatalog loads the TBCPL catalog once per process, or nil if disabled,
// under no deadline of its own. The interactive paths (newProvider,
// fallbackProviders) have no budget to honour and keep using it.
func tbcplCatalog() *tbcpl.Catalog { return tbcplCatalogContext(context.Background()) }

// tbcplCatalogContext is tbcplCatalog bounded by ctx.
//
// The catalog load can reach the network (Client.LoadMerged fetches when the
// cache is cold or a region is configured), so on an agent-facing path it has
// to sit inside the same budget as the playlist load it feeds. Otherwise
// "channels" can block for the catalog fetch's own timeout and then a further
// LiveLoadBudget, overshooting the deadline the command advertises — with no
// output and no error to explain the wait.
//
// The sync.Once is shared with tbcplCatalog: whichever call arrives first
// decides the deadline for the process, and a cancelled load caches nil (a
// catalog that could not be fetched), which is the same outcome as the feed
// being disabled. That is the intended behaviour rather than a compromise:
// re-fetching per call would put an unbounded network fetch behind every
// command that consults the catalog.
func tbcplCatalogContext(ctx context.Context) *tbcpl.Catalog {
	tbcplCatOnce.Do(func() {
		if cfg == nil || !cfg.TBCPLFeed {
			return
		}
		cache, err := config.TBCPLCachePath()
		if err != nil {
			return
		}
		cl := tbcpl.NewClient(cache, 12*time.Hour, debugf)
		region := ""
		if cfg != nil {
			region = cfg.TBCPLRegion
		}
		tbcplCatVal = cl.LoadMerged(ctx, region)
	})
	return tbcplCatVal
}

// liveTVSources returns configured IPTV sources plus TBCPL live-tv playlists,
// with the catalog fetch unbounded. The TUI is its only caller.
func liveTVSources() []string { return liveTVSourcesContext(context.Background()) }

// liveTVSourcesContext is liveTVSources with the catalog fetch bounded by
// ctx. The agent-facing commands call this one, with the same context that
// bounds the playlist load, so the deadline they advertise covers discovering
// the sources as well as loading them.
func liveTVSourcesContext(ctx context.Context) []string {
	var sources []string
	if cfg != nil {
		sources = cfg.LiveTV.Sources()
	}
	cat := tbcplCatalogContext(ctx)
	if cat == nil {
		return sources
	}
	include := cfg != nil && cfg.TBCPLIncludeUntrusted
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s] = true
	}
	for _, pl := range cat.LivePlaylists(include) {
		if !seen[pl] {
			sources = append(sources, pl)
			seen[pl] = true
		}
	}
	return sources
}

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

	if cat := tbcplCatalog(); cat != nil {
		sites := cat.EligibleSites(cfg != nil && cfg.TBCPLIncludeUntrusted)
		if _, ok := primary.(*provider.TBCPLEmbed); !ok && len(sites) > 0 {
			embed := provider.NewTBCPLEmbed(sites)
			embed.SetLogger(debugf)
			fallbacks = append(fallbacks, embed)
		}
	}

	// YTS last, and only when torrent_fallback is set.
	//
	// Be precise about what that setting does and does not decide. It is NOT
	// what makes lobster join a swarm: under the default base = "auto" every
	// movie is routed to YTS at selection time (routeByType, cmd/typeroute.go),
	// so a default install already streams films over BitTorrent with the
	// user's IP visible to its peers. What this setting controls is the other
	// direction — letting a *failed* stream resolution end on a torrent: for a
	// series, for a film YTS has no match for, and under an explicit base the
	// user chose precisely to say which source to use. A swarm reached by
	// choosing "auto" is a consequence of the documented default; one reached
	// because a scraper broke is not, which is why this stays off until asked.
	//
	// Opting out of swarming altogether is a source, not this flag:
	// `base = "soap2day"` — or any explicit non-YTS base, or an api_url,
	// which overrides base entirely — with torrent_fallback off never
	// reaches a magnet.
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
	r := resolver.New(agentFallbackProviders(primary), sharedHealth(), debugf)
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
		return nil, &resolveFailure{report: report, err: err}
	}
	debugf("resolve ok via report: %s", report.Summary())
	return stream, nil
}

// resolveFailure is "the resolver ran and no provider produced a stream",
// carrying the per-provider report alongside the error.
//
// The report was already assembled on every Resolve call and then thrown away
// here: `debugf("resolve failed: %s", report.Summary())` was its only
// consumer, so the reasons eleven providers gave existed solely behind -x. The
// returned error has the same digest flattened into one sentence by
// resolver.Resolve, which is unparseable and, at nine providers, too long to
// read as a message.
//
// Error() deliberately returns the wrapped error verbatim and Unwrap exposes
// it, so the callers that only render text — cmd/batch.go's per-episode
// download loop, makeStreamResolver — read exactly as they did before this
// type existed. Only a caller that asks for the structure gets it.
type resolveFailure struct {
	report *resolver.Report
	err    error
}

func (e *resolveFailure) Error() string { return e.err.Error() }
func (e *resolveFailure) Unwrap() error { return e.err }

// providerRows renders the report as JSON-ready rows, one per probe attempt, in
// the order the resolver recorded them.
//
// One row per *attempt*, not per provider: the resolver probes in
// health-ordered batches and records a batch-timeout row for any provider it
// abandons (Resolver.recordPending), so a provider can legitimately appear once
// as a timeout and never again. Collapsing by name would hide which stage each
// one reached, and the stage is the actionable half — "search" means the
// provider does not have the title, "resolve" means it has it and could not
// serve it, "validate" means it served a URL that did not answer.
func (e *resolveFailure) providerRows() []map[string]any {
	if e.report == nil {
		return nil
	}
	rows := make([]map[string]any, 0, len(e.report.Attempts))
	for _, a := range e.report.Attempts {
		row := map[string]any{
			"provider":    a.Provider,
			"stage":       a.Stage,
			"duration_ms": a.DurationMs,
		}
		if a.Err != nil {
			row["error"] = a.Err.Error()
		}
		rows = append(rows, row)
	}
	return rows
}

// providersTried counts the distinct providers the report mentions, for the
// one-line message. Distinct, because providerRows is per attempt.
func (e *resolveFailure) providersTried() int {
	if e.report == nil {
		return 0
	}
	seen := make(map[string]bool, len(e.report.Attempts))
	for _, a := range e.report.Attempts {
		seen[a.Provider] = true
	}
	return len(seen)
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
