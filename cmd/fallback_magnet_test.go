package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// magnetProvider stands in for a torrent fallback (EZTV, Nyaa, YTS): it
// knows the show and answers Watch with a magnet.
type magnetProvider struct {
	stubStreamProvider
	results []media.SearchResult
}

func (p *magnetProvider) Search(string) ([]media.SearchResult, error) { return p.results, nil }

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
