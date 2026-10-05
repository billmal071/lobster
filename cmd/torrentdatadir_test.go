package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"lobster/internal/media"
	"lobster/internal/torrentstream"
)

// playStream used to call torrentstream.New("") unconditionally, so every
// torrent payload landed under os.TempDir() whatever the config said — on this
// machine a 39G root filesystem rather than the 304G home volume. The
// configured directory has to reach the server.
func TestPlayStreamPassesTheConfiguredTorrentDir(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	want := filepath.Join(t.TempDir(), "torrents")
	cfg.TorrentDir = want

	var got string
	called := false
	prev := newTorrentServer
	newTorrentServer = func(dataDir string) (*torrentstream.Server, error) {
		got, called = dataDir, true
		return nil, errors.New("stubbed: no client started")
	}
	t.Cleanup(func() { newTorrentServer = prev })

	stream := &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}
	sel := media.SearchResult{ID: "movie/x", Title: "X", Type: media.Movie}
	if err := playStream(stream, "X", sel, 0, 0); err == nil {
		t.Fatal("playStream returned nil; the stubbed server error must surface")
	}

	if !called {
		t.Fatal("playStream never stood up a torrent server for a magnet URL")
	}
	if got != want {
		t.Errorf("torrent server data dir = %q, want the configured %q", got, want)
	}
}

// A tilde in torrent_dir is what a user writes; playStream must hand over the
// expanded path, not a literal "~" directory created in the working directory.
func TestPlayStreamExpandsTildeInTorrentDir(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg.TorrentDir = "~/torrents"

	var got string
	prev := newTorrentServer
	newTorrentServer = func(dataDir string) (*torrentstream.Server, error) {
		got = dataDir
		return nil, errors.New("stubbed: no client started")
	}
	t.Cleanup(func() { newTorrentServer = prev })

	stream := &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}
	sel := media.SearchResult{ID: "movie/x", Title: "X", Type: media.Movie}
	if err := playStream(stream, "X", sel, 0, 0); err == nil {
		t.Fatal("playStream returned nil; the stubbed server error must surface")
	}
	if want := filepath.Join(home, "torrents"); got != want {
		t.Errorf("torrent server data dir = %q, want %q", got, want)
	}
}
