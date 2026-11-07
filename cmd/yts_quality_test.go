package cmd

import (
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
)

// serverRecordingProvider is a StreamProvider whose GetServers lists YTS-shaped
// entries (highest quality first, as provider.YTS does) and whose Watch records
// the server name it was handed. That name is the whole observable effect of
// the command path's server ordering.
type serverRecordingProvider struct {
	stubStreamProvider
	servers []media.Server
	watched []string
}

func (p *serverRecordingProvider) GetServers(string, string) ([]media.Server, error) {
	return p.servers, nil
}

func (p *serverRecordingProvider) Watch(_, _, server, _ string) (*media.Stream, error) {
	p.watched = append(p.watched, server)
	return p.stream, nil
}

func ytsShapedServers() []media.Server {
	return []media.Server{
		{Name: "2160p bluray (5.98 GB, 47 seeds)", ID: "0"},
		{Name: "1080p bluray (1.75 GB, 100 seeds)", ID: "1"},
		{Name: "720p bluray (900.47 MB, 33 seeds)", ID: "2"},
	}
}

func TestPreferQualityServers(t *testing.T) {
	names := func(s []media.Server) []string {
		out := make([]string, len(s))
		for i, srv := range s {
			out[i] = srv.Name
		}
		return out
	}
	cases := []struct {
		name    string
		quality string
		first   string
	}{
		{"explicit 1080 moves 1080p first", "1080", "1080p bluray (1.75 GB, 100 seeds)"},
		{"explicit 1080p (with suffix) also matches", "1080p", "1080p bluray (1.75 GB, 100 seeds)"},
		{"720 moves 720p first", "720", "720p bluray (900.47 MB, 33 seeds)"},
		{"best keeps highest first", "best", "2160p bluray (5.98 GB, 47 seeds)"},
		{"empty keeps highest first", "", "2160p bluray (5.98 GB, 47 seeds)"},
		{"nothing at or below keeps order", "480", "2160p bluray (5.98 GB, 47 seeds)"},
		{"unstocked quality moves the closest lower first", "900", "720p bluray (900.47 MB, 33 seeds)"},
		{"unstocked 1440 moves 1080p first, not 2160p", "1440", "1080p bluray (1.75 GB, 100 seeds)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preferQualityServers(ytsShapedServers(), tc.quality)
			if len(got) != 3 {
				t.Fatalf("got %d servers, want 3: %v", len(got), names(got))
			}
			if got[0].Name != tc.first {
				t.Fatalf("first server = %q, want %q (order %v)", got[0].Name, tc.first, names(got))
			}
		})
	}

	// Non-YTS names carry no quality and must come through untouched.
	plain := []media.Server{{Name: "Vidcloud"}, {Name: "UpCloud"}}
	got := preferQualityServers(plain, "1080")
	if got[0].Name != "Vidcloud" || got[1].Name != "UpCloud" {
		t.Fatalf("plain server list reordered: %v", names(got))
	}
}

func TestResolveAndPlayPassesRequestedQualityServerFirst(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.Quality = "1080"
	cfg.Provider = "Default"

	prov := &serverRecordingProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}},
		servers:            ytsShapedServers(),
	}
	sel := media.SearchResult{ID: "yts/4003", Title: "Underworld", Year: "2003", Type: media.Movie}
	if err := resolveAndPlay(prov, sel, 0, 0); err != nil {
		t.Fatalf("resolveAndPlay: %v", err)
	}
	if len(prov.watched) == 0 {
		t.Fatal("Watch was never called")
	}
	if got := prov.watched[0]; got != "1080p bluray (1.75 GB, 100 seeds)" {
		t.Fatalf("first Watch server = %q, want the 1080p entry: --quality 1080 was overridden by the list order", got)
	}
}

// `-q best` is the one way to reach 2160p and must keep doing so.
func TestResolveAndPlayBestKeepsHighestQualityFirst(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.Quality = "best"
	cfg.Provider = "Default"

	prov := &serverRecordingProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"}},
		servers:            ytsShapedServers(),
	}
	sel := media.SearchResult{ID: "yts/4003", Title: "Underworld", Year: "2003", Type: media.Movie}
	if err := resolveAndPlay(prov, sel, 0, 0); err != nil {
		t.Fatalf("resolveAndPlay: %v", err)
	}
	if got := prov.watched[0]; got != "2160p bluray (5.98 GB, 47 seeds)" {
		t.Fatalf("first Watch server = %q, want the 2160p entry for -q best", got)
	}
}

func TestPreferQualityServersNeverPromotesAboveTheCeiling(t *testing.T) {
	srv := func(names ...string) []media.Server {
		out := make([]media.Server, len(names))
		for i, n := range names {
			out[i] = media.Server{Name: n}
		}
		return out
	}
	cases := []struct {
		name    string
		servers []media.Server
		quality string
		want    []string
	}{
		{"only above the ceiling keeps order", srv("2160p x", "1080p y", "720p z"), "480", []string{"2160p x", "1080p y", "720p z"}},
		{"closest lower beats a higher one listed first", srv("2160p x", "1080p y", "360p z", "240p w"), "480", []string{"360p z", "2160p x", "1080p y", "240p w"}},
		{"exact match beats higher and lower", srv("2160p x", "720p y", "1080p z"), "1080", []string{"1080p z", "2160p x", "720p y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preferQualityServers(tc.servers, tc.quality)
			for i, w := range tc.want {
				if got[i].Name != w {
					t.Fatalf("order[%d] = %q, want %q", i, got[i].Name, w)
				}
			}
		})
	}
}
