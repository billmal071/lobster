package cmd

import (
	"errors"
	"testing"
	"time"

	"lobster/internal/config"
	"lobster/internal/media"
)

type lateFailingMagnet struct {
	magnetProvider
	release chan struct{}
}

func (p *lateFailingMagnet) Search(string) ([]media.SearchResult, error) {
	<-p.release
	return nil, errors.New("search down")
}

func TestAbandonedProbeNeverReadsTheGlobalConfig(t *testing.T) {
	prev := cfg
	cfg = &config.Config{Base: "flixhq.ws", Quality: "1080", TorrentFallback: true}
	t.Cleanup(func() { cfg = prev })

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Show"
	winner := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: magnet}},
		results:            []media.SearchResult{{ID: "eztv/1", Title: "Show", Year: "2008", Type: media.TV}},
	}
	loser := &lateFailingMagnet{release: make(chan struct{})}
	withRecordingFallbackChain(t, loser, winner)

	content := media.SearchResult{ID: "tv/1396", Title: "Show", Year: "2008", Type: media.TV}
	stream, err := tryFallbackStream(winner, content, 1, 2)
	if err != nil || stream == nil {
		t.Fatalf("got %v / %v, want the winner's magnet", stream, err)
	}

	close(loser.release)
	cfg = &config.Config{Base: "other"}
	time.Sleep(200 * time.Millisecond)
}
