package cmd

import (
	"reflect"
	"testing"

	"lobster/internal/config"
	"lobster/internal/tbcpl"
)

// TestLiveTVSourcesMergesTBCPL verifies liveTVSources merges TBCPL live
// playlists with configured IPTV sources without dropping the latter, and
// without duplicating a playlist the config already lists.
func TestLiveTVSourcesMergesTBCPL(t *testing.T) {
	const (
		configured = "https://local.example/mine.m3u8"
		fromFeed   = "https://feed.example/live.m3u8"
		shared     = "https://both.example/dup.m3u8"
	)
	seedTBCPLCatalog(t,
		&config.Config{
			TBCPLFeed: true,
			LiveTV:    config.LiveTVConfig{IPTVOrg: false, Playlists: []string{configured, shared}},
		},
		&tbcpl.Catalog{Sites: []tbcpl.Site{
			{Name: "Feed", URL: fromFeed, Category: "livetv", Status: "trusted", Enabled: true},
			{Name: "Dup", URL: shared, Category: "livetv", Status: "trusted", Enabled: true},
			{Name: "Untrusted", URL: "https://bad.example/x.m3u8", Category: "livetv", Status: "new", Enabled: true},
			{Name: "Movies", URL: "https://movies.example/y.m3u8", Category: "movies", Status: "trusted", Enabled: true},
		}},
	)

	got := liveTVSources()
	want := []string{configured, shared, fromFeed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("liveTVSources() = %v, want %v", got, want)
	}
}

// With the feed off, liveTVSources must return the configured sources verbatim.
func TestLiveTVSourcesFeedDisabled(t *testing.T) {
	const configured = "https://local.example/mine.m3u8"
	seedTBCPLCatalog(t,
		&config.Config{
			TBCPLFeed: false,
			LiveTV:    config.LiveTVConfig{IPTVOrg: false, Playlists: []string{configured}},
		},
		nil,
	)

	got := liveTVSources()
	if !reflect.DeepEqual(got, []string{configured}) {
		t.Fatalf("liveTVSources() = %v, want %v", got, []string{configured})
	}
}

// liveTVSources dereferences cfg on its first line but nil-checks it a few
// lines later; the nil path must not panic.
func TestLiveTVSourcesNilConfig(t *testing.T) {
	seedTBCPLCatalog(t, nil, nil)
	if got := liveTVSources(); len(got) != 0 {
		t.Fatalf("liveTVSources() = %v, want empty", got)
	}
}
