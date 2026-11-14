package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
)

// TestStreamToResultCarriesUserAgent checks that a stream's Referer and User-Agent reach the queue's stream result.
func TestStreamToResultCarriesUserAgent(t *testing.T) {
	res, err := streamToResultChecked(&media.Stream{URL: "https://cdn.example.com/a.m3u8", Referer: "https://ref/", UserAgent: "CustomUA/1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UserAgent != "CustomUA/1" || res.Referer != "https://ref/" {
		t.Fatalf("result = %+v, want the stream's Referer and UserAgent", res)
	}
}

// TestStreamToResultCheckedRefusesObfuscatedHLS checks that the queue refuses a stream whose segments need the de-obfuscating proxy.
func TestStreamToResultCheckedRefusesObfuscatedHLS(t *testing.T) {
	_, err := streamToResultChecked(&media.Stream{URL: "https://cdn.example.com/a.m3u8", Deobfuscate: true})
	if err == nil {
		t.Fatal("the queue's engines cannot de-obfuscate; the download would save PNG-wrapped segments")
	}
}

type slowWatch struct{ magnetProvider }

func (p *slowWatch) Watch(a, b, c, d string) (*media.Stream, error) {
	time.Sleep(150 * time.Millisecond)
	return p.magnetProvider.Watch(a, b, c, d)
}

// TestMakeStreamResolverTriesNextProviderWhenWinnerIsUnqueueable checks that the queue resolver moves on to a downloadable HLS source when the first provider answers with a DASH manifest.
func TestMakeStreamResolverTriesNextProviderWhenWinnerIsUnqueueable(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "flixhq.ws", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusPartialContent) }))
	t.Cleanup(cdn.Close)
	results := []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}}
	dash := &magnetProvider{stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.mpd"}}, results: results}
	hls := &slowWatch{magnetProvider{stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}}, results: results}}
	withRecordingFallbackChain(t, dash, hls)

	res, err := makeStreamResolver(&stubProvider{})(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2})
	if err != nil {
		t.Fatalf("makeStreamResolver: %v (the HLS source was never tried)", err)
	}
	if res.URL != cdn.URL+"/ep.m3u8" {
		t.Fatalf("result = %+v, want the HLS source", res)
	}
}

// TestMakeStreamResolverTriesThePrimaryProvider checks that the queue resolver tries the primary provider, so a download whose only source is the primary still resolves.
func TestMakeStreamResolverTriesThePrimaryProvider(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "flixhq.ws", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusPartialContent) }))
	t.Cleanup(cdn.Close)
	results := []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}}
	primary := &magnetProvider{stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: cdn.URL + "/ep.m3u8"}}, results: results}
	withRecordingFallbackChain(t, &stubProvider{searchErr: errors.New("scraper down")})

	res, err := makeStreamResolver(primary)(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2})
	if err != nil {
		t.Fatalf("makeStreamResolver: %v (the primary provider was never tried)", err)
	}
	if res.URL != cdn.URL+"/ep.m3u8" {
		t.Fatalf("result = %+v, want the primary's HLS stream", res)
	}
}

// TestMakeStreamResolverStillRefusesAPrimaryMagnet checks that the queue resolver does not hand a primary torrent source's magnet to the download queue.
type watchCountingTorrent struct {
	*torrentDouble
	watched atomic.Int32
}

// Watch counts the call and answers like the wrapped torrent source.
func (w *watchCountingTorrent) Watch(a, b, c, d string) (*media.Stream, error) {
	w.watched.Add(1)
	return w.torrentDouble.Watch(a, b, c, d)
}

func TestMakeStreamResolverStillRefusesAPrimaryMagnet(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "nyaa", Quality: "1080"}
	t.Cleanup(func() { cfg = prev })

	results := []media.SearchResult{{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}}
	primary := &watchCountingTorrent{torrentDouble: &torrentDouble{magnetProvider{stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}}, results: results}}}
	withRecordingFallbackChain(t, &stubProvider{searchErr: errors.New("scraper down")})

	if res, err := makeStreamResolver(primary)(dlmanager.ResolveRequest{MediaID: "tv/1396", Title: "Show", Year: "2008", MediaType: "tv", Season: 1, Episode: 2}); err == nil {
		t.Fatalf("result = %+v, want a failure: the queue cannot open a magnet", res)
	}
	if n := primary.watched.Load(); n != 0 {
		t.Fatalf("the primary torrent source was asked for a stream %d times; the queue's chain must not contain it", n)
	}
}
