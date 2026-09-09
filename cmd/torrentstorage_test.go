package cmd

import (
	"testing"

	"lobster/internal/config"
)

// mayStreamTorrent decides whether the storage backend matters for a run, and
// so whether applyConfig re-execs. Getting it wrong in one direction leaves the
// SIGBUS-prone backend in place for a run that streams; in the other it
// re-execs runs that never touch a torrent.
func TestMayStreamTorrent(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"no config at all", nil, false},
		{"yts as the base resolves to magnets", &config.Config{Base: "yts"}, true},
		// config.Validate lower-cases Base before any reader sees it, so a
		// loud spelling does not reach here from a real run. The EqualFold
		// below is what covers a cfg assembled without Validate — which is
		// what every fixture in this file is.
		{"yts spelled loudly", &config.Config{Base: "YTS"}, true},
		{"the fallback can reach yts from any base", &config.Config{Base: "flixhq.to", TorrentFallback: true}, true},
		{"an http provider with no fallback never streams", &config.Config{Base: "flixhq.to"}, false},
		{"an empty base with no fallback never streams", &config.Config{}, false},
		// The default. base = "auto" routes every movie to YTS
		// (routeByType, cmd/typeroute.go), so the default install streams
		// torrents and needs the backend that cannot SIGBUS just as much as
		// an explicit --base yts does.
		{"the auto base routes movies to yts", &config.Config{Base: config.BaseAuto}, true},
		// Deliberately not a claim about routing: this fixture can only see
		// mayStreamTorrent, and mayStreamTorrent is the one reader of Base
		// that folds case. Whether a loudly spelled auto actually routes
		// movies to YTS depends on baseIsAuto and newProvider agreeing too,
		// and that is asserted end to end in
		// TestALoudlySpelledAutoIsAutoForEveryReaderOfBase (autobase_test.go).
		{"an unvalidated loud auto still reads as auto here", &config.Config{Base: "AUTO"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mayStreamTorrent(c.cfg); got != c.want {
				t.Errorf("mayStreamTorrent(%+v) = %v, want %v", c.cfg, got, c.want)
			}
		})
	}
}

// withOutputFlags sets the two flags that make a run produce output rather
// than playback, and restores them afterwards.
func withOutputFlags(t *testing.T, jsonOut bool, dl string) {
	t.Helper()
	prevJSON, prevDL := flagJSON, flagDownload
	flagJSON, flagDownload = jsonOut, dl
	t.Cleanup(func() { flagJSON, flagDownload = prevJSON, prevDL })
}

// Under the auto base the route is the only thing that reaches YTS, and
// routeByType returns early for both --json and --download, so neither run can
// open a magnet however it ends. Saying "yes" for one of them costs a re-exec,
// or on Windows (canExec false) an ungated SIGBUS notice on stderr.
//
// This covers the output flags only. It says nothing about which subcommand is
// running: `lobster version` passes neither flag, so this function answered
// "yes" for it and every other non-playback command until the gate moved to
// loadConfig. That gate is asserted in
// TestOnlyCommandsThatCanPlayChooseTheStorageBackend (storagegate_test.go),
// which watches the call rather than the environment — the
// TORRENT_STORAGE_DEFAULT_FILE_IO that TestMain presets makes the mechanism
// itself a no-op under test, so any fixture reading the environment would see
// nothing.
func TestMayStreamTorrentUnderAutoIgnoresRunsTheRouteRefuses(t *testing.T) {
	auto := &config.Config{Base: config.BaseAuto}

	t.Run("--json is never routed to YTS", func(t *testing.T) {
		withOutputFlags(t, true, "")
		if mayStreamTorrent(auto) {
			t.Fatalf("mayStreamTorrent said a --json run may open a magnet; routeByType returns early for it")
		}
	})
	t.Run("--download is never routed to YTS", func(t *testing.T) {
		withOutputFlags(t, false, t.TempDir())
		if mayStreamTorrent(auto) {
			t.Fatalf("mayStreamTorrent said a --download run may open a magnet; routeByType returns early for it")
		}
	})
	t.Run("a plain auto run still may", func(t *testing.T) {
		withOutputFlags(t, false, "")
		if !mayStreamTorrent(auto) {
			t.Fatalf("mayStreamTorrent said a default install cannot stream; the route sends every movie to YTS")
		}
	})
}

// The other two arms are the user naming a torrent source outright, and those
// do not go through the route at all: --base yts makes YTS the primary and
// torrent_fallback puts it in the fallback chain, both of which resolve a
// magnet whatever the output flags say.
func TestMayStreamTorrentKeepsAnExplicitTorrentSourceUnderOutputFlags(t *testing.T) {
	withOutputFlags(t, true, t.TempDir())
	for _, c := range []struct {
		name string
		cfg  *config.Config
	}{
		{"--base yts", &config.Config{Base: "yts"}},
		{"torrent_fallback", &config.Config{Base: "flixhq.to", TorrentFallback: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !mayStreamTorrent(c.cfg) {
				t.Fatalf("mayStreamTorrent(%+v) = false under output flags; the user named a torrent source", c.cfg)
			}
		})
	}
}
