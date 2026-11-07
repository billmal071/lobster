package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
// Both StreamProviders (VaPlayer, TBCPL) and regular Providers (FlixHQWS) are
// included so the app tries every source before giving up. Consumet joins
// them only when api_url is configured.
//
// Retired sources (config.retiredBases) are not here: each one in the chain
// cost a probe timeout per resolve for an answer that could only be a failure.
func fallbackProviders(primary provider.Provider) []provider.Provider {
	var fallbacks []provider.Provider

	if _, ok := primary.(*provider.VaPlayer); !ok {
		fallbacks = append(fallbacks, provider.NewVaPlayer())
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

	// EZTV before the remaining scrapers for a series (complete encodes, not a
	// player's HLS), gated like YTS below: its magnet puts the user in a swarm.
	if _, ok := primary.(*provider.EZTV); !ok {
		if cfg != nil && cfg.TorrentFallback {
			fallbacks = append(fallbacks, provider.NewEZTV())
		}
	}

	if _, ok := primary.(*provider.FlixHQWS); !ok {
		fallbacks = append(fallbacks, provider.NewFlixHQWS("flixhq.ws"))
	}

	// Last so movie/TV scrapers keep priority; this catches anime the others
	// lack. AnimeOnsen is the only anime source left standing.
	if _, ok := primary.(*provider.AnimeOnsen); !ok {
		fallbacks = append(fallbacks, provider.NewAnimeOnsen())
	}

	// Nyaa after AnimeOnsen: anime torrents for what it lacks, gated the same way.
	if _, ok := primary.(*provider.Nyaa); !ok {
		if cfg != nil && cfg.TorrentFallback {
			fallbacks = append(fallbacks, provider.NewNyaa())
		}
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
	// `base = "vaplayer"` — or any explicit non-torrent base, or an api_url,
	// which overrides base entirely — with torrent_fallback off never
	// reaches a magnet. An explicit yts, nyaa or eztv base still does: the
	// user named the torrent source outright.
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
	return tryFallbackStreamFrom(agentFallbackProviders(primary), content, season, episode)
}

// servesMagnets reports whether p's Watch answers with a magnet URI, which
// only the local torrent server can open.
func servesMagnets(p provider.Provider) bool {
	switch p.(type) {
	case *provider.YTS, *provider.EZTV, *provider.Nyaa:
		return true
	}
	_, ok := p.(magnetSource)
	return ok
}

// magnetSource marks a provider whose streams are magnets (test doubles).
type magnetSource interface{ ServesMagnets() }

// withoutMagnetSources drops the torrent providers from a chain, for a
// consumer that cannot open a magnet (the download queue).
func withoutMagnetSources(ps []provider.Provider) []provider.Provider {
	out := make([]provider.Provider, 0, len(ps))
	for _, p := range ps {
		if !servesMagnets(p) {
			out = append(out, p)
		}
	}
	return out
}

// onlyMagnetSources keeps just the torrent providers of a chain.
func onlyMagnetSources(ps []provider.Provider) []provider.Provider {
	out := make([]provider.Provider, 0, len(ps))
	for _, p := range ps {
		if servesMagnets(p) {
			out = append(out, p)
		}
	}
	return out
}

var nyaaSeasonSuffix = regexp.MustCompile(`(?i)\s+Season\s+\d{1,2}$`)

// fallbackTitle is the title other providers are searched for. Nyaa titles a
// sequel "Show Season N", which no TMDB-backed catalogue lists; the season
// travels separately.
func fallbackTitle(c media.SearchResult) string {
	if strings.HasPrefix(c.ID, "nyaa/") {
		return nyaaSeasonSuffix.ReplaceAllString(c.Title, "")
	}
	return c.Title
}

// fallbackBudget bounds both tiers of tryFallbackStreamFrom together.
const fallbackBudget = 90 * time.Second

// tryFallbackStreamFrom is tryFallbackStream over an explicit chain. The
// streaming providers are raced first and the torrent providers only when
// every one of them fails: a magnet validates instantly, so racing them
// together lets a torrent beat a working scraper that is still being checked.
func tryFallbackStreamFrom(providers []provider.Provider, content media.SearchResult, season, episode int) (*media.Stream, error) {
	req := resolver.Request{
		ID:        content.ID,
		Title:     fallbackTitle(content),
		Year:      content.Year,
		MediaType: content.Type,
		Season:    season,
		Episode:   episode,
		Quality:   cfgQuality(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), fallbackBudget)
	defer cancel()

	plain, magnets := withoutMagnetSources(providers), onlyMagnetSources(providers)
	if len(plain) == 0 || len(magnets) == 0 {
		return resolveTier(ctx, providers, req, nil)
	}
	stream, err := resolveTier(ctx, plain, req, nil)
	if err == nil || ctx.Err() != nil {
		return stream, err
	}
	var first *resolveFailure
	errors.As(err, &first)
	return resolveTier(ctx, magnets, req, first)
}

func resolveTier(ctx context.Context, providers []provider.Provider, req resolver.Request, earlier *resolveFailure) (*media.Stream, error) {
	r := resolver.New(providers, sharedHealth(), debugf)
	stream, report, err := r.Resolve(ctx, req)
	if err != nil {
		if earlier != nil && earlier.report != nil {
			report.Attempts = append(earlier.report.Attempts, report.Attempts...)
			err = fmt.Errorf("all providers failed: %s", report.Summary())
		}
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
// provider's own search call failed (resolveWithProvider, internal/resolver/probe.go),
// "match" means it searched fine and has nothing under that title, "resolve"
// means it has the title and could not serve it, "validate" means it served a
// URL that did not answer.
//
// "search" and "match" are deliberately not merged: a failed search is the
// provider being broken or blocked and says nothing about the catalogue, while
// "match" is the one stage that really does mean "not here". Reading them as
// one is how a provider outage gets diagnosed as a missing title.
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
//
// Attempt.IsProviderProbe is what excludes the resolver's note about its own
// deadline, which carries resolver.SyntheticProvider as its name and would
// otherwise be counted as a tenth provider in a nine-provider run. The
// predicate is asked rather than the stage string compared: the stage is
// resolver's to rename, and a literal here would have gone on compiling while
// quietly adding one to a number a user reads.
func (e *resolveFailure) providersTried() int {
	if e.report == nil {
		return 0
	}
	seen := make(map[string]bool, len(e.report.Attempts))
	for _, a := range e.report.Attempts {
		if !a.IsProviderProbe() {
			continue
		}
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

		// Use fallback providers to resolve a stream for downloads. The
		// torrent sources are left out of the race: the queue cannot open a
		// magnet (streamToResultChecked), so one winning would fail the
		// download while a scraper that could have served it was abandoned.
		content := media.SearchResult{ID: req.MediaID, Title: req.Title, Year: req.Year, Type: mt}
		fbStream, err := tryFallbackStreamFrom(withoutMagnetSources(agentFallbackProviders(primary)), content, req.Season, req.Episode)
		if err != nil {
			return nil, fmt.Errorf("all providers failed: %w", err)
		}
		return streamToResultChecked(fbStream)
	}
}

// isDASHManifest reports whether a stream URL is an MPEG-DASH manifest.
//
// The test is on the path, not on a substring of the whole URL: a query string
// can legitimately carry ".mpd" (a referrer or a filename parameter) without
// the resource being a manifest, and classifying such a URL as DASH would
// refuse a download that would have worked.
func isDASHManifest(rawURL string) bool {
	path := rawURL
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return strings.HasSuffix(strings.ToLower(path), ".mpd")
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
// MPEG-DASH is refused for the same reason and by the same reasoning, one step
// further on — and it is the worse case of the two. dlmanager's engine has
// exactly two arms, "hls" and "http" (internal/dlmanager/manager.go), and the
// classification above is a substring test that finds neither ".m3u8" nor
// "hls" in a .mpd URL. So a DASH manifest is labelled "http", handed to the
// plain-file engine, and saved verbatim: an 8 kB XML document under a video
// filename, and a download the manager reports as complete. That is strictly
// worse than a failure, because nothing tells the user until they try to play
// it.
//
// This is not a claim that DASH cannot be downloaded. `lobster -d` goes
// through internal/download, which is ffmpeg, and ffmpeg demuxes DASH
// natively. It is this queue that cannot, and the message says which.
func streamToResultChecked(s *media.Stream) (*dlmanager.StreamResult, error) {
	if torrentstream.IsMagnet(s.URL) {
		return nil, fmt.Errorf("this source is a torrent, which cannot be downloaded this way: play it instead, or pick another source")
	}
	if isDASHManifest(s.URL) {
		return nil, fmt.Errorf("this source is an MPEG-DASH stream, which the download queue cannot assemble: play it instead, or download it with -d, which goes through ffmpeg")
	}
	return streamToResult(s), nil
}
