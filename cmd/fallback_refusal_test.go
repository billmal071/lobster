package cmd

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"lobster/internal/config"
	"lobster/internal/dlmanager"
	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

// countingTorrent is a torrent-source double that counts how often it is searched.
type countingTorrent struct {
	torrentDouble
	searches atomic.Int32
}

func (c *countingTorrent) Search(q string) ([]media.SearchResult, error) {
	c.searches.Add(1)
	return c.torrentDouble.Search(q)
}

// TestTwoTierChainKeepsTheTVRefusal checks that when the streaming tier refuses a TV 0/0 request, the torrent tier is not run and the typed, readable refusal comes back instead of "all providers failed" with an empty summary.
func TestTwoTierChainKeepsTheTVRefusal(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "vaplayer", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	web := &recordingStreamProvider{url: "https://cdn/x.m3u8"}
	torrent := &countingTorrent{}
	torrent.results = []media.SearchResult{{ID: "eztv/1", Title: "Naruto", Type: media.TV}}
	torrent.stream = &media.Stream{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}

	content := media.SearchResult{ID: "tv/31910", Title: "Naruto", Type: media.TV}
	_, err := tryFallbackStreamFrom([]provider.Provider{web, torrent}, content, 0, 0)
	if !errors.Is(err, resolver.ErrSeasonEpisodeRequired) {
		t.Fatalf("err = %v, want ErrSeasonEpisodeRequired", err)
	}
	if strings.Contains(err.Error(), "all providers failed") {
		t.Errorf("err %q is dressed as a provider failure", err)
	}
	if torrent.searches.Load() != 0 {
		t.Errorf("torrent tier was searched %d times after the refusal", torrent.searches.Load())
	}
}

// TestTVRefusalIsSaidOnce checks the refusal text does not repeat itself, from the resolver and from the prompt gate.
func TestTVRefusalIsSaidOnce(t *testing.T) {
	_, _, err := resolver.New(nil, resolver.NewHealthStore(), nil).Resolve(t.Context(), resolver.Request{Title: "Naruto", MediaType: media.TV})
	if n := strings.Count(err.Error(), "TV title"); n != 1 {
		t.Errorf("resolver refusal %q repeats itself (%d x TV title)", err, n)
	}

	withTTY(t, false, false)
	_, _, err = askSeasonEpisode(&stubProvider{}, media.SearchResult{ID: "tv/1", Title: "Naruto", Type: media.TV}, 0, 0)
	if !errors.Is(err, resolver.ErrSeasonEpisodeRequired) {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(err.Error(), "TV title"); n != 1 {
		t.Errorf("prompt refusal %q says TV title %d times", err, n)
	}
}

// TestQueueResolverDoesNotDressTheTVRefusalAsAProviderFailure checks the download queue passes the typed refusal through unprefixed.
func TestQueueResolverDoesNotDressTheTVRefusalAsAProviderFailure(t *testing.T) {
	withFallbackChain(t, &recordingStreamProvider{url: "https://cdn/x.m3u8"})
	_, err := makeStreamResolver(&stubProvider{})(dlmanager.ResolveRequest{MediaID: "tv/31910", Title: "Naruto", MediaType: "tv"})
	if !errors.Is(err, resolver.ErrSeasonEpisodeRequired) {
		t.Fatalf("err = %v, want ErrSeasonEpisodeRequired", err)
	}
	if strings.HasPrefix(err.Error(), "all providers failed") {
		t.Errorf("err %q is prefixed as a provider failure", err)
	}
}
