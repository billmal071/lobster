package cmd

import (
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/playlist"
	"lobster/internal/provider"
)

const chainTestMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"

func chainTestContent() media.SearchResult {
	return media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}
}

func chainTestConfig(t *testing.T, json bool) {
	t.Helper()
	prev, prevJSON := cfg, flagJSON
	cfg = &config.Config{Base: "vaplayer", Quality: "1080"}
	flagJSON = json
	t.Cleanup(func() { cfg, flagJSON = prev, prevJSON })
}

func TestResolveStreamUnderJSONTriesHTTPFallbacksAfterAPrimaryMagnet(t *testing.T) {
	chainTestConfig(t, true)
	httpURL := stubStreamServer(t)
	fb := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: httpURL}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, fb)

	prov := &stubStreamProvider{stream: &media.Stream{URL: chainTestMagnet}}
	sess := playlist.New(prov, chainTestContent(), []media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
	stream, _, err := resolveStream(sess, map[string]bool{})
	if err != nil {
		t.Fatalf("resolveStream: %v; an HTTP fallback is available", err)
	}
	if stream.URL != httpURL {
		t.Fatalf("stream = %q, want the HTTP fallback %q", stream.URL, httpURL)
	}
}

type otherRecordingSP struct{ recordingSP }

func TestRetryChainExcludesTheRecoveredProviderOnceItsStreamFailed(t *testing.T) {
	chainTestConfig(t, false)
	url := stubStreamServer(t)
	newSP := func() *recordingSP {
		return &recordingSP{magnetProvider: magnetProvider{
			stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: url}},
			results:            []media.SearchResult{chainTestContent()},
		}}
	}
	recovered := newSP()
	other := &otherRecordingSP{recordingSP: *newSP()}
	freshRecovered := newSP()
	withRecordingFallbackChain(t, freshRecovered, other)

	configured := &stubProvider{}
	sess := playlist.NewWithProviderID(recovered, chainTestContent(), "x/1", configured,
		[]media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
	if _, _, err := resolveStream(sess, map[string]bool{"Primary": true}); err != nil {
		t.Fatalf("resolveStream: %v", err)
	}
	if freshRecovered.searched != "" {
		t.Fatalf("the recovered provider was searched again after its stream failed playback")
	}
	if other.searched == "" {
		t.Fatalf("the other chain member was never asked")
	}
}

func TestResolveStreamUnderJSONKeepsFallbackFailureWhenPrimaryIsAMagnet(t *testing.T) {
	chainTestConfig(t, true)
	empty := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/x.m3u8"}},
	}}
	withRecordingFallbackChain(t, empty)

	prov := &stubStreamProvider{stream: &media.Stream{URL: chainTestMagnet}}
	sess := playlist.New(prov, chainTestContent(), []media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
	err := playCurrentEpisode(sess)
	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "--json cannot express") {
		t.Fatalf("err = %v, want the torrent refusal kept", err)
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Fatalf("err = %v, want the fallback failure reported too", err)
	}
}

func recoveredTorrentSession(recovered provider.Provider, chainPrimary provider.Provider) *playlist.Session {
	return playlist.NewWithProviderID(recovered, chainTestContent(), "x/1", chainPrimary,
		[]media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
}

func newTorrentSource() *torrentDouble {
	return &torrentDouble{magnetProvider{stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: chainTestMagnet}}}}
}

func TestResolveStreamTriesStreamingBeforeARecoveredTorrentProvider(t *testing.T) {
	chainTestConfig(t, false)
	httpURL := stubStreamServer(t)
	fb := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: httpURL}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, fb)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	stream, _, err := resolveStream(sess, map[string]bool{})
	if err != nil {
		t.Fatalf("resolveStream: %v", err)
	}
	if stream.URL != httpURL {
		t.Fatalf("stream = %q, want the streaming fallback %q before the recovered torrent", stream.URL, httpURL)
	}
}

func TestResolveStreamFallsBackToARecoveredTorrentWhenStreamingFails(t *testing.T) {
	chainTestConfig(t, false)
	withRecordingFallbackChain(t)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	stream, name, err := resolveStream(sess, map[string]bool{})
	if err != nil {
		t.Fatalf("resolveStream: %v", err)
	}
	if stream.URL != chainTestMagnet || name != "Primary" {
		t.Fatalf("stream = %q (%s), want the recovered torrent once streaming is exhausted", stream.URL, name)
	}
}

func TestResolveStreamPlaysAnExplicitTorrentBaseDirectly(t *testing.T) {
	chainTestConfig(t, false)
	chain := withRecordingFallbackChain(t)

	sess := recoveredTorrentSession(newTorrentSource(), nil)
	stream, _, err := resolveStream(sess, map[string]bool{})
	if err != nil {
		t.Fatalf("resolveStream: %v", err)
	}
	if stream.URL != chainTestMagnet {
		t.Fatalf("stream = %q, want the explicit torrent base's magnet", stream.URL)
	}
	if len(chain.seen()) != 0 {
		t.Fatalf("fallback chain consulted %d times for an explicit torrent base", len(chain.seen()))
	}
}
