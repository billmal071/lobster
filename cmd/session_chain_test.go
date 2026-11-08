package cmd

import (
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/playlist"
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
