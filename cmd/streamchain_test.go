package cmd

import (
	"errors"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/playlist"
	"lobster/internal/provider"
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
	seenMagnet := false
	for _, p := range got {
		if _, ok := p.(*provider.FlixHQWS); ok {
			n++
		}
		if servesMagnets(p) {
			seenMagnet = true
		}
	}
	if n != 1 {
		t.Fatalf("FlixHQWS appears %d times in %v, want once", n, providerNames(got))
	}
	if !seenMagnet {
		t.Fatalf("torrent_fallback on but no magnet source in %v", providerNames(got))
	}
	if len(withoutMagnetSources(got))+len(onlyMagnetSources(got)) != len(got) {
		t.Fatalf("chain %v does not split cleanly into streaming and magnet tiers", providerNames(got))
	}
}

func TestStreamChainLeavesAStreamProviderPrimaryOut(t *testing.T) {
	withDefaultConfig(t)
	got := streamChain(provider.NewTBCPL("tbcpl"))
	if hasProvider[*provider.TBCPL](got) {
		t.Fatalf("streamChain(TBCPL) = %v, want TBCPL excluded: Watch already tried it", providerNames(got))
	}
}
