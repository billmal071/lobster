package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	torrent := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"}},
		results:            []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}},
	}}
	withRecordingFallbackChain(t, torrent, scraper)

	res, err := makeStreamResolver(&stubProvider{})(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2})
	if err != nil {
		t.Fatalf("makeStreamResolver: %v", err)
	}
	if res.URL != cdn.URL+"/ep.m3u8" || res.StreamType != "hls" {
		t.Fatalf("result = %+v, want the scraper's HLS stream", res)
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
