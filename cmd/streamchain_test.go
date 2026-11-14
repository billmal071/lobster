package cmd

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/playlist"
	"lobster/internal/provider"
	"lobster/internal/torrentstream"
)

func TestResolveStreamAsksAFlixHQWSPrimaryForAnEpisode(t *testing.T) {
	chainTestConfig(t, false)
	withRecordingFallbackChain(t, &stubProvider{searchErr: errors.New("scraper down")})

	sess := playlist.New(provider.NewFlixHQWS("http://127.0.0.1:1"), chainTestContent(),
		[]media.Season{{Number: 1, ID: "s1"}}, []media.Episode{{Number: 2, ID: "e2"}}, 0, 0)
	_, _, err := resolveStream(sess, map[string]bool{})
	var rf *resolveFailure
	if !errors.As(err, &rf) || rf.report == nil {
		t.Fatalf("resolveStream err = %v, want a resolveFailure with a report", err)
	}
	for _, a := range rf.report.Attempts {
		if a.Provider == "FlixHQWS" {
			return
		}
	}
	t.Fatalf("attempts = %+v, want FlixHQWS among the providers tried for the episode", rf.report.Attempts)
}

func withDefaultConfig(t *testing.T) {
	t.Helper()
	prev := cfg
	cfg = config.Default()
	cfg.TBCPLFeed = false
	t.Cleanup(func() { cfg = prev })
}

func TestStreamChainAsksAFlixHQWSPrimaryForTheEpisode(t *testing.T) {
	withDefaultConfig(t)
	got := streamChain(provider.NewFlixHQWS("flixhq.ws"))
	if !hasProvider[*provider.FlixHQWS](got) {
		t.Fatalf("streamChain(FlixHQWS) = %v, want FlixHQWS among the candidates: it listed the show and has no Watch of its own", providerNames(got))
	}
}

func TestStreamChainHoldsFlixHQWSOnceAndKeepsMagnetsLast(t *testing.T) {
	withDefaultConfig(t)
	cfg.TorrentFallback = true
	got := streamChain(provider.NewFlixHQWS("flixhq.ws"))
	n := 0
	for _, p := range got {
		if _, ok := p.(*provider.FlixHQWS); ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("FlixHQWS appears %d times in %v, want once", n, providerNames(got))
	}
	want := []string{"*provider.TBCPL", "*provider.EZTV", "*provider.FlixHQWS", "*provider.AnimeOnsen", "*provider.Nyaa", "*provider.YTS"}
	if fmt.Sprint(providerNames(got)) != fmt.Sprint(want) {
		t.Fatalf("chain = %v, want %v: the order the merge base had, with the resolver splitting the magnet tier off", providerNames(got), want)
	}
}

func TestStreamChainLeavesAStreamProviderPrimaryOut(t *testing.T) {
	withDefaultConfig(t)
	got := streamChain(provider.NewTBCPL("tbcpl"))
	if hasProvider[*provider.TBCPL](got) {
		t.Fatalf("streamChain(TBCPL) = %v, want TBCPL excluded: Watch already tried it", providerNames(got))
	}
}

func countingFlixHQWS(t *testing.T) (*provider.FlixHQWS, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return provider.NewFlixHQWS(ln.Addr().String()), &hits
}

func TestTryFallbackStreamAsksAFlixHQWSPrimary(t *testing.T) {
	chainTestConfig(t, false)
	withRecordingFallbackChain(t)
	primary, hits := countingFlixHQWS(t)

	_, _ = tryFallbackStream(primary, chainTestContent(), 1, 2)
	if hits.Load() == 0 {
		t.Fatal("FlixHQWS primary was never asked: tryFallbackStream must build its chain with streamChain")
	}
}

func TestOpenBatchStreamStreamingFallbackAsksAFlixHQWSPrimary(t *testing.T) {
	chainTestConfig(t, false)
	magnet := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: chainTestMagnet}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, magnet)
	prevSrv := newTorrentServer
	newTorrentServer = func(torrentstream.Options) (torrentServer, error) {
		return nil, errors.New("stubbed: no peers answered")
	}
	t.Cleanup(func() { newTorrentServer = prevSrv })

	primary, hits := countingFlixHQWS(t)
	_, _ = tryFallbackStream(primary, chainTestContent(), 1, 2)
	perCall := hits.Load()
	if perCall == 0 {
		t.Fatal("baseline: FlixHQWS primary was never asked")
	}
	hits.Store(0)

	_, _, _ = openBatchStream(primary, chainTestContent(), media.Episode{Number: 2, ID: "2"}, 1)
	if got := hits.Load(); got < 2*perCall {
		t.Fatalf("FlixHQWS asked %d times, want at least %d: the streaming retry after a failed torrent must include it", got, 2*perCall)
	}
}
