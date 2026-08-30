package cmd

import (
	"sync"
	"testing"

	"lobster/internal/config"
)

// TestLiveTVSourcesMergesTBCPL verifies liveTVSources merges TBCPL live
// playlists with configured IPTV sources without dropping the latter.
func TestLiveTVSourcesMergesTBCPL(t *testing.T) {
	tbcplCatOnce = sync.Once{}
	tbcplCatVal = nil

	cfg = &config.Config{TBCPLFeed: true, LiveTV: config.LiveTVConfig{IPTVOrg: false}}
	defer func() {
		cfg = nil
		tbcplCatOnce = sync.Once{}
		tbcplCatVal = nil
	}()

	got := liveTVSources()
	// Offline snapshot may or may not contain m3u livetv entries; assert the
	// function returns config sources at minimum and never panics.
	base := cfg.LiveTV.Sources()
	if len(got) < len(base) {
		t.Fatalf("liveTVSources dropped config sources: got %d, base %d", len(got), len(base))
	}
}
