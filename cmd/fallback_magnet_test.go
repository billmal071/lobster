package cmd

import (
	"errors"
	"lobster/internal/playlist"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/torrentstream"
)

// magnetProvider stands in for a torrent fallback (EZTV, Nyaa, YTS): it
// knows the show and answers Watch with a magnet.
type magnetProvider struct {
	stubStreamProvider
	results []media.SearchResult
}

func (p *magnetProvider) Search(string) ([]media.SearchResult, error) { return p.results, nil }

// torrentDouble is a magnetProvider the chain filter recognises as a torrent
// source, so a test can prove the download queue never races one.
type torrentDouble struct{ magnetProvider }

func (*torrentDouble) ServesMagnets() {}

// The resolver validates every candidate stream with an HTTP GET, and a
// magnet is not something an HTTP client can fetch, so every torrent
// fallback failed at stage "validate" and the chain never produced a
// magnet. tryFallbackStream runs the resolver exactly as production does
// (validation on), so this is the cmd-level guard for that path.
func TestTryFallbackStreamYieldsAMagnetWhenScrapersFail(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"
	scraper := &stubProvider{searchErr: errors.New("scraper down")}
	torrent := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: magnet}},
		results:            []media.SearchResult{{ID: "eztv/0903747", Title: "Show", Year: "2008", Type: media.TV}},
	}
	withRecordingFallbackChain(t, scraper, torrent)

	content := media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}
	stream, err := tryFallbackStream(scraper, content, 1, 2)
	if err != nil {
		var rf *resolveFailure
		if errors.As(err, &rf) && rf.report != nil {
			for _, a := range rf.report.Attempts {
				if a.Stage == "validate" {
					t.Fatalf("the magnet fallback was rejected at the validate stage: %v", a.Err)
				}
			}
		}
		t.Fatalf("tryFallbackStream: %v", err)
	}
	if stream == nil || !strings.HasPrefix(stream.URL, "magnet:") {
		t.Fatalf("stream = %+v, want the torrent fallback's magnet", stream)
	}
}

// The download queue cannot open a magnet (streamToResultChecked), so the
// torrent sources must not be in its race at all: a magnet winning fails the
// download while a scraper that could have served it is abandoned.
func TestWithoutMagnetSourcesDropsTorrentProviders(t *testing.T) {
	chain := []provider.Provider{provider.NewEZTV(), provider.NewVaPlayer(), provider.NewNyaa(), provider.NewYTS(), provider.NewFlixHQWS("flixhq.ws")}
	got := withoutMagnetSources(chain)
	if len(got) != 2 || !hasProvider[*provider.VaPlayer](got) || !hasProvider[*provider.FlixHQWS](got) {
		t.Fatalf("withoutMagnetSources = %v, want VaPlayer and FlixHQWS only", providerNames(got))
	}
}

// TestMakeStreamResolverSkipsTorrentSourcesForTheQueue checks that the queue resolver returns the downloadable scraper's stream and never asks the torrent source for one.
func TestMakeStreamResolverSkipsTorrentSourcesForTheQueue(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusPartialContent) }))
	t.Cleanup(cdn.Close)
	scraper := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}
	// A torrent source ahead of the scraper that answers instantly with a
	// magnet: left in the race it wins, and the queue rejects the result.
	torrent := &watchCountingTorrent{torrentDouble: &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}}}
	withRecordingFallbackChain(t, torrent, scraper)

	res, err := makeStreamResolver(&stubProvider{})(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2})
	if err != nil {
		t.Fatalf("makeStreamResolver: %v", err)
	}
	if res.URL != cdn.URL+"/ep.m3u8" || res.StreamType != "hls" {
		t.Fatalf("result = %+v, want the scraper's HLS stream", res)
	}
	if n := torrent.watched.Load(); n != 0 {
		t.Fatalf("the torrent source was asked for a stream %d times; the queue must leave it out of the race", n)
	}
}

