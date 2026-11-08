package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/torrentstream"
)

type sequencedPlayer struct {
	errs   []error
	played []string
}

// Play records the stream URL and returns the next scripted error, or success once the script is exhausted.
func (p *sequencedPlayer) Play(s *media.Stream, _ string, _ float64, _ []string) (player.PlayResult, error) {
	p.played = append(p.played, s.URL)
	var err error
	if len(p.played) <= len(p.errs) {
		err = p.errs[len(p.played)-1]
	}
	return player.PlayResult{}, err
}
func (p *sequencedPlayer) Name() string    { return "stub" }
func (p *sequencedPlayer) Available() bool { return true }

func heldMagnetHarness(t *testing.T, p player.Player) (httpURL string) {
	t.Helper()
	playStreamHarness(t, p)
	chainTestConfig(t, false)
	prev := newTorrentServer
	newTorrentServer = func(torrentstream.Options) (torrentServer, error) {
		return &fakeTorrentServer{url: "http://127.0.0.1:1/stream/0"}, nil
	}
	t.Cleanup(func() { newTorrentServer = prev })
	httpURL = stubStreamServer(t)
	fb := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: httpURL}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, fb)
	return httpURL
}

// TestPlayCurrentEpisodeTriesTheHeldRecoveredMagnetWhenTheFallbackStreamFails checks that the recovered torrent source's magnet is played after the HTTP fallback stream fails.
func TestPlayCurrentEpisodeTriesTheHeldRecoveredMagnetWhenTheFallbackStreamFails(t *testing.T) {
	p := &sequencedPlayer{errs: []error{errors.New("stream died")}}
	httpURL := heldMagnetHarness(t, p)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode = %v; the recovered torrent was never tried", err)
	}
	if len(p.played) != 2 || p.played[0] != httpURL || p.played[1] != "http://127.0.0.1:1/stream/0" {
		t.Fatalf("played = %v, want the HTTP fallback, then the served torrent", p.played)
	}
}

// TestPlayCurrentEpisodeReportsTheFallbackFailureWhenNoMagnetIsHeld checks that the playback failure is returned, after one attempt, when the session holds no torrent magnet.
func TestPlayCurrentEpisodeReportsTheFallbackFailureWhenNoMagnetIsHeld(t *testing.T) {
	p := &sequencedPlayer{errs: []error{errors.New("stream died")}}
	heldMagnetHarness(t, p)

	sess := recoveredTorrentSession(&stubProvider{}, &stubProvider{})
	err := playCurrentEpisode(sess)
	if err == nil || !strings.Contains(err.Error(), "playback failed") {
		t.Fatalf("err = %v, want the original playback failure", err)
	}
	if len(p.played) != 1 {
		t.Fatalf("played = %v, want exactly one attempt", p.played)
	}
}

// TestPlayCurrentEpisodeKeepsPlaybackErrorWhenHeldMagnetAlsoFails checks that the original playback error stays reachable with errors.Is when the held magnet also fails.
func TestPlayCurrentEpisodeKeepsPlaybackErrorWhenHeldMagnetAlsoFails(t *testing.T) {
	streamDied, swarmStalled := errors.New("stream died"), errors.New("swarm stalled")
	p := &sequencedPlayer{errs: []error{streamDied, swarmStalled}}
	heldMagnetHarness(t, p)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	err := playCurrentEpisode(sess)
	if !errors.Is(err, streamDied) {
		t.Fatalf("err = %v, want the original playback error (stream died) preserved", err)
	}
	if errors.Is(err, swarmStalled) {
		t.Fatalf("err = %v, the held magnet's failure replaced the original one", err)
	}
	if len(p.played) != 2 {
		t.Fatalf("played = %v, want the fallback then the held magnet, no more", p.played)
	}
}

// TestResolveStreamDoesNotRepickTheHeldTorrentSourceFromTheChain checks that the held torrent source is not picked a second time from the fallback chain.
func TestResolveStreamDoesNotRepickTheHeldTorrentSourceFromTheChain(t *testing.T) {
	chainTestConfig(t, false)
	const other = "magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Other"
	dup := &torrentDouble{magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: other}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, dup)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	stream, name, err := resolveStream(sess, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if stream.URL != chainTestMagnet || name != "Primary" {
		t.Fatalf("stream = %q (%s), want the held recovered magnet, not the same source again from the chain", stream.URL, name)
	}
}

type magnetKeyedServer struct {
	fakeTorrentServer
	failMagnet string
}

// Serve fails for failMagnet and otherwise returns a URL that embeds the magnet, so a test can tell which one was served.
func (m *magnetKeyedServer) Serve(magnet string) (string, error) {
	if magnet == m.failMagnet {
		return "", errors.New("no peers answered")
	}
	return "http://127.0.0.1:1/stream/" + magnet, nil
}

// TestPlayCurrentEpisodeFallbackTorrentServeFailureTriesTheHeldMagnet checks that when the fallback torrent cannot be served, the held recovered magnet is the one served and played next.
func TestPlayCurrentEpisodeFallbackTorrentServeFailureTriesTheHeldMagnet(t *testing.T) {
	p := &sequencedPlayer{}
	playStreamHarness(t, p)
	chainTestConfig(t, false)
	const other = "magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Other"
	prev := newTorrentServer
	newTorrentServer = func(torrentstream.Options) (torrentServer, error) {
		return &magnetKeyedServer{failMagnet: other}, nil
	}
	t.Cleanup(func() { newTorrentServer = prev })
	eztv := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: other}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, eztv)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode = %v; the held magnet was never tried after the fallback swarm died", err)
	}
	want := "http://127.0.0.1:1/stream/" + chainTestMagnet
	if len(p.played) != 1 || p.played[0] != want {
		t.Fatalf("played = %v, want the held magnet served once as %q", p.played, want)
	}
}

type watchOnce struct {
	*torrentDouble
	calls int
}

// Watch answers like the wrapped torrent source on the first call and fails on every later one.
func (w *watchOnce) Watch(a, b, c, d string) (*media.Stream, error) {
	w.calls++
	if w.calls > 1 {
		return nil, errors.New("episode release list expired")
	}
	return w.torrentDouble.Watch(a, b, c, d)
}

// TestPlayCurrentEpisodeKeepsTheHeldMagnetAfterALaterWatchFails checks that the magnet received from the first Watch is still tried when a later Watch of the source fails.
func TestPlayCurrentEpisodeKeepsTheHeldMagnetAfterALaterWatchFails(t *testing.T) {
	p := &sequencedPlayer{errs: []error{errors.New("stream died")}}
	httpURL := heldMagnetHarness(t, p)

	sess := recoveredTorrentSession(&watchOnce{torrentDouble: newTorrentSource()}, &stubProvider{})
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode = %v; the magnet held from the first Watch was lost when the second failed", err)
	}
	if len(p.played) != 2 || p.played[0] != httpURL || p.played[1] != "http://127.0.0.1:1/stream/0" {
		t.Fatalf("played = %v, want the HTTP fallback, then the retained torrent", p.played)
	}
}
