package player

import (
	"testing"

	"lobster/internal/media"
)

func TestMPVNetworkTimeoutArgTorrentStreamNeverTimesOut(t *testing.T) {
	st := &media.Stream{URL: "http://127.0.0.1:56764/stream/abc", LocalTorrent: true}
	if got := mpvNetworkTimeoutArg(st); got != "--network-timeout=0" {
		t.Fatalf("torrent stream arg = %q, want --network-timeout=0: a swarm stall must read as buffering, not a dead stream", got)
	}
}

func TestMPVNetworkTimeoutArgRemoteStreamKeepsShortTimeout(t *testing.T) {
	st := &media.Stream{URL: "https://cdn.example/x.m3u8"}
	if got := mpvNetworkTimeoutArg(st); got != "--network-timeout=15" {
		t.Fatalf("remote stream arg = %q, want --network-timeout=15", got)
	}
}