// TestMakeStreamResolverDoesNotFallBackToTheTorrentTierWhenScrapersFail checks that the queue resolver fails without asking a torrent source for a stream when every scraper fails, instead of running the torrent tier.
func TestMakeStreamResolverDoesNotFallBackToTheTorrentTierWhenScrapersFail(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	torrent := &watchCountingTorrent{torrentDouble: &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}}}
	withRecordingFallbackChain(t, &stubProvider{searchErr: errors.New("scraper down")}, torrent)

	if res, err := makeStreamResolver(&stubProvider{})(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2}); err == nil {
		t.Fatalf("result = %+v, want a failure: the queue cannot download a magnet", res)
	}
	if n := torrent.watched.Load(); n != 0 {
		t.Fatalf("the torrent source was asked for a stream %d times after every scraper failed", n)
	}
}

// A batch download under `--base nyaa` or `--base eztv` must ask that
// provider first: the chain excludes its primary, so without this the one
// source the user named — and the one that listed the episodes — is skipped.
func TestResolveBatchStreamAsksTheSelectedTorrentSourceFirst(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "nyaa", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"
	primary := &stubStreamProvider{stream: &media.Stream{URL: magnet}}
	rec := withRecordingFallbackChain(t, &stubProvider{searchErr: errors.New("scraper down")})

	sel := media.SearchResult{ID: "nyaa/Show", Title: "Show", Type: media.TV}
	stream, err := resolveBatchStream(primary, sel, media.Episode{Number: 2, ID: "2"}, 1)
	if err != nil {
		t.Fatalf("resolveBatchStream: %v", err)
	}
	if stream.URL != magnet {
		t.Fatalf("stream = %+v, want the selected source's magnet", stream)
	}
	if len(rec.seen()) != 0 {
		t.Fatalf("fallback chain consulted %d times before the selected torrent source", len(rec.seen()))
	}

	// A streaming base keeps the chain-only behaviour.
	cfg.Base = "vaplayer"
	if _, err := resolveBatchStream(primary, sel, media.Episode{Number: 2, ID: "2"}, 1); err == nil {
		t.Fatal("a non-torrent primary was asked directly; want the chain's failure")
	}
}

// Under torrent_fallback the chain really does carry the TV torrent source,
// so the stub above stands in for a provider that is wired in.
func TestFallbackProvidersIncludesEZTVWhenOptedIn(t *testing.T) {
	prev := cfg
	cfg = &config.Config{TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })
	if !hasProvider[*provider.EZTV](fallbackProviders(nil)) {
		t.Fatalf("EZTV missing after opt-in: %v", providerNames(fallbackProviders(nil)))
	}
}

// TestOpenBatchStreamFallsBackWhenTorrentSetupFails checks that a batch download falls back to a scraper when the torrent server cannot start.
func TestOpenBatchStreamFallsBackWhenTorrentSetupFails(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "nyaa", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	prevSrv := newTorrentServer
	newTorrentServer = func(torrentstream.Options) (torrentServer, error) {
		return nil, errors.New("stubbed: no peers answered")
	}
	t.Cleanup(func() { newTorrentServer = prevSrv })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
	}))
	t.Cleanup(cdn.Close)
	scraper := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}},
		results:            []media.SearchResult{{ID: "tv/1", Title: "Show", Type: media.TV}},
	}
	withRecordingFallbackChain(t, scraper)

	primary := &stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}}
	sel := media.SearchResult{ID: "nyaa/Show", Title: "Show", Type: media.TV}
	stream, closeFn, err := openBatchStream(primary, sel, media.Episode{Number: 2, ID: "2"}, 1)
	if err != nil {
		t.Fatalf("openBatchStream: %v", err)
	}
	defer closeFn()
	if stream.URL != cdn.URL+"/ep.m3u8" {
		t.Fatalf("stream = %+v, want the fallback scraper's stream", stream)
	}
}

// TestTryFallbackStreamPrefersASlowScraperOverAFastMagnet checks that a slow scraper wins over an instant magnet, because the torrent tier runs only after every scraper fails.
func TestTryFallbackStreamPrefersASlowScraperOverAFastMagnet(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusPartialContent)
	}))
	t.Cleanup(cdn.Close)
	results := []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}}
	scraper := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}},
		results:            results,
	}
	torrent := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}},
		results:            results,
	}}
	withRecordingFallbackChain(t, torrent, scraper)

	content := media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}
	stream, err := tryFallbackStream(&stubProvider{}, content, 1, 2)
	if err != nil {
		t.Fatalf("tryFallbackStream: %v", err)
	}
	if stream.URL != cdn.URL+"/ep.m3u8" {
		t.Fatalf("stream = %q, want the scraper's stream: a torrent must only win when every scraper fails", stream.URL)
	}
}

