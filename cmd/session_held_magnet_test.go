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

func TestPlayCurrentEpisodeKeepsPlaybackErrorWhenHeldMagnetAlsoFails(t *testing.T) {
	p := &sequencedPlayer{errs: []error{errors.New("stream died"), errors.New("swarm stalled")}}
	heldMagnetHarness(t, p)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	err := playCurrentEpisode(sess)
	if err == nil || !strings.Contains(err.Error(), "playback failed") {
		t.Fatalf("err = %v, want a playback failure", err)
	}
	if len(p.played) != 2 {
		t.Fatalf("played = %v, want the fallback then the held magnet, no more", p.played)
	}
}

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

func TestPlayCurrentEpisodeFallbackTorrentServeFailureTriesTheHeldMagnet(t *testing.T) {
	p := &sequencedPlayer{}
	playStreamHarness(t, p)
	chainTestConfig(t, false)
	calls := 0
	prev := newTorrentServer
	newTorrentServer = func(torrentstream.Options) (torrentServer, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("no peers answered")
		}
		return &fakeTorrentServer{url: "http://127.0.0.1:1/stream/0"}, nil
	}
	t.Cleanup(func() { newTorrentServer = prev })
	const other = "magnet:?xt=urn:btih:89abcdef0123456789abcdef0123456789abcdef&dn=Other"
	eztv := &recordingSP{magnetProvider: magnetProvider{
		stubStreamProvider: stubStreamProvider{stream: &media.Stream{URL: other}},
		results:            []media.SearchResult{chainTestContent()},
	}}
	withRecordingFallbackChain(t, eztv)

	sess := recoveredTorrentSession(newTorrentSource(), &stubProvider{})
	if err := playCurrentEpisode(sess); err != nil {
		t.Fatalf("playCurrentEpisode = %v; the held magnet was never tried after the fallback swarm died", err)
	}
	if len(p.played) != 1 || p.played[0] != "http://127.0.0.1:1/stream/0" {
		t.Fatalf("played = %v, want the held magnet served once", p.played)
	}
}
