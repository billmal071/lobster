package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
)

func TestStreamToResultCarriesUserAgent(t *testing.T) {
	res, err := streamToResultChecked(&media.Stream{URL: "https://cdn.example.com/a.m3u8", Referer: "https://ref/", UserAgent: "CustomUA/1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UserAgent != "CustomUA/1" || res.Referer != "https://ref/" {
		t.Fatalf("result = %+v, want the stream's Referer and UserAgent", res)
	}
}

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

func TestMakeStreamResolverTriesNextProviderWhenWinnerIsUnqueueable(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080"}
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
