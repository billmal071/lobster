package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/torrentstream"
)

// The session loop handed its resolved stream straight to the player, which
// was fine while YTS — films only — was the one magnet source. Nyaa and EZTV
// answer with a magnet per episode, so a series played under either reached
// the player as "magnet:?..." and failed. The session path has to stand up
// the torrent server exactly as playStream does.
func TestPlayCurrentEpisodeServesAMagnetThroughTheTorrentServer(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})

	called := false
	prev := newTorrentServer
	newTorrentServer = func(opts torrentstream.Options) (torrentServer, error) {
		called = true
		return nil, errors.New("stubbed: no client started")
	}
	t.Cleanup(func() { newTorrentServer = prev })

	// An empty chain: the primary's failure moves the loop onto the
	// fallback, which must not reach the network from a test.
	chain := withRecordingFallbackChain(t)

	prov := &stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}}
	err := playCurrentEpisode(sessionForTest(prov))
	if !called {
		t.Fatal("playCurrentEpisode handed a magnet to the player without standing up the torrent server")
	}
	if err == nil {
		t.Fatal("err = nil, want the failure to surface once every source is exhausted")
	}
	if len(chain.seen()) != 1 {
		t.Fatalf("fallback chain asked %d times, want 1: the primary's dead swarm must be excluded and the next source tried", len(chain.seen()))
	}
}

// A swarm that never answers is a failed source, not the end of the session:
// the loop excludes it and plays the next source, exactly as it does when a
// player fails on a Primary stream.
func TestPlayCurrentEpisodeTriesTheNextSourceWhenTheSwarmIsDead(t *testing.T) {
	stub := &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}
	playStreamHarness(t, stub)

	prev := newTorrentServer
	newTorrentServer = func(opts torrentstream.Options) (torrentServer, error) {
		return nil, errors.New("stubbed: no peers answered")
	}
	t.Cleanup(func() { newTorrentServer = prev })

	// The resolver validates the fallback's stream with a GET, so it has to
	// be a URL something answers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
	}))
	t.Cleanup(srv.Close)
	next := &magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: srv.URL + "/live.m3u8"}},
		results:            []media.SearchResult{{ID: "fb/s", Title: "S", Type: media.TV}},
	}
	withRecordingFallbackChain(t, next)

	prov := &stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}}
	sess := sessionForTest(prov)
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode = %v, want the next source to play after the dead swarm", err)
	}
	if sess.LastPosition != 10 {
		t.Fatalf("LastPosition = %v, want the stub player's 10: the fallback stream was not played", sess.LastPosition)
	}
}

// fakeTorrentServer answers Serve without a swarm.
type fakeTorrentServer struct {
	url    string
	closed bool
}

func (f *fakeTorrentServer) Serve(string) (string, error) { return f.url, nil }
func (f *fakeTorrentServer) Close() error                 { f.closed = true; return nil }
func (f *fakeTorrentServer) DataDir() string              { return "/tmp/fake" }

// The player reads LocalTorrent to wait indefinitely on a stalled swarm
// (mpvNetworkTimeoutArg), so serveTorrent has to set it on the copy it hands
// back, and leave the caller's stream untouched for history.
func TestServeTorrentMarksTheStreamLocalTorrent(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	fake := &fakeTorrentServer{url: "http://127.0.0.1:1/stream/0"}
	prev := newTorrentServer
	newTorrentServer = func(opts torrentstream.Options) (torrentServer, error) { return fake, nil }
	t.Cleanup(func() { newTorrentServer = prev })

	in := &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000", Referer: "https://ref/"}
	out, closeTorrent, err := serveTorrent(in)
	if err != nil {
		t.Fatalf("serveTorrent: %v", err)
	}
	if !out.LocalTorrent || out.URL != fake.url || out.Referer != "" {
		t.Fatalf("served stream = %+v, want LocalTorrent on the loopback URL with no Referer", out)
	}
	if in.LocalTorrent || !strings.HasPrefix(in.URL, "magnet:") {
		t.Fatalf("caller's stream was mutated: %+v", in)
	}
	closeTorrent()
	if !fake.closed {
		t.Fatal("cleanup did not close the torrent server")
	}
}

// --download on the session path goes through the torrent server too, and
// asks for the whole file so a download that cannot fit is refused up front.
func TestPlayCurrentEpisodeDownloadServesAMagnetWholeFile(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	flagDownload = t.TempDir()

	var got torrentstream.Options
	called := false
	prev := newTorrentServer
	newTorrentServer = func(opts torrentstream.Options) (torrentServer, error) {
		got, called = opts, true
		return nil, errors.New("stubbed: no client started")
	}
	t.Cleanup(func() { newTorrentServer = prev })

	prov := &stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}}
	err := playCurrentEpisode(sessionForTest(prov))
	if !called {
		t.Fatal("--download handed a magnet to the downloader without standing up the torrent server")
	}
	if !got.WholeFile {
		t.Fatal("WholeFile = false for a download; the free-space check must refuse, not warn")
	}
	if err == nil || !strings.Contains(err.Error(), "starting torrent stream") {
		t.Fatalf("err = %v, want the stubbed torrent server error to surface (download mode has no retry)", err)
	}
}

// --json on the session path refuses a magnet the way playStream does: no
// URL it could print would be open by the time the caller used it.
func TestPlayCurrentEpisodeJSONRefusesAMagnet(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{})
	withOutputFlags(t, true, "")
	withRecordingFallbackChain(t)

	prov := &stubStreamProvider{stream: &media.Stream{URL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"}}
	err := playCurrentEpisode(sessionForTest(prov))
	if err == nil || !strings.Contains(err.Error(), "--json cannot express") {
		t.Fatalf("err = %v, want a refusal naming --json", err)
	}
}