// TestTryFallbackStreamFallsBackToMagnetWhenEveryScraperFails checks that the torrent tier supplies the magnet when every scraper fails.
func TestTryFallbackStreamFallsBackToMagnetWhenEveryScraperFails(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"
	scraper := &stubProvider{searchErr: errors.New("scraper down")}
	torrent := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: magnet}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}}
	withRecordingFallbackChain(t, torrent, scraper)

	content := media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}
	stream, err := tryFallbackStream(&stubProvider{}, content, 1, 2)
	if err != nil {
		t.Fatalf("tryFallbackStream: %v", err)
	}
	if stream.URL != magnet {
		t.Fatalf("stream = %q, want the magnet", stream.URL)
	}
}

// TestOnlyMagnetSourcesKeepsTorrentProviders checks that onlyMagnetSources keeps the torrent providers of a chain and drops the rest.
func TestOnlyMagnetSourcesKeepsTorrentProviders(t *testing.T) {
	chain := []provider.Provider{provider.NewEZTV(), provider.NewVaPlayer(), provider.NewNyaa(), provider.NewYTS()}
	got := onlyMagnetSources(chain)
	if len(got) != 3 || hasProvider[*provider.VaPlayer](got) {
		t.Fatalf("onlyMagnetSources = %v, want the three torrent providers", providerNames(got))
	}
}

// TestResolveStreamSkipsTheTorrentTierUnderJSON checks that under --json the torrent providers are never probed.
func TestResolveStreamSkipsTheTorrentTierUnderJSON(t *testing.T) {
	prev, prevJSON := cfg, flagJSON
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	flagJSON = true
	t.Cleanup(func() { cfg, flagJSON = prev, prevJSON })

	scraper := &stubProvider{searchErr: errors.New("scraper down")}
	torrent := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}}
	withRecordingFallbackChain(t, scraper, torrent)

	sess := playlist.New(&stubProvider{}, media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV},
		[]media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
	stream, _, err := resolveStream(sess, map[string]bool{})
	if err == nil {
		t.Fatalf("resolveStream = %+v, want a failure: --json cannot express a torrent, so the torrent tier is not tried", stream)
	}
	var rf *resolveFailure
	if !errors.As(err, &rf) || rf.report == nil {
		t.Fatalf("err = %v, want a *resolveFailure carrying the resolver's report", err)
	}
	for _, a := range rf.report.Attempts {
		if strings.Contains(a.Provider, "torrentDouble") || a.Stage == "resolve" {
			t.Fatalf("the torrent provider was probed under --json: %+v", a)
		}
	}
}

type recordingSP struct {
	magnetProvider
	searched  string
	episodeID string
}

func (p *recordingSP) Search(q string) ([]media.SearchResult, error) {
	p.searched = q
	return p.results, nil
}

func (p *recordingSP) Watch(_, episodeID, _, _ string) (*media.Stream, error) {
	p.episodeID = episodeID
	return p.stream, nil
}

func TestFallbackForANyaaSeasonResultAsksForThatSeasonByBaseTitle(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "nyaa", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusPartialContent) }))
	t.Cleanup(cdn.Close)
	rec := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}},
		results:            []media.SearchResult{{ID: "tv/77", Title: "One Punch Man", Year: "2015", Type: media.TV}},
	}}
	withRecordingFallbackChain(t, rec)

	content := media.SearchResult{ID: "nyaa/One%20Punch%20Man?s=3", Title: "One Punch Man Season 3", Year: "2015", Type: media.TV}
	if _, err := tryFallbackStreamFrom([]provider.Provider{rec}, content, 3, 4); err != nil {
		t.Fatalf("tryFallbackStreamFrom: %v", err)
	}
	if rec.searched != "One Punch Man" {
		t.Errorf("searched %q, want the base title", rec.searched)
	}
	if rec.episodeID != "77:3:4" {
		t.Errorf("episode ID = %q, want 77:3:4", rec.episodeID)
	}
}
