package cmd

import (
	"errors"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
)

func TestPlayLiveSurfOpensBrowserChannelWithoutConfiguredPlayer(t *testing.T) {
	prevCfg := cfg
	cfg = config.Default()
	cfg.Player = "mpv"
	t.Cleanup(func() { cfg = prevCfg })
	t.Setenv("PATH", "")

	prevOpen := agentOpenBrowser
	opened := ""
	agentOpenBrowser = func(rawURL string) error {
		opened = rawURL
		return nil
	}
	t.Cleanup(func() { agentOpenBrowser = prevOpen })

	prevMenu := agentSurfMenu
	agentSurfMenu = func(string) (surfAction, error) { return surfQuit, nil }
	t.Cleanup(func() { agentSurfMenu = prevMenu })

	stream := &media.Stream{
		URL:        "https://1ball.pk/watch/example-match",
		BrowserURL: "https://1ball.pk/watch/example-match",
	}
	provider := &stubStreamProvider{stream: stream}
	lineup := []media.SearchResult{{ID: "example-match", Title: "Example Match"}}

	if err := playLiveSurf(provider, lineup, 0); err != nil {
		t.Fatalf("playLiveSurf: %v", err)
	}
	if opened != stream.BrowserURL {
		t.Fatalf("opened URL = %q, want %q", opened, stream.BrowserURL)
	}
}

func TestPlayLiveSurfStillRequiresPlayerForDirectStreams(t *testing.T) {
	prevCfg := cfg
	cfg = config.Default()
	cfg.Player = "mpv"
	t.Cleanup(func() { cfg = prevCfg })
	t.Setenv("PATH", "")

	provider := &stubStreamProvider{stream: &media.Stream{URL: "https://cdn.invalid/live.m3u8"}}
	lineup := []media.SearchResult{{ID: "channel", Title: "Channel"}}
	err := playLiveSurf(provider, lineup, 0)
	if !errors.Is(err, errSurfPlayerUnavailable) {
		t.Fatalf("playLiveSurf error = %v, want player-unavailable error", err)
	}
}
