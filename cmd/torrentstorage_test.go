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
		// Base reaches this from a flag as well as the file, and neither is
		// case-normalised on the way in.
		{"yts spelled loudly", &config.Config{Base: "YTS"}, true},
		{"the fallback can reach yts from any base", &config.Config{Base: "flixhq.to", TorrentFallback: true}, true},
		{"an http provider with no fallback never streams", &config.Config{Base: "flixhq.to"}, false},
		{"an empty base with no fallback never streams", &config.Config{}, false},
		// The default. base = "auto" routes every movie to YTS
		// (routeByType, cmd/typeroute.go), so the default install streams
		// torrents and needs the backend that cannot SIGBUS just as much as
		// an explicit --base yts does.
		{"the auto base routes movies to yts", &config.Config{Base: config.BaseAuto}, true},
		{"auto spelled loudly", &config.Config{Base: "AUTO"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mayStreamTorrent(c.cfg); got != c.want {
				t.Errorf("mayStreamTorrent(%+v) = %v, want %v", c.cfg, got, c.want)
			}
		})
	}
}
